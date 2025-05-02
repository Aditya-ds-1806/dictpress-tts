package providers

import (
	"dictpress-tts/types"
	"fmt"
)

type TTSProvider interface{
	PerformTTS(text string) ([]byte, error)
}

type TTSAdapter struct{
	TTSConfig *types.TTSConfig
}

func (t *TTSAdapter) getProvider() (TTSProvider, error) {
	switch *t.TTSConfig.Provider {
	case "google":
		var provider TTSProvider = &GCloudProvider{Config: t.TTSConfig}
		return provider, nil
	}

	return nil, fmt.Errorf("unknown provider: %s", *t.TTSConfig.Provider)
}

func (t *TTSAdapter) PerformTTS(text string) ([]byte, error) {
	provider, err := t.getProvider()
	if err != nil {
		return nil, err
	}

	return provider.PerformTTS(text)
}
