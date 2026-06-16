package google

import (
	"context"
	"testing"

	piai "github.com/hycjack/crux-ai"
)

func TestNew(t *testing.T) {
	p := New()
	if p == nil {
		t.Error("expected non-nil provider")
	}
}

func TestProviderImplementsInterface(t *testing.T) {
	var _ = New()
}

func TestStreamNoAPIKey(t *testing.T) {
	p := New()
	model := piai.Model{
		ID:       "gemini-pro",
		Provider: piai.ProviderGoogle,
	}

	_, err := p.Stream(context.Background(), model, piai.Context{}, piai.StreamOptions{})
	if err == nil {
		t.Error("expected error for missing API key")
	}
}
