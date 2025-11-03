package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/knadh/koanf"
	"golang.org/x/time/rate"
)

var (
	buildString = "dev"
	ko          = koanf.New(".")
	lo          = log.New(os.Stdout, "dictpress-tts: ", log.Ldate|log.Ltime|log.Lmicroseconds|log.Lshortfile)

	// Track last processed ID of the word across workers.
	mut    sync.Mutex
	lastID int
)

// Config represents the app config.
type Config struct {
	TTS             TTSConfig `koanf:"tts"`
	Workers         int       `koanf:"workers"`
	TTSProviderName string    `koanf:"tts_provider"`
	FetchBatchSize  int       `koanf:"fetch_batch_size"`
}

// TTSConfig represents the backend TTS provider config.
type TTSConfig struct {
	APIKey       string  `koanf:"api_key"`
	LanguageCode string  `koanf:"language_code"`
	VoiceName    string  `koanf:"voice_name"`
	OutputFormat string  `koanf:"output_format"`
	OutDir       string  `koanf:"out_dir"`
	ReqPerSec    float64 `koanf:"req_per_sec"`
	SpeechRate   float64 `koanf:"speech_rate"`
	Pitch        float64 `koanf:"pitch"`
	VolumeGainDB float64 `koanf:"volume_gain_db"`
}

// Word represents a word entry from the dictpress database.
type Word struct {
	ID   int
	Word string
}

// TTSProvider is the interface for multiple backend TTS providers.
type TTSProvider interface {
	PerformTTS(ctx context.Context, text string) ([]byte, error)
	Close() error
}

// fetchWords reads words from the database in batches and sends them to the channel.
func fetchWords(ctx context.Context, srcLang string, db *sql.DB, batchSize int, lastId int, wordCh chan<- Word) {
	defer close(wordCh)

	var (
		n = 0
	)

	for {
		select {
		case <-ctx.Done():
			lo.Printf("cancelled")
			return
		default:
		}

		// Fetch words for a specific language, or all languages, from the DB in batches.
		query := "SELECT id, content FROM entries WHERE initial != '' AND (CASE WHEN $1 != '' THEN lang=$1 ELSE TRUE END) AND id > $2 ORDER BY id LIMIT $3"
		rows, err := db.QueryContext(ctx, query, srcLang, lastId, batchSize)
		if err != nil {
			lo.Printf("failed to fetch rows: %v", err)
			break
		}

		has := false
		for rows.Next() {
			var w Word
			if err := rows.Scan(&w.ID, &w.Word); err != nil {
				lo.Printf("failed to scan row: %v", err)
				continue
			}
			wordCh <- w
			lastId = w.ID
			n++
			has = true
		}
		rows.Close()

		if err := rows.Err(); err != nil {
			lo.Printf("error iterating rows: %v", err)
			break
		}

		lo.Printf("fetched %d words from DB", n)

		// Got 0 rows, all records have been read.
		if !has {
			break
		}
	}

	lo.Printf("fetched total %d words from DB", n)
}

// processWords processes words from the channel and generates TTS audio.
func processWords(ctx context.Context, provider TTSProvider, limiter *rate.Limiter, outputTemplate string, wordCh <-chan Word, wg *sync.WaitGroup) {
	defer wg.Done()

	for word := range wordCh {
		// Check if context is cancelled before processing.
		if ctx.Err() != nil {
			return
		}

		// If this provider has rate imiting, wait for the limiter.
		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				return
			}
		}

		filepath := strings.Replace(outputTemplate, "{}", strconv.Itoa(word.ID), 1)

		audioData, err := provider.PerformTTS(ctx, word.Word)
		if err != nil {
			lo.Printf("❌ error synthesizing '%s': %v", word.Word, err)
			continue
		}

		if err := os.WriteFile(filepath, audioData, 0644); err != nil {
			lo.Printf("❌ error writing file '%s': %v", filepath, err)
			continue
		}

		// Update global last processed ID.
		mut.Lock()
		if word.ID > lastID {
			lastID = word.ID
		}
		mut.Unlock()

		lo.Printf("✅ %d: '%s' -> %s", word.ID, word.Word, filepath)
	}
}

func main() {
	// Initialize commandline flags.
	initFlags(ko)

	// Display version.
	if ko.Bool("version") {
		fmt.Println(buildString)
		os.Exit(0)
	}

	// Initialize config.
	cfg := initConfig(ko)

	lo.Printf("loaded config from: %s", ko.Strings("config"))
	lo.Printf("TTS provider: %s, language: %s, voice: %s", cfg.TTSProviderName, cfg.TTS.LanguageCode, cfg.TTS.VoiceName)
	lo.Printf("output: %s/*.%s, workers: %d, rate: %.0f req/s", cfg.TTS.OutDir, cfg.TTS.OutputFormat, cfg.Workers, cfg.TTS.ReqPerSec)

	// Connect to database.
	db, err := connectDB(ko)
	if err != nil {
		lo.Fatalf("database error: %v", err)
	}
	defer db.Close()
	lo.Printf("connected to database: %s:%d/%s", ko.MustString("db.host"), ko.MustInt("db.port"), ko.MustString("db.db"))

	// Create the output directory.
	if err := createOutputDir(cfg.TTS.OutDir); err != nil {
		lo.Fatalf("output directory error: %v", err)
	}

	// Initialize TTS provider with cancellable context for graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	provider, err := initProvider(ctx, cfg.TTSProviderName, cfg.TTS)
	if err != nil {
		lo.Fatalf("failed to create TTS provider: %v", err)
	}
	defer provider.Close()

	var wg sync.WaitGroup
	wordCh := make(chan Word, 10000)

	// If a rate limit is set, create a limiter that waits across all workers.
	var limiter *rate.Limiter
	if cfg.TTS.ReqPerSec > 0 {
		limiter = rate.NewLimiter(rate.Limit(cfg.TTS.ReqPerSec), int(cfg.TTS.ReqPerSec))
	}

	outputTemplate := fmt.Sprintf("%s/{}.%s", cfg.TTS.OutDir, cfg.TTS.OutputFormat)

	// Start the word fetch worker to feed the queue in batches.
	go fetchWords(ctx, ko.String("lang"), db, cfg.FetchBatchSize, ko.Int("last-id"), wordCh)

	// Setup worker pool.
	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		go processWords(ctx, provider, limiter, outputTemplate, wordCh, &wg)
	}

	// Wait for all workers to complete.
	wg.Wait()

	// Print the last processed ID for resumption.
	mut.Lock()
	lastProcessedId := lastID
	mut.Unlock()

	lo.Printf("last processed ID: %d (resume with --last-id=%d)", lastProcessedId, lastProcessedId)
	lo.Printf("finished (cancelled=%v)", ctx.Err() != nil)
}
