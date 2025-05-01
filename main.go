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
	Host      *string `koanf:"host"`
	Port      *int    `koanf:"port"`
	Database  *string `koanf:"db"`
	Username  *string `koanf:"user"`
	Password  *string `koanf:"password"`
}

type TTSConfig struct {
    Provider      *string  `koanf:"provider"`
    APIKey        *string  `koanf:"api_key"`
    LanguageCode  *string  `koanf:"language_code"`
    VoiceName     *string  `koanf:"voice_name"`
    OutputFormat  *string  `koanf:"output_format"`
    OutDir        *string  `koanf:"out_dir"`
    ReqPerSec     *float64 `koanf:"req_per_sec"`
    SpeechRate    *float64 `koanf:"speech_rate"`
    Pitch         *float64 `koanf:"pitch"`
    VolumeGainDB  *float64 `koanf:"volume_gain_db"`
}

type Config struct {
	TTS *TTSConfig
	DB *DBConfig
	Version *bool
	File *string
	Workers *int64
}

type Word struct {
	ID int
	Word string
}

var (
	logger *log.Logger
	chBufferSize = 1000
)

func ptr[T any](v T) *T {
	return &v
}

func CreateTTSClient(ctx context.Context, config* TTSConfig) (*texttospeech.Client, error) {
	if config.APIKey == nil {
		logger.Fatalf("API key is required!")
	}

	return texttospeech.NewClient(ctx, option.WithAPIKey(*config.APIKey))
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
			Name: *ttsConfig.VoiceName,
			LanguageCode: *ttsConfig.LanguageCode,
		},
		AudioConfig: &texttospeechpb.AudioConfig{
			AudioEncoding: texttospeechpb.AudioEncoding_MP3,
			SpeakingRate: *ttsConfig.SpeechRate,
			Pitch: *ttsConfig.Pitch,
			VolumeGainDb: *ttsConfig.VolumeGainDB,
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

	filePath := fmt.Sprintf("%s/%s.%s", *ttsConfig.OutDir, filename, *ttsConfig.OutputFormat)

	err = WriteToFile(filePath, res.AudioContent)
	if err != nil {
		return nil, err
	}

	return &filePath, nil
}

func ParseTomlConf(config *Config) {
	k := koanf.New(".")
	err := k.Load(file.Provider(*config.File), toml.Parser())

	if err != nil {
		logger.Fatalf("failed to load config.toml: %s", err);
	}

	logger.Printf("loaded TOML from: %s", *config.File)

	err = k.Unmarshal("tts", &config.TTS)
	if err != nil {
		logger.Fatalf("failed to parse [tts] from config.toml: %s", err);
	}

	err = k.Unmarshal("db", &config.DB)
	if err != nil {
		logger.Fatalf("failed to parse [db] from config.toml: %s", err);
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

func processWords(ttsClient *texttospeech.Client, config *Config, wordChannel <-chan Word, wg *sync.WaitGroup) {
	defer wg.Done()

	ttsConfig := config.TTS
	reqPerSec := *ttsConfig.ReqPerSec / float64(*config.Workers)
	rateLimiter := rate.NewLimiter(rate.Limit(reqPerSec), int(reqPerSec))

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

func defineFlags() {
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
	flag.Int64("tts-rate-limit", 1000, "Max requests per second to the TTS API")
	flag.Float64("tts-speed", 1.0, "TTS speech rate multiplier")
	flag.Float64("tts-pitch", 0.0, "TTS pitch in dB")
	flag.Float64("tts-volume", 0.0, "TTS volume gain in dB")

	// Misc
	flag.Bool("version", false, "Print dictpress-tts version")
	flag.String("file", "./config.toml", "Path to dictpress TOML file")
	flag.Int("workers", 1, "Number of concurrent TTS processing workers")
}

func ParseRuntimeFlags() Config {
	var config Config

	flag.Visit(func(arg *flag.Flag) {
		switch arg.Name {
			case "file":
				config.File = ptr(arg.Value.String())

			case "version":
				printVersion, _ := strconv.ParseBool(arg.Value.String())
				config.Version = &printVersion
			
			case "workers":
				workers, _ := strconv.ParseInt(arg.Value.String(), 10, 64)
				config.Workers = &workers
		}
	})

	if (config.File == nil) {
		config.File = &flag.Lookup("file").DefValue
	}

	if (config.Workers == nil) {
		workers, _ := strconv.ParseInt(flag.Lookup("workers").DefValue, 10, 64)
		config.Workers = &workers
	}

	if (config.Version == nil) {
		showVersion, _ := strconv.ParseBool(flag.Lookup("version").DefValue)
		config.Version = &showVersion
	}

	return config
}

func resolveConfig(config *Config) {
	flag.Visit(func(arg *flag.Flag) {
		switch arg.Name {
		case "db-host":
			config.DB.Host = ptr(arg.Value.String())

		case "db-port":
			port, _ := strconv.Atoi(arg.Value.String())
			config.DB.Port = &port

		case "db-name":
			config.DB.Database = ptr(arg.Value.String())

		case "db-user":
			config.DB.Username = ptr(arg.Value.String())

		case "db-pass":
			config.DB.Password = ptr(arg.Value.String())

		case "tts-provider":
			config.TTS.Provider = ptr(arg.Value.String())

		case "tts-api-key":
			config.TTS.APIKey = ptr(arg.Value.String())

		case "tts-lang":
			config.TTS.LanguageCode = ptr(arg.Value.String())

		case "tts-voice":
			config.TTS.VoiceName = ptr(arg.Value.String())

		case "tts-format":
			config.TTS.OutputFormat = ptr(arg.Value.String())

		case "tts-out-dir":
			config.TTS.OutDir = ptr(arg.Value.String())

		case "tts-rate-limit":
			limit, _ := strconv.ParseFloat(arg.Value.String(), 64)
			config.TTS.ReqPerSec = &limit

		case "tts-speed":
			speed, _ := strconv.ParseFloat(arg.Value.String(), 64)
			config.TTS.SpeechRate = &speed

		case "tts-pitch":
			pitch, _ := strconv.ParseFloat(arg.Value.String(), 64)
			config.TTS.Pitch = &pitch

		case "tts-volume":
			vol, _ := strconv.ParseFloat(arg.Value.String(), 64)
			config.TTS.VolumeGainDB = &vol
		}
	})

	if config.TTS.Provider == nil {
		config.TTS.Provider = &flag.Lookup("tts-provider").DefValue
	}

	if config.TTS.OutputFormat == nil {
		config.TTS.OutputFormat = &flag.Lookup("tts-format").DefValue
	}

	if config.TTS.OutDir == nil {
		config.TTS.OutDir = &flag.Lookup("tts-out-dir").DefValue
	}

	if config.TTS.ReqPerSec == nil {
		config.TTS.OutDir = &flag.Lookup("tts-rate-limit").DefValue
	}

	if config.TTS.SpeechRate == nil {
		speed, _ := strconv.ParseFloat(flag.Lookup("tts-speed").DefValue, 64)
		config.TTS.SpeechRate = &speed
	}
}

func initApp() (Config, *sql.DB, *texttospeech.Client) {
	flag.Parse()

	var config Config = ParseRuntimeFlags()
	
	if *config.Version {
		fmt.Println("dictpress-tts", Version)
		os.Exit(0)
	}
	
	// parse toml
	ParseTomlConf(&config)

	// merge toml and cli config
	resolveConfig(&config)

	// create TTS Client
	ttsClient, err := CreateTTSClient(context.Background(), config.TTS)
	if err != nil {
		logger.Fatalf("failed to create text-to-speech client: %v\n", err)
	}

	// connect to postgres
	dbURI := fmt.Sprintf("postgres://%s:%d/%s", *config.DB.Host, *config.DB.Port, *config.DB.Database)

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
	if *config.TTS.OutDir != "." {
		err := os.Mkdir(*config.TTS.OutDir, 0777)

		if err != nil {
			if !os.IsExist(err) {
				logger.Fatalf("failed to create out dir: %s", err)
			}

			logger.Printf("out dir: \"%s\" exists, skipping creation", *config.TTS.OutDir)
		}
	}

	printConfig(*config.TTS)

	return config, db, ttsClient
}

func init() {
	logger = log.New(os.Stdout, "dictpress-tts: ", log.Ltime)
	defineFlags()
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
		value := v.Field(i).Elem()
		logger.Printf("%s: %v\n", field.Name, value)
	}

	logger.Println()
}

func main() {
	config, db, ttsClient := initApp()

	defer db.Close()
	defer ttsClient.Close()

	var wg sync.WaitGroup
	wordChannel := make(chan Word, chBufferSize)
	
	wg.Add(1)
	go fetchWordsFromDB(db, wordChannel, &wg)

	for range *config.Workers {
		wg.Add(1)
		go processWords(ttsClient, &config, wordChannel, &wg)
	}

	wg.Wait()
}
