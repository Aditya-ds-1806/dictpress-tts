package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	flag "github.com/spf13/pflag"
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

	cfg := &Config{
		Workers: ko.MustInt("app.workers"),
	}

	if err := ko.Unmarshal("tts", &cfg.TTS); err != nil {
		lo.Fatalf("failed to unmarshal config: %v", err)
	}

	if err := ko.Unmarshal("db", &cfg.DB); err != nil {
		lo.Fatalf("failed to unmarshal config: %v", err)
	}

	return cfg
}

// connectDB establishes a connection to PostgreSQL.
func connectDB(cfg DBConfig) (*sql.DB, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
		cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to create db client: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return db, nil
}

// createOutputDir creates the output directory if it doesn't exist.
func createOutputDir(dir string) error {
	if dir == "." {
		return nil
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	return nil
}
