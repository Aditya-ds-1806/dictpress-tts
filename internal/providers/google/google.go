package google

import (
	"context"
	"fmt"
	"strings"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"google.golang.org/api/option"
)

// TTSConfig represents TTS provider configuration.
type TTSConfig struct {
	Provider     string  `koanf:"provider"`
	APIKey       string  `koanf:"api_key"`
	LanguageCode string  `koanf:"language_code"`
	VoiceName    string  `koanf:"voice_name"`
	OutputFormat string  `koanf:"output_format"`
	OutDir       string  `koanf:"out_dir"`
	ReqPerSec    float64 `koanf:"req_per_sec"`
	SpeechRate   float64 `koanf:"speech_rate"`
	Pitch        float64 `koanf:"pitch"`
	VolumeGainDB float64 `koanf:"volume_gain_db"`
}

// Provider implements Google Cloud TTS.
type Provider struct {
	client *texttospeech.Client
	config TTSConfig
}

// NewProvider creates a new Google TTS provider.
func NewProvider(ctx context.Context, cfg TTSConfig) (*Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("api_key is required for Google TTS")
	}

	client, err := texttospeech.NewClient(ctx, option.WithAPIKey(cfg.APIKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create Google TTS client: %w", err)
	}

	return &Provider{client: client, config: cfg}, nil
}

// PerformTTS converts text to speech using Google TTS.
func (g *Provider) PerformTTS(ctx context.Context, text string) ([]byte, error) {
	// Map output format to Google's AudioEncoding.
	audioEncoding := texttospeechpb.AudioEncoding_MP3
	switch strings.ToLower(g.config.OutputFormat) {
	case "ogg_opus":
		audioEncoding = texttospeechpb.AudioEncoding_OGG_OPUS
	case "linear16":
		audioEncoding = texttospeechpb.AudioEncoding_LINEAR16
	}

	req := &texttospeechpb.SynthesizeSpeechRequest{
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Text{Text: text},
		},
		Voice: &texttospeechpb.VoiceSelectionParams{
			Name:         g.config.VoiceName,
			LanguageCode: g.config.LanguageCode,
		},
		AudioConfig: &texttospeechpb.AudioConfig{
			AudioEncoding: audioEncoding,
			SpeakingRate:  g.config.SpeechRate,
			Pitch:         g.config.Pitch,
			VolumeGainDb:  g.config.VolumeGainDB,
		},
	}

	res, err := g.client.SynthesizeSpeech(ctx, req)
	if err != nil {
		return nil, err
	}

	return res.AudioContent, nil
}

// Close closes the Google TTS client.
func (g *Provider) Close() error {
	if g.client != nil {
		return g.client.Close()
	}
	return nil
}
