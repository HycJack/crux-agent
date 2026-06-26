// Package kimi implements the Kimi (Moonshot) provider.
//
// Kimi exposes an OpenAI-compatible coding API at
// https://api.moonshot.ai/v1. This provider delegates to the shared
// compat engine.
//
// Reference: oh-my-pi `packages/ai/src/utils/oauth/moonshot.ts`.
package kimi

import (
	core "crux-ai/core"
	"crux-ai/providers/compat"
)

const defaultBaseURL = "https://api.moonshot.ai/v1"

// New returns a Kimi provider config to be added to the compat Router.
func New() compat.Config {
	return compat.Config{
		Provider:       core.ProviderMoonshotCN,
		DefaultBaseURL: defaultBaseURL,
	}
}
