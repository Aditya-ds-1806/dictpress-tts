package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"

	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/time/rate"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/file"
	"google.golang.org/api/option"
)

type DBConfig struct {
	Host      string `koanf:"host"`
	Port      int    `koanf:"port"`
	Database  string `koanf:"db"`
	Username  string `koanf:"user"`
	Password  string `koanf:"password"`
}

type TTSConfig struct {
    Provider      string  `koanf:"provider"`
    APIKey        string  `koanf:"api_key"`
    LanguageCode  string  `koanf:"language_code"`
    VoiceName     string  `koanf:"voice_name"`
    OutputFormat  string  `koanf:"output_format"`
    OutDir        string  `koanf:"out_dir"`
    ReqPerMin     int64   `koanf:"req_per_min"`
    SpeechRate    float64 `koanf:"speech_rate"`
    Pitch         float64 `koanf:"pitch"`
    VolumeGainDB  float64 `koanf:"volume_gain_db"`
}

type Config struct {
	TTS TTSConfig
	DB DBConfig
}

type Word struct {
	ID int
	Word string
}

var (
	logger *log.Logger
	chBufferSize = 1000
)

func CreateTTSClient(ctx context.Context, config* TTSConfig) (*texttospeech.Client, error) {
	if config.APIKey == "" {
		logger.Fatalf("API key is required!")
	}

	return texttospeech.NewClient(ctx, option.WithAPIKey(config.APIKey))
}

func WriteToFile(filename string, data []byte) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(data)

	return err
}

func PerformTTS(client *texttospeech.Client, text string, ttsConfig* TTSConfig) (*texttospeechpb.SynthesizeSpeechResponse, error) {
	req := texttospeechpb.SynthesizeSpeechRequest{
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Text{Text: text},
		},
		Voice: &texttospeechpb.VoiceSelectionParams{
			Name: ttsConfig.VoiceName,
			LanguageCode: ttsConfig.LanguageCode,
		},
		AudioConfig: &texttospeechpb.AudioConfig{
			AudioEncoding: texttospeechpb.AudioEncoding_MP3,
			SpeakingRate: ttsConfig.SpeechRate,
			Pitch: ttsConfig.Pitch,
			VolumeGainDb: ttsConfig.VolumeGainDB,
		},
	}

	res, err := client.SynthesizeSpeech(context.Background(), &req)

	return res, err
}

func PerformTTSAndSaveToFile(client *texttospeech.Client, word string, filename string, ttsConfig* TTSConfig) (*string, error) {
	res, err := PerformTTS(client, word, ttsConfig)
	if err != nil {
		return nil, err
	}

	filePath := fmt.Sprintf("%s/%s.%s", ttsConfig.OutDir, filename, ttsConfig.OutputFormat)

	err = WriteToFile(filePath, res.AudioContent)
	if err != nil {
		return nil, err
	}

	return &filePath, nil
}

func ParseTomlConf() Config {
	k := koanf.New(".")
	err := k.Load(file.Provider("config.toml"), toml.Parser())

	if err != nil {
		logger.Fatalf("failed to load config.toml: %s", err);
	}

	var ttsConfig TTSConfig
	err = k.Unmarshal("tts", &ttsConfig)
	if err != nil {
		logger.Fatalf("failed to parse [tts] from config.toml: %s", err);
	}

	var dbConfig DBConfig
	err = k.Unmarshal("db", &dbConfig)
	if err != nil {
		logger.Fatalf("failed to parse [db] from config.toml: %s", err);
	}

	return Config{
		TTS: ttsConfig,
		DB: dbConfig,
	}
}

func initApp() (Config, *sql.DB, *texttospeech.Client) {
	// init logger
	logger = log.New(os.Stdout, "dictpress-tts: ", log.Ltime)

	// parser toml
	config := ParseTomlConf()

	// init out dir
	if config.TTS.OutDir != "." {
		err := os.Mkdir(config.TTS.OutDir, 0777)

		if err != nil {
			if !os.IsExist(err) {
				logger.Fatalf("failed to create out dir: %s", err)
			}

			logger.Println("out dir exists, skipping creation")
		}
	}

	// connect to postgres
	dbURI := fmt.Sprintf("postgres://%s:%d/%s", config.DB.Host, config.DB.Port, config.DB.Database)

	db, err := sql.Open("pgx", dbURI)
	if err != nil {
		logger.Fatalf("failed to connect to DB: %s", err)
	}

	logger.Printf("connected to DB: %s\n", dbURI)

	// create TTS Client
	ttsClient, err := CreateTTSClient(context.Background(), &config.TTS)
	if err != nil {
		logger.Fatalf("failed to create text-to-speech client: %v\n", err)
	}

	return config, db, ttsClient
}

func fetchWordsFromDB(db *sql.DB, wordChannel chan<-Word, wg *sync.WaitGroup) {
	defer wg.Done()

	rows, err := db.Query("SELECT id, content FROM entries WHERE initial != '' ORDER BY id")
	if err != nil {
		logger.Fatalf("failed to fetch rows: %s", err)
	}

	defer rows.Close()

	for rows.Next() {
		var id int;
		var word string;

		err := rows.Scan(&id, &word)
		if err != nil {
			logger.Printf("failed to scan row")
			continue
		}

		wordChannel <- Word{id, word}
	}

	logger.Println("finished reading all words from DB, closing channel!")

	close(wordChannel)
}

func processWords(ttsClient *texttospeech.Client, ttsConfig *TTSConfig, wordChannel <-chan Word, wg *sync.WaitGroup) {
	defer wg.Done()

	rateLimiter := rate.NewLimiter(rate.Limit(ttsConfig.ReqPerMin), int(ttsConfig.ReqPerMin))

	for word := range wordChannel {
		rateLimiter.Wait(context.Background())

		filepath, err := PerformTTSAndSaveToFile(ttsClient, word.Word, strconv.Itoa(word.ID), ttsConfig)
		if (err != nil) {
			logger.Printf("❌ failed to perform TTS on word %s: %s\n", word.Word, err)
		} else {
			logger.Printf("✅ performed TTS on word %s: %s\n", word.Word, *filepath)
		}
	}
}

func main() {
	config, db, ttsClient := initApp()

	defer db.Close()
	defer ttsClient.Close()

	wordChannel := make(chan Word, chBufferSize)
	
	var wg sync.WaitGroup
	wg.Add(2)
	
	go fetchWordsFromDB(db, wordChannel, &wg)
	go processWords(ttsClient, &config.TTS, wordChannel, &wg)

	wg.Wait()
}
