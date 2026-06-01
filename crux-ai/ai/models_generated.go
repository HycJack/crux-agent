// Code generated from model data. DO NOT EDIT.
package ai

import "crux-ai/core"

// GeneratedModels returns the built-in model database.
func GeneratedModels() map[core.KnownProvider]map[string]core.Model {
	return generatedModels
}

var generatedModels = map[core.KnownProvider]map[string]core.Model{
	// ── Anthropic ──
	core.ProviderAnthropic: {
		"claude-sonnet-4-20250514": {
			ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4",
			API: core.APIAnthropicMessages, Provider: core.ProviderAnthropic,
			Reasoning: true, Input: []core.Modality{core.ModalityText, core.ModalityImage},
			Cost: core.Cost{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
			ContextWindow: 200000, MaxTokens: 16000,
		},
		"claude-opus-4-20250514": {
			ID: "claude-opus-4-20250514", Name: "Claude Opus 4",
			API: core.APIAnthropicMessages, Provider: core.ProviderAnthropic,
			Reasoning: true, Input: []core.Modality{core.ModalityText, core.ModalityImage},
			Cost: core.Cost{Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75},
			ContextWindow: 200000, MaxTokens: 32000,
		},
		"claude-3-5-haiku-20241022": {
			ID: "claude-3-5-haiku-20241022", Name: "Claude Haiku 3.5",
			API: core.APIAnthropicMessages, Provider: core.ProviderAnthropic,
			Input: []core.Modality{core.ModalityText, core.ModalityImage},
			Cost: core.Cost{Input: 0.8, Output: 4, CacheRead: 0.08, CacheWrite: 1},
			ContextWindow: 200000, MaxTokens: 8192,
		},
	},
	// ── OpenAI ──
	core.ProviderOpenAI: {
		"gpt-4o": {
			ID: "gpt-4o", Name: "GPT-4o",
			API: core.APIOpenAICompletions, Provider: core.ProviderOpenAI,
			Input: []core.Modality{core.ModalityText, core.ModalityImage},
			Cost: core.Cost{Input: 2.5, Output: 10, CacheRead: 1.25},
			ContextWindow: 128000, MaxTokens: 16384,
		},
		"gpt-4o-mini": {
			ID: "gpt-4o-mini", Name: "GPT-4o Mini",
			API: core.APIOpenAICompletions, Provider: core.ProviderOpenAI,
			Input: []core.Modality{core.ModalityText, core.ModalityImage},
			Cost: core.Cost{Input: 0.15, Output: 0.6, CacheRead: 0.075},
			ContextWindow: 128000, MaxTokens: 16384,
		},
		"o3": {
			ID: "o3", Name: "o3",
			API: core.APIOpenAIResponses, Provider: core.ProviderOpenAI,
			Reasoning: true, Input: []core.Modality{core.ModalityText, core.ModalityImage},
			Cost: core.Cost{Input: 10, Output: 40, CacheRead: 2.5},
			ContextWindow: 200000, MaxTokens: 100000,
		},
		"o3-mini": {
			ID: "o3-mini", Name: "o3-mini",
			API: core.APIOpenAIResponses, Provider: core.ProviderOpenAI,
			Reasoning: true, Input: []core.Modality{core.ModalityText},
			Cost: core.Cost{Input: 1.1, Output: 4.4, CacheRead: 0.55},
			ContextWindow: 200000, MaxTokens: 100000,
		},
		"o4-mini": {
			ID: "o4-mini", Name: "o4-mini",
			API: core.APIOpenAIResponses, Provider: core.ProviderOpenAI,
			Reasoning: true, Input: []core.Modality{core.ModalityText, core.ModalityImage},
			Cost: core.Cost{Input: 1.1, Output: 4.4, CacheRead: 0.275},
			ContextWindow: 200000, MaxTokens: 100000,
		},
	},
	// ── Google ──
	core.ProviderGoogle: {
		"gemini-2.5-pro-preview-05-06": {
			ID: "gemini-2.5-pro-preview-05-06", Name: "Gemini 2.5 Pro",
			API: core.APIGoogleGenerative, Provider: core.ProviderGoogle,
			Reasoning: true, Input: []core.Modality{core.ModalityText, core.ModalityImage},
			Cost: core.Cost{Input: 1.25, Output: 10, CacheRead: 0.315},
			ContextWindow: 1048576, MaxTokens: 65536,
		},
		"gemini-2.5-flash-preview-05-20": {
			ID: "gemini-2.5-flash-preview-05-20", Name: "Gemini 2.5 Flash",
			API: core.APIGoogleGenerative, Provider: core.ProviderGoogle,
			Reasoning: true, Input: []core.Modality{core.ModalityText, core.ModalityImage},
			Cost: core.Cost{Input: 0.15, Output: 0.6, CacheRead: 0.0375},
			ContextWindow: 1048576, MaxTokens: 65536,
		},
		"gemini-2.0-flash": {
			ID: "gemini-2.0-flash", Name: "Gemini 2.0 Flash",
			API: core.APIGoogleGenerative, Provider: core.ProviderGoogle,
			Input: []core.Modality{core.ModalityText, core.ModalityImage},
			Cost: core.Cost{Input: 0.1, Output: 0.4, CacheRead: 0.025},
			ContextWindow: 1048576, MaxTokens: 8192,
		},
	},
	// ── DeepSeek ──
	core.ProviderDeepSeek: {
		"deepseek-chat": {
			ID: "deepseek-chat", Name: "DeepSeek V3",
			API: core.APIOpenAICompletions, Provider: core.ProviderDeepSeek,
			BaseURL: "https://api.deepseek.com/v1",
			Input: []core.Modality{core.ModalityText},
			Cost: core.Cost{Input: 0.27, Output: 1.1, CacheRead: 0.07},
			ContextWindow: 65536, MaxTokens: 8192,
		},
		"deepseek-reasoner": {
			ID: "deepseek-reasoner", Name: "DeepSeek R1",
			API: core.APIOpenAICompletions, Provider: core.ProviderDeepSeek,
			BaseURL: "https://api.deepseek.com/v1",
			Reasoning: true, Input: []core.Modality{core.ModalityText},
			Cost: core.Cost{Input: 0.55, Output: 2.19, CacheRead: 0.14},
			ContextWindow: 65536, MaxTokens: 8192,
		},
	},
	// ── Groq ──
	core.ProviderGroq: {
		"llama-3.3-70b-versatile": {
			ID: "llama-3.3-70b-versatile", Name: "Llama 3.3 70B",
			API: core.APIOpenAICompletions, Provider: core.ProviderGroq,
			Input: []core.Modality{core.ModalityText},
			Cost: core.Cost{Input: 0.59, Output: 0.79},
			ContextWindow: 128000, MaxTokens: 32768,
		},
	},
	// ── xAI ──
	core.ProviderXAI: {
		"grok-3": {
			ID: "grok-3", Name: "Grok 3",
			API: core.APIOpenAICompletions, Provider: core.ProviderXAI,
			BaseURL: "https://api.x.ai/v1",
			Input: []core.Modality{core.ModalityText},
			Cost: core.Cost{Input: 3, Output: 15},
			ContextWindow: 131072, MaxTokens: 16384,
		},
		"grok-3-mini": {
			ID: "grok-3-mini", Name: "Grok 3 Mini",
			API: core.APIOpenAICompletions, Provider: core.ProviderXAI,
			BaseURL: "https://api.x.ai/v1",
			Reasoning: true, Input: []core.Modality{core.ModalityText},
			Cost: core.Cost{Input: 0.3, Output: 0.5},
			ContextWindow: 131072, MaxTokens: 16384,
		},
	},
	// ── Mistral ──
	core.ProviderMistral: {
		"mistral-large-latest": {
			ID: "mistral-large-latest", Name: "Mistral Large",
			API: core.APIMistralConversations, Provider: core.ProviderMistral,
			Reasoning: true, Input: []core.Modality{core.ModalityText, core.ModalityImage},
			Cost: core.Cost{Input: 2, Output: 6},
			ContextWindow: 128000, MaxTokens: 16384,
		},
		"codestral-latest": {
			ID: "codestral-latest", Name: "Codestral",
			API: core.APIMistralConversations, Provider: core.ProviderMistral,
			Input: []core.Modality{core.ModalityText},
			Cost: core.Cost{Input: 0.3, Output: 0.9},
			ContextWindow: 256000, MaxTokens: 16384,
		},
	},
	// ── Cerebras ──
	core.ProviderCerebras: {
		"llama-3.3-70b": {
			ID: "llama-3.3-70b", Name: "Llama 3.3 70B",
			API: core.APIOpenAICompletions, Provider: core.ProviderCerebras,
			BaseURL: "https://api.cerebras.ai/v1",
			Input: []core.Modality{core.ModalityText},
			Cost: core.Cost{Input: 0.6, Output: 0.6},
			ContextWindow: 128000, MaxTokens: 8192,
		},
	},
}

func init() {
	LoadModels(generatedModels)
}
