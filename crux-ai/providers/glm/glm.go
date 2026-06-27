// Package glm implements the Z.AI / GLM (Zhipu) provider.
//
// Z.AI exposes an OpenAI-compatible coding API at
// https://api.z.ai/api/coding/paas/v4. This provider delegates to the shared
// compat engine.
//
// Reference: oh-my-pi packages/ai/src/utils/oauth/zai.ts.
package glm

import (
	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/providers/compat"
)

const defaultBaseURL = "https://api.z.ai/api/coding/paas/v4"

// New returns a GLM provider config to be added to the compat Router.
func New() compat.Config {
	return compat.Config{
		Provider:       core.ProviderGLM,
		DefaultBaseURL: defaultBaseURL,
	}
}