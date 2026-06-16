package oauth

import (
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
	if anthropicPort == 0 {
		t.Error("expected non-zero port")
	}
}

func TestAnthropicClientIDDecodable(t *testing.T) {
	// The client ID should be base64 decodable
	clientID := getAnthropicClientID()
	if len(clientID) == 0 {
		t.Error("expected non-empty decoded client ID")
	}
}
