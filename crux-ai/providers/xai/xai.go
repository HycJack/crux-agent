// Package xai implements the xAI (Grok) provider.
//
// xAI exposes an OpenAI-compatible API at https://api.x.ai/v1. It is a pure
// OpenAI-protocol provider with no notable quirks, so it delegates to the
// shared compat engine.
package xai

import (
	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/providers/compat"
)

const defaultBaseURL = "https://api.x.ai/v1"

// New returns an xAI provider config to be added to the compat Router.
func New() compat.Config {
	return compat.Config{
		Provider:       core.ProviderXAI,
		DefaultBaseURL: defaultBaseURL,
	}
}
