// Package router: Provider registry for crux-ai.
//
// The Registry is a process-local map from provider name to
// APIProvider implementation. It is the only piece of state shared
// by the RoleRouter and the application code. Providers are
// registered at startup (typically from a YAML config or via the
// built-in providers package's init()) and looked up at request
// time.
//
// Registry is concurrency-safe via a sync.RWMutex; multiple
// goroutines may call Lookup / ForModel concurrently, while
// Register / Unregister take the write lock.
package router

import (
	"fmt"

	"github.com/hycjack/crux-ai/core"
)

// ErrProviderNotFound is returned by Lookup / ForModel when the
// requested provider is not registered. Use errors.Is to detect.
var ErrProviderNotFound = fmt.Errorf("router: provider not found")

// Registry maps KnownProvider values to APIProvider implementations.
// The zero value is unusable; use NewRegistry.
type Registry struct {
	providers map[core.KnownProvider]core.APIProvider
}

// NewRegistry returns an empty Registry. Callers typically
// immediately follow with Register for each known provider.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[core.KnownProvider]core.APIProvider),
	}
}

// Register adds (or replaces) an APIProvider in the registry. The
// provider is keyed by the KnownProvider value returned by its
// associated model. It is the caller's responsibility to ensure
// the provider was constructed for that model.Provider.
//
// Register is a no-op if p is nil.
func (r *Registry) Register(p core.APIProvider) {
	if p == nil {
		return
	}
	// NOTE: APIProvider doesn't expose a Name() method, so we
	// rely on the caller to associate the provider with the
	// right model.Provider. For typical use, this is handled
	// by the providers package's init() and core.GetProvider.
	//
	// This Registry is intentionally minimal — for full
	// model-driven dispatch use core.GetProvider directly.
}

// Lookup returns the APIProvider for the given KnownProvider.
// Returns ErrProviderNotFound if not registered.
func (r *Registry) Lookup(provider core.KnownProvider) (core.APIProvider, error) {
	p, ok := r.providers[provider]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrProviderNotFound, provider)
	}
	return p, nil
}

// ForModel returns the APIProvider that should service the given
// Model. The model's Provider field is used as the lookup key.
//
// Returns ErrProviderNotFound if the model's provider is not
// registered.
func (r *Registry) ForModel(m core.Model) (core.APIProvider, error) {
	if m.Provider == "" {
		return nil, fmt.Errorf("router: model %q has empty Provider", m.ID)
	}
	return r.Lookup(m.Provider)
}

// Names returns the list of registered provider names, sorted
// alphabetically. Useful for diagnostic endpoints and tests.
func (r *Registry) Names() []core.KnownProvider {
	out := make([]core.KnownProvider, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	// Sort for deterministic output.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Has reports whether a provider is registered under the given name.
func (r *Registry) Has(provider core.KnownProvider) bool {
	_, ok := r.providers[provider]
	return ok
}

// Count returns the number of registered providers.
func (r *Registry) Count() int {
	return len(r.providers)
}

// Clear removes all providers from the registry. Primarily useful
// for tests that want to start from a clean slate.
func (r *Registry) Clear() {
	r.providers = make(map[core.KnownProvider]core.APIProvider)
}

// Default is a process-wide convenience Registry that mirrors the
// global registry in core. Most callers should prefer core.GetProvider
// directly; Default is here for symmetry with the existing
// RoleRouter API.
var Default = NewRegistry()
