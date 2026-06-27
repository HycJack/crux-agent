package credstore

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStore_SetGetDelete(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	if _, err := s.Get(ctx, "openai"); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("expected ErrCredentialNotFound, got %v", err)
	}
	cred := APIKeyCredential{ProviderID: "openai", Key: "sk-test"}
	if err := s.Set(ctx, cred); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(ctx, "openai")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	ak, ok := got.(APIKeyCredential)
	if !ok || ak.Key != "sk-test" {
		t.Errorf("unexpected credential: %#v", got)
	}
	if err := s.Delete(ctx, "openai"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "openai"); !errors.Is(err, ErrCredentialNotFound) {
		t.Errorf("expected ErrCredentialNotFound after delete")
	}
}

func TestOAuthCredential_Valid(t *testing.T) {
	expired := OAuthCredential{AccessToken: "x", ExpiresAt: time.Now().Add(-time.Minute)}
	if expired.Valid() {
		t.Error("expired token should not be valid")
	}
	fresh := OAuthCredential{AccessToken: "x", ExpiresAt: time.Now().Add(time.Minute)}
	if !fresh.Valid() {
		t.Error("fresh token should be valid")
	}
	noExpiry := OAuthCredential{AccessToken: "x"}
	if !noExpiry.Valid() {
		t.Error("zero ExpiresAt should be treated as valid")
	}
}

func TestMemoryStore_OAuthRefresh(t *testing.T) {
	calls := 0
	refresh := func(ctx context.Context, prev string) (string, string, time.Time, error) {
		calls++
		return "new-access", "new-refresh", time.Now().Add(time.Hour), nil
	}
	s := NewMemoryStore()
	ctx := context.Background()
	if err := s.Set(ctx, OAuthCredential{
		ProviderID:   "anthropic",
		AccessToken:  "",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-time.Minute),
		Refresh:      refresh,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "anthropic")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	oauth, ok := got.(OAuthCredential)
	if !ok {
		t.Fatalf("expected OAuthCredential, got %T", got)
	}
	if oauth.AccessToken != "new-access" {
		t.Errorf("expected refreshed token, got %q", oauth.AccessToken)
	}
	if calls != 1 {
		t.Errorf("expected 1 refresh call, got %d", calls)
	}
	// Second Get should not re-refresh (now valid).
	if _, err := s.Get(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected still 1 refresh call, got %d", calls)
	}
}

func TestResolveProviderAuth_Precedence(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.Set(ctx, APIKeyCredential{ProviderID: "openai", Key: "sk-stored"}); err != nil {
		t.Fatal(err)
	}

	// 1. optsAPIKey wins.
	got, ok := ResolveProviderAuth(ctx, "openai", store, "sk-opts", nil)
	if !ok || got != "sk-opts" {
		t.Errorf("opts should win, got %q", got)
	}
	// 2. stored key when opts is empty.
	got, ok = ResolveProviderAuth(ctx, "openai", store, "", nil)
	if !ok || got != "sk-stored" {
		t.Errorf("stored should win when opts empty, got %q", got)
	}
	// 3. envLookup when store has nothing.
	got, ok = ResolveProviderAuth(ctx, "openai", nil, "", func(name string) string {
		if name == "OPENAI_API_KEY" {
			return "sk-env"
		}
		return ""
	})
	if !ok || got != "sk-env" {
		t.Errorf("env should win when store empty, got %q", got)
	}
	// 4. nothing → false.
	got, ok = ResolveProviderAuth(ctx, "openai", nil, "", nil)
	if ok {
		t.Errorf("expected ok=false when nothing set, got %q", got)
	}
}

func TestProviderToEnvName(t *testing.T) {
	cases := map[string]string{
		"openai":        "OPENAI_API_KEY",
		"openai-codex":  "OPENAI_CODEX_API_KEY",
		"google-vertex": "GOOGLE_API_KEY", // canonical for both google + google-vertex
		"deepseek":      "DEEPSEEK_API_KEY",
		"newprovider":   "NEWPROVIDER_API_KEY",
	}
	for in, want := range cases {
		if got := providerToEnvName(in); got != want {
			t.Errorf("providerToEnvName(%q) = %q, want %q", in, got, want)
		}
	}
}
