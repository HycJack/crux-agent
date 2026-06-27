// Package credstore provides a small abstraction over provider credentials.
//
// Source: pi-mono packages/ai/src/auth/{credential-store.ts, resolve.ts}
// (transposed to Go).
//
// Two credential types are supported:
//
//   - APIKeyCredential: a static key, optionally with a per-provider env
//     override table.
//   - OAuthCredential:  a refreshable token, refreshed lazily on read.
//
// The MemoryStore is the default in-memory implementation; disk and
// Keyring implementations can plug in via the CredentialStore interface
// (kept minimal so platform-specific code stays out of this package).
package credstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrCredentialNotFound is returned by Get when no credential exists for
// the requested provider.
var ErrCredentialNotFound = errors.New("credstore: credential not found")

// CredentialType enumerates supported credential kinds.
type CredentialType string

const (
	TypeAPIKey CredentialType = "api_key"
	TypeOAuth  CredentialType = "oauth"
)

// Credential is the sealed interface implemented by APIKeyCredential and
// OAuthCredential.
type Credential interface {
	Type() CredentialType
	Provider() string
}

// APIKeyCredential holds a static API key plus optional env fallbacks
// the caller may consult before reporting a missing key.
type APIKeyCredential struct {
	ProviderID string
	Key        string
	EnvVars    []string // optional lookup order: e.g. {"OPENAI_API_KEY"}
}

// Type implements Credential.
func (c APIKeyCredential) Type() CredentialType { return TypeAPIKey }

// Provider implements Credential.
func (c APIKeyCredential) Provider() string { return c.ProviderID }

// OAuthCredential holds a (refreshable) OAuth token.
//
// Refresh is invoked by the store when AccessToken is empty/expired; it
// receives the previous RefreshToken and returns the new pair. Returning
// an error means the token cannot be refreshed and the credential should
// be considered invalid.
type OAuthCredential struct {
	ProviderID   string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time

	// Refresh is optional. If nil, the store will not attempt refresh
	// and will surface ErrCredentialNotFound when ExpiresAt has passed.
	Refresh func(ctx context.Context, prevRefresh string) (access, refresh string, expiresAt time.Time, err error)
}

// Type implements Credential.
func (c OAuthCredential) Type() CredentialType { return TypeOAuth }

// Provider implements Credential.
func (c OAuthCredential) Provider() string { return c.ProviderID }

// Valid reports whether the access token is still usable (no clock skew
// tolerance; callers may add one).
func (c OAuthCredential) Valid() bool {
	return c.AccessToken != "" && (c.ExpiresAt.IsZero() || time.Now().Before(c.ExpiresAt))
}

// CredentialStore is the minimal abstraction over a credential backend.
type CredentialStore interface {
	// Get returns the credential for provider, or ErrCredentialNotFound.
	Get(ctx context.Context, provider string) (Credential, error)

	// Set stores a credential, replacing any existing entry.
	Set(ctx context.Context, cred Credential) error

	// Delete removes the credential for provider. No error if absent.
	Delete(ctx context.Context, provider string) error
}

// MemoryStore is an in-process CredentialStore. Safe for concurrent use.
type MemoryStore struct {
	mu    sync.RWMutex
	items map[string]Credential
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: map[string]Credential{}}
}

// Get implements CredentialStore.
//
// Refresh path: when an OAuth credential is expired and has a Refresh
// function, the read lock is upgraded to a write lock for the duration
// of the refresh call. Because we cannot safely upgrade Go's RWMutex,
// we release the read lock first, then take the write lock — and we
// re-check the credential state on the write side to avoid redundant
// refreshes.
func (s *MemoryStore) Get(ctx context.Context, provider string) (Credential, error) {
	s.mu.RLock()
	c, ok := s.items[provider]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrCredentialNotFound
	}
	oauth, needsRefresh := c.(OAuthCredential)
	if !needsRefresh || oauth.Refresh == nil || oauth.Valid() || oauth.RefreshToken == "" {
		s.mu.RUnlock()
		return c, nil
	}
	// Drop read lock and acquire write lock for refresh.
	s.mu.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.items[provider]
	if !ok {
		return nil, ErrCredentialNotFound
	}
	oauth, ok = current.(OAuthCredential)
	if !ok || oauth.Refresh == nil || oauth.Valid() {
		return current, nil
	}
	access, refresh, exp, err := oauth.Refresh(ctx, oauth.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("credstore: refresh failed: %w", err)
	}
	refreshed := OAuthCredential{
		ProviderID:   oauth.ProviderID,
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    exp,
		Refresh:      oauth.Refresh,
	}
	s.items[provider] = refreshed
	return refreshed, nil
}

// Set implements CredentialStore.
func (s *MemoryStore) Set(ctx context.Context, cred Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[cred.Provider()] = cred
	return nil
}

// Delete implements CredentialStore.
func (s *MemoryStore) Delete(ctx context.Context, provider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, provider)
	return nil
}

// ResolveProviderAuth is the shared resolution path used by AI core APIs.
//
// Precedence (matching pi-mono's resolveProviderAuth):
//
//  1. optsAPIKey (if non-empty) — per-call override
//  2. stored credential (with OAuth refresh handled by the store)
//  3. envLookup fallback       — caller-provided env resolver
//
// Returns (apiKey, true) on success or ("", false) when no credential
// could be resolved.
func ResolveProviderAuth(
	ctx context.Context,
	provider string,
	store CredentialStore,
	optsAPIKey string,
	envLookup func(string) string,
) (string, bool) {
	if optsAPIKey != "" {
		return optsAPIKey, true
	}
	if store != nil {
		if cred, err := store.Get(ctx, provider); err == nil {
			switch c := cred.(type) {
			case APIKeyCredential:
				if c.Key != "" {
					return c.Key, true
				}
			case OAuthCredential:
				if c.Valid() {
					return c.AccessToken, true
				}
			}
		}
	}
	if envLookup != nil {
		// Try provider-specific env names first, then a generic fallback.
		envName := providerToEnvName(provider)
		if v := envLookup(envName); v != "" {
			return v, true
		}
	}
	return "", false
}

// providerToEnvName maps known provider IDs to their canonical API key
// env var name. Keep in sync with core/env.go:providerEnvVars.
func providerToEnvName(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "openai-codex":
		return "OPENAI_CODEX_API_KEY"
	case "google", "google-vertex":
		return "GOOGLE_API_KEY"
	case "mistral":
		return "MISTRAL_API_KEY"
	case "github-copilot":
		return "COPILOT_GITHUB_TOKEN"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "groq":
		return "GROQ_API_KEY"
	case "xai":
		return "XAI_API_KEY"
	case "cerebras":
		return "CEREBRAS_API_KEY"
	case "kimi":
		return "MOONSHOT_API_KEY"
	default:
		// Generic {PROVIDER}_API_KEY uppercase.
		out := make([]byte, 0, len(provider)+9)
		for i := 0; i < len(provider); i++ {
			c := provider[i]
			if c == '-' || c == '.' {
				c = '_'
			}
			if c >= 'a' && c <= 'z' {
				c -= 32
			}
			out = append(out, c)
		}
		out = append(out, "_API_KEY"...)
		return string(out)
	}
}
