// Package xiaomi implements the Xiaomi MiMo provider.
//
// Xiaomi exposes an OpenAI-compatible API. Two endpoints are supported:
//   - Token Plan: https://token-plan-cn.xiaomimimo.com/v1 (default)
//   - Regular API: https://api.xiaomimimo.com/v1
//
// Set XIAOMI_BASE_URL environment variable to override the default endpoint.
// This provider delegates to the shared compat engine.
package xiaomi

import (
	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/providers/compat"
)

const tokenPlanBaseURL = "https://token-plan-cn.xiaomimimo.com/v1"
const regularBaseURL = "https://api.xiaomimimo.com/v1"

// New returns a Xiaomi provider to be added to the compat Router.
// Base URL resolution order:
//  1. model.BaseURL (highest priority, checked at request time)
//  2. XIAOMI_BASE_URL environment variable
//  3. Token Plan endpoint (default)
func New() compat.Config {
	return compat.Config{
		Provider:       core.ProviderXiaomi,
		DefaultBaseURL: core.GetEnvBaseURL(core.ProviderXiaomi, tokenPlanBaseURL),
	}
}
