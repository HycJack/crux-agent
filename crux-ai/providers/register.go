package providers

import (
	"crux-ai/core"
	"crux-ai/providers/anthropic"
	"crux-ai/providers/bedrock"
	"crux-ai/providers/faux"
	"crux-ai/providers/google"
	"crux-ai/providers/images"
	"crux-ai/providers/mistral"
	"crux-ai/providers/openai"
)

// FauxAPI is the KnownAPI identifier for the faux (testing) provider.
const FauxAPI core.KnownAPI = "faux"

// OpenRouterImagesAPI is the KnownAPI identifier for the OpenRouter images provider.
const OpenRouterImagesAPI core.KnownAPI = "openrouter-images"

// RegisterBuiltInProviders registers all built-in API providers.
func RegisterBuiltInProviders() {
	core.RegisterProvider(core.APIAnthropicMessages, anthropic.New(), "builtin")
	core.RegisterProvider(core.APIOpenAICompletions, openai.NewCompletions(), "builtin")
	core.RegisterProvider(core.APIOpenAIResponses, openai.NewResponses(), "builtin")
	core.RegisterProvider(core.APIAzureOpenAIResponses, openai.NewAzure(), "builtin")
	core.RegisterProvider(core.APIOpenAICodexResponses, openai.NewCodex(), "builtin")
	core.RegisterProvider(core.APIGoogleGenerative, google.New(), "builtin")
	core.RegisterProvider(core.APIGoogleVertex, google.NewVertex(), "builtin")
	core.RegisterProvider(core.APIBedrockConverse, bedrock.New(), "builtin")
	core.RegisterProvider(core.APIMistralConversations, mistral.New(), "builtin")
	// Faux (testing)
	core.RegisterProvider(FauxAPI, faux.New(), "builtin")

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
