package llm

import (
	"context"
	"testing"

	"github.com/hycjack/crux-ai/core"
)

func TestStreamNoProvider(t *testing.T) {
	core.ClearProviders()
	defer func() {
		core.ClearProviders()
	}()

	model := core.Model{
		ID:      "test",
		API:     core.APIAnthropicMessages,
		Provider: core.ProviderAnthropic,
	}

	_, err := Stream(context.Background(), model, []core.Message{
		core.UserMessage{Role: "user", Content: "hello"},
	})
	if err == nil {
		t.Error("expected error for unregistered provider")
	}
}

func TestCompleteNoProvider(t *testing.T) {
	core.ClearProviders()
	defer func() {
		core.ClearProviders()
	}()

	model := core.Model{
		ID:      "test",
		API:     core.APIAnthropicMessages,
		Provider: core.ProviderAnthropic,
	}

	_, err := Complete(context.Background(), model, []core.Message{
		core.UserMessage{Role: "user", Content: "hello"},
	})
	if err == nil {
		t.Error("expected error for unregistered provider")
	}
}

func TestStreamSimpleNoProvider(t *testing.T) {
	core.ClearProviders()
	defer func() {
		core.ClearProviders()
	}()

	model := core.Model{
		ID:      "test",
		API:     core.APIAnthropicMessages,
		Provider: core.ProviderAnthropic,
	}

	_, err := StreamSimple(context.Background(), model, []core.Message{
		core.UserMessage{Role: "user", Content: "hello"},
	})
	if err == nil {
		t.Error("expected error for unregistered provider")
	}
}

func TestCompleteSimpleNoProvider(t *testing.T) {
	core.ClearProviders()
	defer func() {
		core.ClearProviders()
	}()

	model := core.Model{
		ID:      "test",
		API:     core.APIAnthropicMessages,
		Provider: core.ProviderAnthropic,
	}

	_, err := CompleteSimple(context.Background(), model, []core.Message{
		core.UserMessage{Role: "user", Content: "hello"},
	})
	if err == nil {
		t.Error("expected error for unregistered provider")
	}
}
