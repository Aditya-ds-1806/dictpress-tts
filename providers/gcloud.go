package providers

import (
	"context"
	"fmt"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"google.golang.org/api/option"

	"dictpress-tts/types"
)

type GCloudProvider struct{
	Config *types.TTSConfig
	client *texttospeech.Client
}

func (g *GCloudProvider) createTTSClient() (*texttospeech.Client, error) {
	if g.Config.APIKey == nil {
		return nil, fmt.Errorf("api key is required")
	}

	return texttospeech.NewClient(context.Background(), option.WithAPIKey(*g.Config.APIKey))
}

func (g *GCloudProvider) PerformTTS(text string) ([]byte, error) {
	if g.client == nil {
		client, err := g.createTTSClient()
		if err != nil {
			return nil, err
		}

		g.client = client
	}

	req := texttospeechpb.SynthesizeSpeechRequest{
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Text{Text: text},
		},
		Voice: &texttospeechpb.VoiceSelectionParams{
			Name: *g.Config.VoiceName,
			LanguageCode: *g.Config.LanguageCode,
		},
		AudioConfig: &texttospeechpb.AudioConfig{
			AudioEncoding: texttospeechpb.AudioEncoding_MP3,
			SpeakingRate: *g.Config.SpeechRate,
			Pitch: *g.Config.Pitch,
			VolumeGainDb: *g.Config.VolumeGainDB,
		},
	}

	res, err := g.client.SynthesizeSpeech(context.Background(), &req)

	if err == nil {
		return res.AudioContent, nil
	}

	return nil, err
}
