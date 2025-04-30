package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
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
    ReqPerSec     float64 `koanf:"req_per_sec"`
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

func ParseTomlConf(tomlPath string) Config {
	k := koanf.New(".")
	err := k.Load(file.Provider(tomlPath), toml.Parser())

	if err != nil {
		logger.Fatalf("failed to load config.toml: %s", err);
	}

	logger.Printf("loaded TOML from: %s", tomlPath)

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

	rateLimiter := rate.NewLimiter(rate.Limit(ttsConfig.ReqPerSec), int(ttsConfig.ReqPerSec))

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

func parseCLIArgs() {
	// toml config
	flag.String("file", "./config.toml", "Path to dictpress TOML file")

	// Database Config
	flag.String("db-host", "", "PostgreSQL host")
	flag.Int("db-port", 0, "PostgreSQL port")
	flag.String("db-name", "", "Name of the PostgreSQL database")
	flag.String("db-user", "", "PostgreSQL username")
	flag.String("db-pass", "", "PostgreSQL password")

	// TTS Config
	flag.String("tts-provider", "google", "TTS provider (e.g., google)")
	flag.String("tts-api-key", "", "API key for TTS provider")
	flag.String("tts-lang", "", "Language code for TTS (e.g., en-US)")
	flag.String("tts-voice", "", "Voice name to use for TTS")
	flag.String("tts-format", "mp3", "Audio output format (e.g., mp3, wav)")
	flag.String("tts-out-dir", "tts", "Directory to save TTS audio files")
	flag.Int64("tts-rate-limit", 1000, "Max TTS requests per second")
	flag.Float64("tts-speed", 1.0, "TTS speech rate multiplier")
	flag.Float64("tts-pitch", 0.0, "TTS pitch in dB")
	flag.Float64("tts-volume", 0.0, "TTS volume gain in dB")

	flag.Parse()
}

func resolveConfig(config *Config) {
	flag.Visit(func(arg *flag.Flag) {
		switch arg.Name {
		case "db-host":
			config.DB.Host = arg.Value.String()

		case "db-port":
			port, err := strconv.Atoi(arg.Value.String())
			if err == nil && port > 0 {
				config.DB.Port = port
			}

		case "db-name":
			config.DB.Database = arg.Value.String()

		case "db-user":
			config.DB.Username = arg.Value.String()

		case "db-pass":
			config.DB.Password = arg.Value.String()

		case "tts-provider":
			config.TTS.Provider = arg.Value.String()

		case "tts-api-key":
			config.TTS.APIKey = arg.Value.String()

		case "tts-lang":
			config.TTS.LanguageCode = arg.Value.String()

		case "tts-voice":
			config.TTS.VoiceName = arg.Value.String()

		case "tts-format":
			config.TTS.OutputFormat = arg.Value.String()

		case "tts-out-dir":
			config.TTS.OutDir = arg.Value.String()

		case "tts-rate-limit":
			limit, err := strconv.ParseFloat(arg.Value.String(), 64)
			if err == nil && limit > 0 {
				config.TTS.ReqPerSec = limit
			}

		case "tts-speed":
			speed, err := strconv.ParseFloat(arg.Value.String(), 64)
			if err == nil && speed > 0 {
				config.TTS.SpeechRate = speed
			}

		case "tts-pitch":
			pitch, err := strconv.ParseFloat(arg.Value.String(), 64)
			if err == nil {
				config.TTS.Pitch = pitch
			}

		case "tts-volume":
			vol, err := strconv.ParseFloat(arg.Value.String(), 64)
			if err == nil {
				config.TTS.VolumeGainDB = vol
			}
		}
	})

	if config.TTS.Provider == "" {
		config.TTS.Provider = flag.Lookup("tts-provider").DefValue
	}

	if config.TTS.OutputFormat == "" {
		config.TTS.OutputFormat = flag.Lookup("tts-format").DefValue
	}

	if config.TTS.OutDir == "" {
		config.TTS.OutDir = flag.Lookup("tts-out-dir").DefValue
	}

	if config.TTS.ReqPerSec == 0 {
		config.TTS.OutDir = flag.Lookup("tts-rate-limit").DefValue
	}

	if config.TTS.SpeechRate == 0 {
		config.TTS.SpeechRate, _ = strconv.ParseFloat(flag.Lookup("tts-speed").DefValue, 64)
	}
}

func initApp() (Config, *sql.DB, *texttospeech.Client) {
	// init logger
	logger = log.New(os.Stdout, "dictpress-tts: ", log.Ltime)

	// parse CLI flags
	parseCLIArgs()

	// parser toml
	config := ParseTomlConf(flag.Lookup("file").Value.String())

	// merge toml and cli config
	resolveConfig(&config)

	// create TTS Client
	ttsClient, err := CreateTTSClient(context.Background(), &config.TTS)
	if err != nil {
		logger.Fatalf("failed to create text-to-speech client: %v\n", err)
	}

	// connect to postgres
	dbURI := fmt.Sprintf("postgres://%s:%d/%s", config.DB.Host, config.DB.Port, config.DB.Database)

	db, err := sql.Open("pgx", dbURI)
	if err != nil {
		logger.Fatalf("failed to create db client: %s", err)
	}

	err = db.Ping()
	if err != nil {
		logger.Fatalf("failed to connect to DB: %s", err)
	}

	logger.Printf("connected to DB: %s\n", dbURI)

	// init out dir
	if config.TTS.OutDir != "." {
		err := os.Mkdir(config.TTS.OutDir, 0777)

		if err != nil {
			if !os.IsExist(err) {
				logger.Fatalf("failed to create out dir: %s", err)
			}

			logger.Printf("out dir: \"%s\" exists, skipping creation", config.TTS.OutDir)
		}
	}

	printConfig(config.TTS)

	return config, db, ttsClient
}

func printConfig(cfg any) {
	v := reflect.ValueOf(cfg)

	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	logger.Println()
	logger.Printf("[%s]", reflect.TypeOf(cfg).Name())

	for i := range v.NumField() {
		field := v.Type().Field(i)
		value := v.Field(i).Interface()
		logger.Printf("%s: %v\n", field.Name, value)
	}

	logger.Println()
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
