package types

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
