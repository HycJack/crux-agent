package oauth

import (
	"testing"
)

func TestGitHubConstants(t *testing.T) {
	if githubDeviceCodeURL == "" {
		t.Error("expected non-empty device code URL")
	}
	if githubTokenURL == "" {
		t.Error("expected non-empty token URL")
	}
	if copilotTokenURL == "" {
		t.Error("expected non-empty copilot token URL")
	}
	if githubClientID == "" {
		t.Error("expected non-empty client ID")
	}
}

func TestGitHubCopilotProviderRegistered(t *testing.T) {
	p, err := Get("github-copilot")
	if err != nil {
		t.Fatalf("expected github-copilot provider to be registered: %v", err)
	}
	if p.Name != "GitHub Copilot" {
		t.Errorf("expected name 'GitHub Copilot', got '%s'", p.Name)
	}
	if p.Login == nil {
		t.Error("expected Login function to be set")
	}
	if p.RefreshToken == nil {
		t.Error("expected RefreshToken function to be set")
	}
	if p.GetAPIKey == nil {
		t.Error("expected GetAPIKey function to be set")
	}
}
