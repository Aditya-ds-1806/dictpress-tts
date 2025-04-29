package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

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

type Word struct {
	ID int
	Word string
}

func rateLimiter(limit int64) <- chan time.Time {
	return time.Tick(time.Minute/time.Duration(limit))
}

func CreateTTSClient(ctx context.Context, config* TTSConfig) (*texttospeech.Client, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
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

func PerformTTS(ctx context.Context, client *texttospeech.Client, text string, ttsConfig* TTSConfig) (*texttospeechpb.SynthesizeSpeechResponse, error) {
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

	res, err := client.SynthesizeSpeech(ctx, &req)

	return res, err
}

func PerformTTSAndSaveToFile(ctx context.Context, client *texttospeech.Client, word string, filename string, ttsConfig* TTSConfig) (*string, error) {
	res, err := PerformTTS(ctx, client, word, ttsConfig)
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

func main() {
	k := koanf.New(".")
	
	err := k.Load(file.Provider("config.toml"), toml.Parser())
	if err != nil {
		log.Fatalf("failed to load config.toml: %s", err);
	}

	var ttsConfig TTSConfig
	err = k.Unmarshal("tts", &ttsConfig)
	if err != nil {
		log.Fatalf("failed to parse [tts] from config.toml: %s", err);
	}

	var dbConfig DBConfig
	err = k.Unmarshal("db", &dbConfig)
	if err != nil {
		log.Fatalf("failed to parse [db] from config.toml: %s", err);
	}

	dbURI := fmt.Sprintf("postgres://%s:%d/%s", dbConfig.Host, dbConfig.Port, dbConfig.Database)

	db, err := sql.Open("pgx", dbURI)
	if err != nil {
		log.Fatalf("failed to connect to DB: %s", err)
	}

	defer db.Close()

	fmt.Printf("connected to DB: %s\n", dbURI)

	if ttsConfig.OutDir != "." {
		err := os.Mkdir(ttsConfig.OutDir, 0777)

		if err != nil {
			if !os.IsExist(err) {
				log.Fatalf("failed to create out dir: %s", err)
			}

			fmt.Println("out dir exists, skipping creation")
		}
	}

	ctx := context.Background()
	client, err := CreateTTSClient(ctx, &ttsConfig)
	if err != nil {
		log.Fatalf("Failed to create text-to-speech client: %v\n", err)
	}

	defer client.Close()

	rows, err := db.Query("SELECT id, content FROM entries WHERE initial != '' ORDER BY id")
	if err != nil {
		log.Fatalf("failed to fetch rows: %s", err)
	}

	defer rows.Close()
	
	var wg sync.WaitGroup
	wg.Add(2)
	
	wordChannel := make(chan Word, 1000)

	go func () {
		defer wg.Done()

		for rows.Next() {
			var id int;
			var word string;
	
			err := rows.Scan(&id, &word)
			if err != nil {
				fmt.Printf("failed to scan row")
				continue
			}
	
			wordChannel <- Word{id, word}
		}

		close(wordChannel)
	}()

	go func () {
		defer wg.Done()

		for word := range wordChannel {
			<-rateLimiter(ttsConfig.ReqPerMin)

			filepath, err := PerformTTSAndSaveToFile(ctx, client, word.Word, strconv.Itoa(word.ID), &ttsConfig)
			if (err != nil) {
				fmt.Printf("❌ Failed to perform TTS on word %s: %s\n", word.Word, err)
			} else {
				fmt.Printf("✅ Performed TTS on word %s: %s\n", word.Word, *filepath)
			}
		}
	}()

	wg.Wait()
}
