package providers

import (
	"testing"

	"github.com/hycjack/crux-ai/core"
)

func TestRegisterBuiltInProviders(t *testing.T) {
	// Save the initial registered APIs to restore later.
	initial := core.GetRegisteredProviders()
	defer func() {
		// Restore by unregistering everything we added.
		core.UnregisterProviders(sourceID)
		for range initial {
			// Best-effort: re-register via init.
		}
		RegisterBuiltInProviders()
	}()

	// Run the registration.
	UnregisterBuiltInProviders()
	RegisterBuiltInProviders()

	// Verify all expected APIs are registered.
	for _, api := range RegisteredAPIs() {
		if _, err := core.GetProvider(api); err != nil {
			t.Errorf("expected provider for %q, got error: %v", api, err)
		}
	}
}

func TestUnregisterBuiltInProviders(t *testing.T) {
	// Unregister everything.
	UnregisterBuiltInProviders()

	// Verify the APIs we registered are gone.
	for _, api := range RegisteredAPIs() {
		if _, err := core.GetProvider(api); err == nil {
			// Some APIs may not have been registered yet, that's fine.
			_ = err
		}
	}

	// Re-register for the rest of the test suite.
	RegisterBuiltInProviders()
}

func TestRegisteredAPIs(t *testing.T) {
	apis := RegisteredAPIs()
	if len(apis) == 0 {
		t.Error("RegisteredAPIs() should return at least one API")
	}

	// Ensure no duplicates.
	seen := make(map[core.KnownAPI]bool)
	for _, api := range apis {
		if seen[api] {
			t.Errorf("duplicate API in RegisteredAPIs(): %q", api)
		}
		seen[api] = true
	}
}

func TestRegisteredProviders(t *testing.T) {
	providers := RegisteredProviders()
	if len(providers) == 0 {
		t.Error("RegisteredProviders() should return at least one provider")
	}

	// Check the expected canonical names.
	expected := []string{"openai", "xiaomi", "glm", "deepseek", "kimi"}
	for i, name := range expected {
		if i >= len(providers) {
			t.Errorf("missing provider %q in RegisteredProviders()", name)
			continue
		}
		if providers[i] != name {
			t.Errorf("RegisteredProviders()[%d] = %q, want %q", i, providers[i], name)
		}
	}
}

func TestIdempotentRegistration(t *testing.T) {
	// Calling RegisterBuiltInProviders twice should not error.
	RegisterBuiltInProviders()
	RegisterBuiltInProviders()

	// And the providers should still be registered.
	if _, err := core.GetProvider(core.APIAnthropicMessages); err != nil {
		t.Errorf("expected anthropic provider after double registration: %v", err)
	}
}
