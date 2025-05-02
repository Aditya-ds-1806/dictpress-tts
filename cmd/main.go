package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"sync"

	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/time/rate"

	types "dictpress-tts/internal/config"
	"dictpress-tts/internal/logger"
	"dictpress-tts/internal/providers"
)

type Word struct {
	ID int
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

func PerformTTSAndWriteToFile(word string, filename string, ttsConfig *types.TTSConfig) (*string, error) {
	provider := providers.TTSAdapter{TTSConfig: ttsConfig}

	bytes, err := provider.PerformTTS(word)
	if err != nil {
		
		return nil, err
	}

	filePath := fmt.Sprintf("%s/%s.%s", *ttsConfig.OutDir, filename, *ttsConfig.OutputFormat)

	err = WriteToFile(filePath, bytes)
	if err != nil {
		return nil, err
	}

	return &filePath, nil
}

func fetchWordsFromDB(db *sql.DB, wordChannel chan<-Word, wg *sync.WaitGroup) {
	defer wg.Done()

	rows, err := db.Query("SELECT id, content FROM entries WHERE initial != '' ORDER BY id")
	if err != nil {
		logger.Logger.Fatalf("failed to fetch rows: %s", err)
	}

	defer rows.Close()

	for rows.Next() {
		var id int;
		var word string;

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

func processWords(config *types.Config, wordChannel <-chan Word, wg *sync.WaitGroup) {
	defer wg.Done()

	ttsConfig := config.TTS
	reqPerSec := *ttsConfig.ReqPerSec / float64(*config.Workers)
	rateLimiter := rate.NewLimiter(rate.Limit(reqPerSec), int(reqPerSec))

	for word := range wordChannel {
		rateLimiter.Wait(context.Background())

		filepath, err := PerformTTSAndWriteToFile(word.Word, strconv.Itoa(word.ID), ttsConfig)
		if (err != nil) {
			logger.Logger.Printf("❌ failed to perform TTS on word %s: %s\n", word.Word, err)
		} else {
			logger.Logger.Printf("✅ performed TTS on word %s: %s\n", word.Word, *filepath)
		}
	}
}

func initApp() (types.Config, *sql.DB) {
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

	printConfig(*config.TTS)

	return *config, db
}

func init() {
	defineFlags()
}

func main() {
	config, db := initApp()

	defer db.Close()

	var wg sync.WaitGroup
	wordChannel := make(chan Word, chBufferSize)
	
	wg.Add(1)
	go fetchWordsFromDB(db, wordChannel, &wg)

	for range *config.Workers {
		wg.Add(1)
		go processWords(&config, wordChannel, &wg)
	}

	wg.Wait()
}
