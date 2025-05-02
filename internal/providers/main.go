package providers

import (
	types "dictpress-tts/internal/config"
	"fmt"
)

type TTSProvider interface {
	PerformTTS(text string) ([]byte, error)
	Close() error
}

type TTSAdapter struct {
	TTSConfig *types.TTSConfig
}

func (t *TTSAdapter) getProvider() (TTSProvider, error) {
	switch *t.TTSConfig.Provider {
	case "google":
		if GCloudProvider.Config == nil {
			GCloudProvider.Config = t.TTSConfig
		}

		var provider TTSProvider = GCloudProvider
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

func (t *TTSAdapter) Close() error {
	provider, err := t.getProvider()
	if err != nil {
		return err
	}

	return provider.Close()
}
