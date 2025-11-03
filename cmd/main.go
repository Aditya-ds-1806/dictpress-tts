package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/knadh/koanf"
	"golang.org/x/time/rate"
)

var (
	buildString = "dev"
	ko          = koanf.New(".")
	lo          = log.New(os.Stdout, "dictpress-tts: ", log.Ldate|log.Ltime|log.Lmicroseconds|log.Lshortfile)
)

// Config represents the app config.
type Config struct {
	DB              DBConfig  `koanf:"db"`
	TTS             TTSConfig `koanf:"tts"`
	Workers         int       `koanf:"workers"`
	TTSProviderName string    `koanf:"tts_provider"`
}

// DBConfig represents DB config.
type DBConfig struct {
	Host     string `koanf:"host"`
	Port     int    `koanf:"port"`
	Database string `koanf:"db"`
	Username string `koanf:"user"`
	Password string `koanf:"password"`
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

// fetchWords reads words from the database and sends them to the channel.
func fetchWords(ctx context.Context, srcLang string, db *sql.DB, wordCh chan<- Word) {
	defer close(wordCh)

	// Fetch words for a specific language, or all languages, from the DB.
	rows, err := db.QueryContext(ctx, "SELECT id, content FROM entries WHERE initial != '' AND (CASE WHEN $1 != '' THEN lang=$1 ELSE TRUE END) ORDER BY id", srcLang)
	if err != nil {
		lo.Fatalf("failed to fetch rows: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var w Word
		if err := rows.Scan(&w.ID, &w.Word); err != nil {
			lo.Printf("failed to scan row: %v", err)
			continue
		}
		wordCh <- w
	}

	if err := rows.Err(); err != nil {
		lo.Fatalf("error iterating rows: %w", err)
	}

	lo.Println("finished reading all words from DB")
}

// processWords processes words from the channel and generates TTS audio.
func processWords(ctx context.Context, provider TTSProvider, rateLimit float64, outputTemplate string, wordCh <-chan Word, wg *sync.WaitGroup) {
	defer wg.Done()

	limiter := rate.NewLimiter(rate.Limit(rateLimit), int(rateLimit))

	for word := range wordCh {
		if err := limiter.Wait(ctx); err != nil {
			lo.Printf("rate limiter error: %v", err)
			continue
		}

		filepath := strings.Replace(outputTemplate, "{}", strconv.Itoa(word.ID), 1)

		audioData, err := provider.PerformTTS(ctx, word.Word)
		if err != nil {
			lo.Printf("❌ failed to synthesize '%s': %v", word.Word, err)
			continue
		}

		if err := os.WriteFile(filepath, audioData, 0644); err != nil {
			lo.Printf("❌ failed to write file '%s': %v", filepath, err)
			continue
		}

		lo.Printf("✅ '%s' -> %s", word.Word, filepath)
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
	db, err := connectDB(cfg.DB)
	if err != nil {
		lo.Fatalf("database error: %v", err)
	}
	defer db.Close()
	lo.Printf("connected to database: %s:%d/%s", cfg.DB.Host, cfg.DB.Port, cfg.DB.Database)

	// Create the output directory.
	if err := createOutputDir(cfg.TTS.OutDir); err != nil {
		lo.Fatalf("output directory error: %v", err)
	}

	// Initialize TTS provider.
	ctx := context.Background()
	provider, err := initProvider(ctx, cfg.TTSProviderName, cfg.TTS)
	if err != nil {
		lo.Fatalf("failed to create TTS provider: %v", err)
	}
	defer provider.Close()

	var wg sync.WaitGroup
	wordCh := make(chan Word, 10000)

	perWorkerRate := cfg.TTS.ReqPerSec / float64(cfg.Workers)
	outputTemplate := fmt.Sprintf("%s/{}.%s", cfg.TTS.OutDir, cfg.TTS.OutputFormat)

	// Start the word fetch worker to feed the queue in batches.
	go fetchWords(ctx, "english", db, wordCh)

	// Setup worker pool.
	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		go processWords(ctx, provider, perWorkerRate, outputTemplate, wordCh, &wg)
	}

	// Wait for all workers to complete.
	wg.Wait()

	lo.Println("all workers finished")
}
