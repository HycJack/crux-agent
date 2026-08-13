package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/hycjack/crux-ai/ai"
	core "github.com/hycjack/crux-ai/core"
)

// TestCompatProvidersRegistered verifies that every OpenAI-protocol provider
// that has models in the model database is actually registered with the
// compat Router. Before the fix, Groq / xAI / Cerebras were missing and any
// request to them failed with "no config registered for provider".
func TestCompatProvidersRegistered(t *testing.T) {
	// Providers that should be reachable through the OpenAI-completions compat router.
	wantProviders := []core.KnownProvider{
		core.ProviderOpenAI,
		core.ProviderXiaomi,
		core.ProviderGLM,
		core.ProviderDeepSeek,
		core.ProviderMoonshotCN, // registered by providers/kimi (Kimi/Moonshot CN)
		core.ProviderOllama,
		core.ProviderGroq,
		core.ProviderXAI,
		core.ProviderCerebras,
	}

	provider, err := core.GetProvider(core.APIOpenAICompletions)
	if err != nil {
		t.Fatalf("GetProvider(APIOpenAICompletions): %v", err)
	}

	for _, p := range wantProviders {
		model := core.Model{
			ID:       "test-model",
			API:      core.APIOpenAICompletions,
			Provider: p,
		}
		_, err := provider.Stream(context.Background(), model, core.Context{}, core.StreamOptions{})
		// Stream is asynchronous: it may return nil error while the actual
		// request proceeds in a goroutine. What must never happen is a
		// "no config registered" error, which means the provider is not wired
		// into the router at all.
		if err != nil && strings.Contains(err.Error(), "no config registered") {
			t.Errorf("provider %q is NOT registered with the compat router: %v", p, err)
			continue
		}
		t.Logf("provider %q dispatched correctly (err=%v)", p, err)
	}
}

// TestProviderModelsRegisteredInDB ensures the database exposes models for the
// newly wired providers, so they are actually selectable end-to-end.
func TestProviderModelsRegisteredInDB(t *testing.T) {
	for _, p := range []core.KnownProvider{
		core.ProviderGroq,
		core.ProviderXAI,
		core.ProviderCerebras,
	} {
		models := ai.GetModels(p)
		if len(models) == 0 {
			t.Errorf("provider %q has no models registered in the database", p)
			continue
		}
		t.Logf("provider %q has %d models", p, len(models))
	}
}
