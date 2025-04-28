package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

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

func PerformTTSAndSaveToFile(ctx context.Context, client *texttospeech.Client, word string, filename string, ttsConfig* TTSConfig) error {
	res, err := PerformTTS(ctx, client, word, ttsConfig)
	if err != nil {
		return err
	}

	filePath := fmt.Sprintf("%s/%s.%s", ttsConfig.OutDir, filename, ttsConfig.OutputFormat)

	err = WriteToFile(filePath, res.AudioContent)
	if err != nil {
		return err
	}

	return nil
}

func PerformBulkTTSAndSaveToFile(ctx context.Context, client *texttospeech.Client, words []string, ttsConfig* TTSConfig) error {
	for idx := range words {
		<-rateLimiter(ttsConfig.ReqPerMin)
		
		word := words[idx]

		err := PerformTTSAndSaveToFile(ctx, client, word, strconv.Itoa(idx + 1), ttsConfig)
		if (err != nil) {
			fmt.Printf("❌ Failed to perform TTS on word %s: %s\n", word, err)
		} else {
			fmt.Printf("✅ Performed TTS on word %s:\n", word)
		}
	}

	return nil
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

	words := []string{"ಕನ್ನಡ", "ಕನ್ನಡ", "ಕನ್ನಡ"}

	PerformBulkTTSAndSaveToFile(ctx, client, words, &ttsConfig)
}
