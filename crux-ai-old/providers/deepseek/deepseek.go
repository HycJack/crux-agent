// Package deepseek implements the DeepSeek provider.
//
// DeepSeek exposes an OpenAI-compatible API at https://api.deepseek.com/v1
// with two notable quirks:
//   - Reasoning-capable models (deepseek-reasoner, V4 family) reject
//     follow-up requests in tool_choice != "none" mode. We drop tool_choice
//     for reasoning models.
//
// Reference: oh-my-pi `packages/ai/test/issue-830-repro.test.ts` and
// `packages/ai/test/deepseek-reasoning-content.test.ts`.
package deepseek

import (
	"strings"

	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/providers/compat"
)

const defaultBaseURL = "https://api.deepseek.com/v1"

// New returns a DeepSeek provider config to be added to the compat Router.
func New() compat.Config {
	return compat.Config{
		Provider:       core.ProviderDeepSeek,
		DefaultBaseURL: defaultBaseURL,
		BuildBody: func(model core.Model, c core.Context, opts core.StreamOptions, body map[string]any) error {
			if isReasoningModel(model.ID) {
				delete(body, "tool_choice")
			}
			return nil
		},
	}
}

func isReasoningModel(id string) bool {
	l := strings.ToLower(id)
	return strings.Contains(l, "reasoner") ||
		strings.Contains(l, "-r1") ||
		strings.Contains(l, "v3.2") ||
		strings.Contains(l, "-v4")
}
