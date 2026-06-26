package openrouter

import (
	"testing"

	piai "github.com/hycjack/crux-ai"
)

func TestNewOpenRouter(t *testing.T) {
	p := NewOpenRouter()
	if p == nil {
		t.Error("expected non-nil provider")
	}
}

func TestOpenRouterProviderImplementsInterface(t *testing.T) {
	var _ piai.ImagesAPIProvider = &OpenRouterProvider{}
}

func TestGenerateImagesNoAPIKey(t *testing.T) {
	p := NewOpenRouter()
	model := piai.ImagesModel{
		ID:       "flux-pro",
		Provider: piai.ProviderOpenRouter,
	}

	_, err := p.GenerateImages(model, piai.Context{}, piai.ImageOptions{})
	if err == nil {
		t.Error("expected error for missing API key")
	}
}
