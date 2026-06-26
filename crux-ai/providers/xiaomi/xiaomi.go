// Package xiaomi implements the Xiaomi MiMo provider.
//
// Xiaomi exposes an OpenAI-compatible API at https://api.xiaomimimo.com/v1.
// This provider delegates to the shared compat engine.
package xiaomi

import (
	core "crux-ai/core"
	"crux-ai/providers/compat"
)

const defaultBaseURL = "https://api.xiaomimimo.com/v1"

// New returns a Xiaomi provider to be added to the compat Router.
func New() compat.Config {
	return compat.Config{
		Provider:       core.ProviderXiaomi,
		DefaultBaseURL: defaultBaseURL,
	}
}
