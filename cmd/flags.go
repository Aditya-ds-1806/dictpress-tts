package main

import (
	cfg "dictpress-tts/internal/config"
	"dictpress-tts/internal/logger"
	"flag"
	"reflect"
	"strconv"

	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/file"
)

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
	flag.String("tts-format", cfg.DefaultOutputFormat, "Audio output format (e.g., mp3, wav)")
	flag.String("tts-out-dir", cfg.DefaultOutDir, "Directory to save TTS audio files")
	flag.Float64("tts-rate-limit", cfg.DefaultTTSRateLimit, "Max requests per second to the TTS API")
	flag.Float64("tts-speed", cfg.DefaultTTSSpeed, "TTS speech rate multiplier")
	flag.Float64("tts-pitch", cfg.DefaultTTSPitch, "TTS pitch in dB")
	flag.Float64("tts-volume", cfg.DefaultTTSVolumeGainDB, "TTS volume gain in dB")

	// Misc
	flag.Bool("version", false, "Print dictpress-tts version")
	flag.String("file", cfg.DefaultTomlFilePath, "Path to dictpress TOML file")
	flag.Int64("workers", cfg.DefaultWorkersCount, "Number of concurrent TTS processing workers")
}

func ParseFlagConf() *cfg.Config {
	var config = cfg.Config{
		DB: &cfg.DBConfig{},
		TTS: &cfg.TTSConfig{},
		Version: ptr(false),
		File: ptr(cfg.DefaultTomlFilePath),
		Workers: ptr(cfg.DefaultWorkersCount),
	}

	flag.Parse()

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

	return &config
}

func ParseTomlConf(tomlPath string) *cfg.Config {
	var config = cfg.Config{
		File: ptr("./config.toml"),
		TTS: &cfg.TTSConfig{
			OutputFormat: ptr(cfg.DefaultOutputFormat),
			OutDir: ptr(cfg.DefaultOutDir),
			Provider: ptr(cfg.DefaultTTSProvider),
			ReqPerSec: ptr(cfg.DefaultTTSRateLimit),
			SpeechRate: ptr(cfg.DefaultTTSSpeed),
			Pitch: ptr(cfg.DefaultTTSPitch),
			VolumeGainDB: ptr(cfg.DefaultTTSVolumeGainDB),
		},
	}

	k := koanf.New(".")
	err := k.Load(file.Provider(tomlPath), toml.Parser())

	if err != nil {
		logger.Logger.Fatalf("failed to load config.toml: %s", err);
	}

	logger.Logger.Printf("loaded TOML from: %s", tomlPath)

	err = k.Unmarshal("tts", &config.TTS)
	if err != nil {
		logger.Logger.Fatalf("failed to parse [tts] from config.toml: %s", err);
	}

	err = k.Unmarshal("db", &config.DB)
	if err != nil {
		logger.Logger.Fatalf("failed to parse [db] from config.toml: %s", err);
	}

	return &config
}


func mergeStructs(base any, override any) {
	baseVal := reflect.ValueOf(base).Elem()
	overrideVal := reflect.ValueOf(override).Elem()

	for i := range baseVal.NumField() {
		baseField := baseVal.Field(i)
		overrideField := overrideVal.Field(i)

		// Only replace if override field is not nil
		if !overrideField.IsNil() {
			// If the field is a struct pointer, recurse
			if baseField.Kind() == reflect.Ptr && baseField.Elem().Kind() == reflect.Struct {
				if baseField.IsNil() {
					baseField.Set(reflect.New(baseField.Type().Elem()))
				}
				mergeStructs(baseField.Interface(), overrideField.Interface())
			} else {
				baseField.Set(overrideField)
			}
		}
	}
}
