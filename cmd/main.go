package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/time/rate"

	types "dictpress-tts/internal/config"
	"dictpress-tts/internal/logger"
	"dictpress-tts/internal/providers"
)

type Word struct {
	ID   int
	Word string
}

var (
	chBufferSize = 1000
)

func WriteToFile(filename string, data []byte) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(data)

	return err
}

func PerformTTSAndWriteToFile(provider *providers.TTSAdapter, word string, filepath string) error {
	bytes, err := provider.PerformTTS(word)
	if err != nil {
		return err
	}

	err = WriteToFile(filepath, bytes)
	if err != nil {
		return err
	}

	return nil
}

func fetchWordsFromDB(db *sql.DB, wordChannel chan<- Word, wg *sync.WaitGroup) {
	defer wg.Done()

	rows, err := db.Query("SELECT id, content FROM entries WHERE initial != '' ORDER BY id")
	if err != nil {
		logger.Logger.Fatalf("failed to fetch rows: %s", err)
	}

	defer rows.Close()

	for rows.Next() {
		var id int
		var word string

		err := rows.Scan(&id, &word)
		if err != nil {
			logger.Logger.Printf("failed to scan row")
			continue
		}

		wordChannel <- Word{id, word}
	}

	logger.Logger.Println("finished reading all words from DB, closing channel!")

	close(wordChannel)
}

func processWords(provider *providers.TTSAdapter, rateLimit float64, filepathTemplate string, wordChannel <-chan Word, wg *sync.WaitGroup) {
	defer wg.Done()

	rateLimiter := rate.NewLimiter(rate.Limit(rateLimit), int(rateLimit))

	for word := range wordChannel {
		rateLimiter.Wait(context.Background())

		filepath := strings.Replace(filepathTemplate, "{}", strconv.Itoa(word.ID), -1)

		err := PerformTTSAndWriteToFile(provider, word.Word, filepath)
		if err != nil {
			logger.Logger.Printf("❌ failed to perform TTS on word %s: %s\n", word.Word, err)
		} else {
			logger.Logger.Printf("✅ performed TTS on word %s: %s\n", word.Word, filepath)
		}
	}
}

func initApp() (types.Config, *sql.DB, *providers.TTSAdapter) {
	flagConfig := ParseFlagConf()

	if flagConfig != nil && *flagConfig.Version {
		fmt.Println("dictpress-tts", Version)
		os.Exit(0)
	}

	config := ParseTomlConf(*flagConfig.File)

	mergeStructs(config, flagConfig)

	// connect to postgres
	dbURI := fmt.Sprintf("postgres://%s:%d/%s", *config.DB.Host, *config.DB.Port, *config.DB.Database)

	db, err := sql.Open("pgx", dbURI)
	if err != nil {
		logger.Logger.Fatalf("failed to create db client: %s", err)
	}

	err = db.Ping()
	if err != nil {
		logger.Logger.Fatalf("failed to connect to DB: %s", err)
	}

	logger.Logger.Printf("connected to DB: %s\n", dbURI)

	// init out dir
	if *config.TTS.OutDir != "." {
		err := os.Mkdir(*config.TTS.OutDir, 0777)

		if err != nil {
			if !os.IsExist(err) {
				logger.Logger.Fatalf("failed to create out dir: %s", err)
			}

			logger.Logger.Printf("out dir: \"%s\" exists, skipping creation", *config.TTS.OutDir)
		}
	}

	provider := providers.TTSAdapter{TTSConfig: config.TTS}

	printConfig(*config.TTS)

	return *config, db, &provider
}

func init() {
	defineFlags()
}

func main() {
	config, db, provider := initApp()

	defer db.Close()
	defer provider.Close()

	var wg sync.WaitGroup
	wordChannel := make(chan Word, chBufferSize)

	rateLimit := *config.TTS.ReqPerSec / float64(*config.Workers)
	filepathTemplate := fmt.Sprintf("%s/{}.%s", *config.TTS.OutDir, *config.TTS.OutputFormat)

	wg.Add(1)
	go fetchWordsFromDB(db, wordChannel, &wg)

	for range *config.Workers {
		wg.Add(1)
		go processWords(provider, rateLimit, filepathTemplate, wordChannel, &wg)
	}

	wg.Wait()
}
