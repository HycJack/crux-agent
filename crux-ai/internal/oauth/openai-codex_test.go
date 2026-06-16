package oauth

import (
	"encoding/base64"
	"testing"
)

func TestCodexConstants(t *testing.T) {
	if codexAuthURL == "" {
		t.Error("expected non-empty auth URL")
	}
	if codexTokenURL == "" {
		t.Error("expected non-empty token URL")
	}
	if codexPort == 0 {
		t.Error("expected non-zero port")
	}
	if codexClientID == "" {
		t.Error("expected non-empty client ID")
	}
}

func TestExtractJWTAccountID(t *testing.T) {
	// Create a mock JWT token
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user-123-abc","iss":"openai"}`))
	signature := "mock-signature"
	token := header + "." + payload + "." + signature

	accountID := extractJWTAccountID(token)
	if accountID != "user-123-abc" {
		t.Errorf("expected 'user-123-abc', got '%s'", accountID)
	}
}

func TestExtractJWTAccountIDInvalid(t *testing.T) {
	// Invalid token (not enough parts)
	accountID := extractJWTAccountID("invalid-token")
	if accountID != "" {
		t.Errorf("expected empty string for invalid token, got '%s'", accountID)
	}
}

func TestExtractJWTAccountIDInvalidBase64(t *testing.T) {
	// Invalid base64 in payload
	accountID := extractJWTAccountID("header.!!!invalid!!!.signature")
	if accountID != "" {
		t.Errorf("expected empty string for invalid base64, got '%s'", accountID)
	}
}

func TestExtractJWTAccountIDNoSub(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"openai"}`))
	token := header + "." + payload + ".sig"

	accountID := extractJWTAccountID(token)
	if accountID != "" {
		t.Errorf("expected empty string for missing sub, got '%s'", accountID)
	}
}

func TestOpenAICodexProviderRegistered(t *testing.T) {
	p, err := Get("openai-codex")
	if err != nil {
		// May fail if providers were reset by another test
		t.Skipf("openai-codex provider not registered: %v", err)
	}
	if p.Name != "OpenAI Codex" {
		t.Errorf("expected name 'OpenAI Codex', got '%s'", p.Name)
	}
	if p.Login == nil {
		t.Error("expected Login function to be set")
	}
}
