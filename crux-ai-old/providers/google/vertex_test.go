package google

import (
	"context"
	"testing"

	piai "github.com/hycjack/crux-ai"
)

func TestNewVertex(t *testing.T) {
	p := NewVertex()
	if p == nil {
		t.Error("expected non-nil provider")
	}
}

func TestVertexProviderImplementsInterface(t *testing.T) {
	var _ = NewVertex()
}

func TestVertexStreamNoProject(t *testing.T) {
	p := NewVertex()
	model := piai.Model{
		ID:       "gemini-pro",
		Provider: piai.ProviderGoogleVertex,
	}

	// Should fail without project
	_, err := p.Stream(context.Background(), model, piai.Context{}, piai.StreamOptions{
		APIKey: "test-key",
	})
	if err == nil {
		t.Error("expected error for missing project")
	}
}
