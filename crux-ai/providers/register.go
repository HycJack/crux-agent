package providers

import (
	"github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/providers/anthropic"
	"github.com/hycjack/crux-ai/providers/bedrock"
	"github.com/hycjack/crux-ai/providers/compat"
	"github.com/hycjack/crux-ai/providers/deepseek"
	"github.com/hycjack/crux-ai/providers/faux"
	"github.com/hycjack/crux-ai/providers/glm"
	"github.com/hycjack/crux-ai/providers/google"
	"github.com/hycjack/crux-ai/providers/images"
	"github.com/hycjack/crux-ai/providers/kimi"
	"github.com/hycjack/crux-ai/providers/mistral"
	"github.com/hycjack/crux-ai/providers/ollama"
	"github.com/hycjack/crux-ai/providers/openai"
	"github.com/hycjack/crux-ai/providers/xiaomi"
)

// FauxAPI is the KnownAPI identifier for the faux (testing) provider.
const FauxAPI core.KnownAPI = "faux"

// OpenRouterImagesAPI is the KnownAPI identifier for the OpenRouter images provider.
const OpenRouterImagesAPI core.KnownAPI = "openrouter-images"

// RegisterBuiltInProviders registers all built-in API providers.
//
// OpenAI-protocol providers (OpenAI, Xiaomi, GLM, DeepSeek, Kimi) are
// registered together under APIOpenAICompletions via a single compat.Router
// that dispatches by model.Provider at request time. Native providers
// (Anthropic, Google, Mistral, Bedrock) keep their dedicated APIs.
func RegisterBuiltInProviders() {
	// --- Native providers ---
	core.RegisterProvider(core.APIAnthropicMessages, anthropic.New(), "builtin")
	core.RegisterProvider(core.APIGoogleGenerative, google.New(), "builtin")
	core.RegisterProvider(core.APIGoogleVertex, google.NewVertex(), "builtin")
	core.RegisterProvider(core.APIMistralConversations, mistral.New(), "builtin")
	core.RegisterProvider(core.APIBedrockConverse, bedrock.New(), "builtin")

	// --- OpenAI-native providers (separate APIs) ---
	core.RegisterProvider(core.APIOpenAIResponses, openai.NewResponses(), "builtin")
	core.RegisterProvider(core.APIAzureOpenAIResponses, openai.NewAzure(), "builtin")
	core.RegisterProvider(core.APIOpenAICodexResponses, openai.NewCodex(), "builtin")

	// --- OpenAI-protocol compat router ---
	// A single compat.Router covers every OpenAI-protocol provider. The
	// router looks up model.Provider to pick the right base URL, headers,
	// and body quirks.
	openaiCompat := compat.NewRouter().
		WithConfig(openai.NewCompat()). // OpenAI direct
		WithConfig(xiaomi.New()).
		WithConfig(glm.New()).
		WithConfig(deepseek.New()).
		WithConfig(kimi.New()).
		WithConfig(ollama.New())
	core.RegisterProvider(core.APIOpenAICompletions, openaiCompat, "builtin")

	// --- Faux (testing) ---
	core.RegisterProvider(FauxAPI, faux.New(), "builtin")

	// --- Image providers ---
	core.RegisterImagesProvider(OpenRouterImagesAPI, images.NewOpenRouter(), "builtin")
}

// UnregisterBuiltInProviders removes all built-in providers.
func UnregisterBuiltInProviders() {
	core.UnregisterProviders("builtin")
	core.UnregisterImagesProviders("builtin")
}

func init() {
	RegisterBuiltInProviders()
}
