package core

import (
	"os"
	"strings"
)

// GetEnvAPIKey resolves the API key for a provider from environment variables.
func GetEnvAPIKey(provider KnownProvider) string {
	envVars := providerEnvVars[provider]
	for _, envVar := range envVars {
		if val := os.Getenv(envVar); val != "" {
			return val
		}
	}
	return ""
}

// FindEnvKeys returns which environment variables are set for a provider.
func FindEnvKeys(provider KnownProvider) []string {
	envVars := providerEnvVars[provider]
	var found []string
	for _, envVar := range envVars {
		if os.Getenv(envVar) != "" {
			found = append(found, envVar)
		}
	}
	return found
}

var providerEnvVars = map[KnownProvider][]string{
	ProviderAnthropic:     {"ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"},
	ProviderOpenAI:        {"OPENAI_API_KEY"},
	ProviderGoogle:        {"GOOGLE_API_KEY", "GEMINI_API_KEY"},
	ProviderGoogleVertex:  {"GOOGLE_CLOUD_PROJECT"},
	ProviderMistral:       {"MISTRAL_API_KEY"},
	ProviderAzureOpenAI:   {"AZURE_OPENAI_API_KEY"},
	ProviderOpenAICodex:   {"OPENAI_CODEX_API_KEY"},
	ProviderGitHubCopilot: {"COPILOT_GITHUB_TOKEN"},
	ProviderOpenRouter:    {"OPENROUTER_API_KEY"},
	ProviderFireworks:     {"FIREWORKS_API_KEY"},
	ProviderTogether:      {"TOGETHER_API_KEY"},
	ProviderGroq:          {"GROQ_API_KEY"},
	ProviderXAI:           {"XAI_API_KEY"},
	ProviderDeepSeek:      {"DEEPSEEK_API_KEY"},
	ProviderCerebras:      {"CEREBRAS_API_KEY"},
	ProviderCloudflare:    {"CLOUDFLARE_API_KEY", "CLOUDFLARE_AI_TOKEN"},
	ProviderHuggingFace:   {"HUGGINGFACE_API_KEY", "HF_API_TOKEN"},
	ProviderMoonshot:      {"MOONSHOT_API_KEY"},
	ProviderMoonshotCN:    {"MOONSHOT_API_KEY"},
	ProviderMinimax:       {"MINIMAX_API_KEY"},
	ProviderMinimaxCN:     {"MINIMAX_API_KEY"},
	ProviderXiaomi:        {"XIAOMI_API_KEY", "MI_API_KEY"},
}

// ResolveAPIKey resolves an API key from options or environment.
func ResolveAPIKey(provider KnownProvider, optsKey string) string {
	if optsKey != "" {
		return optsKey
	}
	return GetEnvAPIKey(provider)
}

// ResolveBaseURL resolves the base URL for a provider, with fallback.
func ResolveBaseURL(model Model, defaultURL string) string {
	if model.BaseURL != "" {
		return strings.TrimRight(model.BaseURL, "/")
	}
	return strings.TrimRight(defaultURL, "/")
}
