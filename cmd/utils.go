package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	flag "github.com/spf13/pflag"

	"dictpress-tts/internal/providers/google"
)

// initFlags initializes the commandline flags into the Koanf instance.
func initFlags(ko *koanf.Koanf) {
	f := flag.NewFlagSet("config", flag.ContinueOnError)
	f.Usage = func() {
		// Register --help handler.
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}

	// Register the commandline flags.
	f.StringSlice("config", []string{"config.toml"}, "path to one or more config files (will be merged in order)")
	f.Bool("version", false, "show current version of the build")
	f.String("tts-provider", "", "TTS provider to use (overrides config file)")
	if err := f.Parse(os.Args[1:]); err != nil {
		lo.Fatalf("error loading flags: %v", err)
	}

	if err := ko.Load(posflag.Provider(f, ".", ko), nil); err != nil {
		lo.Fatalf("error loading config: %v", err)
	}
}

// initConfig loads configuration from TOML file.
func initConfig(ko *koanf.Koanf) *Config {
	// Load config files in order.
	for _, f := range ko.MustStrings("config") {
		lo.Printf("reading config: %s", f)
		if err := ko.Load(file.Provider(f), toml.Parser()); err != nil {
			log.Fatalf("error reading config: %v", err)
		}
	}

	// Override app.tts_provider if the --tts-provider flag is set.
	if f := ko.String("tts-provider"); f != "" {
		ko.Set("app.tts_provider", f)
	}

	cfg := &Config{
		Workers:         ko.MustInt("app.workers"),
		TTSProviderName: ko.MustString("app.tts_provider"),
		FetchBatchSize:  ko.MustInt("app.fetch_batch_size"),
	}

	// Unmarshal TTS config from provider-specific section.
	key := fmt.Sprintf("tts.%s", cfg.TTSProviderName)
	if err := ko.Unmarshal(key, &cfg.TTS); err != nil {
		lo.Fatalf("failed to unmarshal config from %s: %v", key, err)
	}

	return cfg
}

// connectDB establishes a connection to PostgreSQL.
func connectDB(ko *koanf.Koanf) (*sql.DB, error) {
	// Build DSN.
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		ko.String("db.user"), ko.String("db.password"), ko.MustString("db.host"),
		ko.MustInt("db.port"), ko.MustString("db.db"), ko.MustString("db.ssl_mode"))

	// Append optional params as-is if present.
	if params := ko.String("db.params"); params != "" {
		dsn = fmt.Sprintf("%s&%s", dsn, params)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to create db client: %w", err)
	}

	// Set connection pool settings.
	db.SetMaxOpenConns(ko.MustInt("db.max_open"))
	db.SetMaxIdleConns(ko.MustInt("db.max_idle"))

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return db, nil
}

// createOutputDir creates the output directory if it doesn't exist.
func createOutputDir(dir string) error {
	if dir == "." || dir == "" {
		return nil
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	return nil
}

// initProvider initializes the TTS provider based on the provider name.
func initProvider(ctx context.Context, providerName string, ttsCfg TTSConfig) (TTSProvider, error) {
	switch providerName {
	case "google":
		googleCfg := google.TTSConfig{
			Provider:     providerName,
			APIKey:       ttsCfg.APIKey,
			LanguageCode: ttsCfg.LanguageCode,
			VoiceName:    ttsCfg.VoiceName,
			OutputFormat: ttsCfg.OutputFormat,
			OutDir:       ttsCfg.OutDir,
			ReqPerSec:    ttsCfg.ReqPerSec,
			SpeechRate:   ttsCfg.SpeechRate,
			Pitch:        ttsCfg.Pitch,
			VolumeGainDB: ttsCfg.VolumeGainDB,
		}
		return google.NewProvider(ctx, googleCfg)
	default:
		return nil, fmt.Errorf("unsupported TTS provider: %s", providerName)
	}
}
