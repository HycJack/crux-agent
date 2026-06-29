// Package ollama wires up local Ollama as an OpenAI-protocol provider.
//
// Ollama exposes an OpenAI-compatible /v1/chat/completions endpoint on
// http://localhost:11434 by default, so we delegate to the shared compat
// engine. Users can override the base URL either via the OLLAMA_BASE_URL
// environment variable or by setting Model.BaseURL on the model record.
//
// Authentication is optional: the local server is unauthenticated by
// default, but some deployments (remote Ollama, reverse proxies) still
// pass an API key, so we forward it whenever the user supplies one.
package ollama

import (
	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/providers/compat"
)

const defaultBaseURL = "http://localhost:11434/v1"

// New returns the Ollama config for the compat Router.
//
// Resolution order for the base URL:
//
//  1. model.BaseURL (highest priority, per-model override)
//  2. OLLAMA_BASE_URL environment variable
//  3. http://localhost:11434/v1 (default)
//
// Streaming uses OpenAI's SSE format; Ollama's /v1 endpoint returns the
// same shape and is fully compatible with the compat engine.
func New() compat.Config {
	required := false
	return compat.Config{
		Provider:       core.ProviderOllama,
		DefaultBaseURL: core.GetEnvBaseURL(core.ProviderOllama, defaultBaseURL),
		// Ollama is typically unauthenticated. Users running behind a
		// reverse proxy that requires auth can still pass an API key
		// via the UI — when they do, the compat layer will forward it.
		RequireAPIKey: &required,
	}
}
