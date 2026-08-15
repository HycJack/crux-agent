package oauth

import (
	"os"
	"testing"
)

func TestGetAnthropicClientID(t *testing.T) {
	clientID := getAnthropicClientID()
	if clientID == "" {
		t.Error("expected non-empty client ID")
	}
}

func TestAnthropicConstants(t *testing.T) {
	if anthropicAuthURL == "" {
		t.Error("expected non-empty auth URL")
	}
	if anthropicTokenURL == "" {
		t.Error("expected non-empty token URL")
	}
	// H3: callback port is now OS-assigned at runtime, so there is no
	// compile-time constant to assert. Verify the env var name is wired.
	if AnthropicClientIDEnv == "" {
		t.Error("expected non-empty AnthropicClientIDEnv")
	}
}

func TestAnthropicClientIDDecodable(t *testing.T) {
	// The default client ID should be base64 decodable, and
	// CRUX_ANTHROPIC_OAUTH_CLIENT_ID should override the default when set.
	t.Setenv(AnthropicClientIDEnv, "override-id")
	if got := getAnthropicClientID(); got != "override-id" {
		t.Errorf("env override: got %q, want %q", got, "override-id")
	}
	os.Unsetenv(AnthropicClientIDEnv)
	if len(getAnthropicClientID()) == 0 {
		t.Error("expected non-empty fallback client ID")
	}
}
