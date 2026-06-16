// Package providers provides a single import point for all built-in
// LLM providers. Importing this package triggers init() which
// registers every provider against the core API registry, so that
// downstream code can simply call llm.Complete / llm.Stream without
// any explicit provider wiring.
//
// Usage:
//
//	import (
//		_ "github.com/hycjack/crux-ai/providers"  // registers everything
//	)
//
// Or, if you want only a subset of providers (smaller binary, faster
// startup), import the individual sub-packages and call
// RegisterBuiltInProviders yourself:
//
//	import (
//		"github.com/hycjack/crux-ai/providers"
//		_ "github.com/hycjack/crux-ai/providers/openai"
//		_ "github.com/hycjack/crux-ai/providers/anthropic"
//	)
//
//	go func() { providers.RegisterBuiltInProviders() }()
package providers

import (
	"github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/providers/anthropic"
	"github.com/hycjack/crux-ai/providers/bedrock"
	"github.com/hycjack/crux-ai/providers/compat"
	"github.com/hycjack/crux-ai/providers/deepseek"
	"github.com/hycjack/crux-ai/providers/glm"
	"github.com/hycjack/crux-ai/providers/google"
	"github.com/hycjack/crux-ai/providers/kimi"
	"github.com/hycjack/crux-ai/providers/mistral"
	"github.com/hycjack/crux-ai/providers/openai"
	"github.com/hycjack/crux-ai/providers/openrouter"
	"github.com/hycjack/crux-ai/providers/xiaomi"
)

// sourceID is the registration source for all built-in providers.
// Pass this to core.UnregisterProviders(sourceID) to remove them.
const sourceID = "builtin"

// RegisterBuiltInProviders registers every built-in API provider
// against the core registry. Calling this more than once is safe —
// the second call replaces the first set with a fresh one.
//
// The providers are grouped into three categories:
//
//  1. Native providers (anthropic, google, mistral, bedrock) implement
//     their own streaming + convert logic.
//  2. OpenAI-native providers (openai-responses / azure / codex)
//     share the openai package's client but use different request
//     shapes.
//  3. OpenAI-compatible providers (openai-direct, xiaomi, glm,
//     deepseek, kimi) share a single compat.Router that dispatches
//     by model.Provider.
func RegisterBuiltInProviders() {
	// --- Category 1: native providers ---------------------------
	core.RegisterProvider(core.APIAnthropicMessages, anthropic.New(), sourceID)
	core.RegisterProvider(core.APIGoogleGenerative, google.New(), sourceID)
	core.RegisterProvider(core.APIGoogleVertex, google.NewVertex(), sourceID)
	core.RegisterProvider(core.APIMistralConversations, mistral.New(), sourceID)
	core.RegisterProvider(core.APIBedrockConverse, bedrock.New(), sourceID)

	// --- Category 2: OpenAI-native (multiple APIs) -------------
	core.RegisterProvider(core.APIOpenAIResponses, openai.NewResponses(), sourceID)
	core.RegisterProvider(core.APIAzureOpenAIResponses, openai.NewAzure(), sourceID)
	core.RegisterProvider(core.APIOpenAICodexResponses, openai.NewCodex(), sourceID)

	// --- Category 3: OpenAI-compat router -----------------------
	// A single core.RegisterProvider call covers all OpenAI-protocol
	// providers. At request time the router dispatches by
	// model.Provider.
	openaiCompat := compat.NewRouter().
		WithConfig(openai.NewCompat()). // openai-direct (the canonical one)
		WithConfig(xiaomi.New()).
		WithConfig(glm.New()).
		WithConfig(deepseek.New()).
		WithConfig(kimi.New())
	core.RegisterProvider(core.APIOpenAICompletions, openaiCompat, sourceID)

	// --- Image providers ----------------------------------------
	core.RegisterImagesProvider("openrouter-images", openrouter.NewOpenRouter(), sourceID)
}

// UnregisterBuiltInProviders removes every provider registered by
// RegisterBuiltInProviders. Useful for tests that want to start
// from a clean registry, or for hot-reload scenarios.
func UnregisterBuiltInProviders() {
	core.UnregisterProviders(sourceID)
	core.UnregisterImagesProviders(sourceID)
}

// RegisteredAPIs returns the list of APIs that RegisterBuiltInProviders
// registers. Useful for diagnostics and documentation generation.
func RegisteredAPIs() []core.KnownAPI {
	return []core.KnownAPI{
		// Native
		core.APIAnthropicMessages,
		core.APIGoogleGenerative,
		core.APIGoogleVertex,
		core.APIMistralConversations,
		core.APIBedrockConverse,
		// OpenAI-native
		core.APIOpenAIResponses,
		core.APIAzureOpenAIResponses,
		core.APIOpenAICodexResponses,
		// OpenAI-compat (shared router)
		core.APIOpenAICompletions,
	}
}

// RegisteredProviders returns the per-provider name list backing the
// OpenAI-compat router. Useful for diagnostic output.
func RegisteredProviders() []string {
	return []string{
		"openai",
		"xiaomi",
		"glm",
		"deepseek",
		"kimi",
	}
}

func init() {
	RegisterBuiltInProviders()
}
