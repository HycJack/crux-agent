/*
 * 功能说明：环境变量解析和 API Key 管理
 * 
 * 解决的问题：
 * 1. 需要从环境变量获取各提供者的 API Key
 * 2. 不同提供者使用不同的环境变量名称
 * 3. 部分提供者支持多个环境变量（优先级）
 * 4. 需要解析模型配置中的自定义 BaseURL
 * 
 * 解决方案：
 * 1. 定义 providerEnvVars 映射，存储每个提供者的环境变量列表
 * 2. GetEnvAPIKey 按优先级依次检查环境变量
 * 3. ResolveAPIKey 支持从选项或环境变量获取 API Key
 * 4. ResolveBaseURL 支持模型自定义 BaseURL
 * 
 * 应用场景：
 * - providers/ 层在发起请求前获取 API Key
 * - 支持 OAuth 令牌优先于 API Key
 * - 支持自定义 API 端点
 */
package core

import (
	"os"
	"strings"
)

// GetEnvAPIKey resolves the API key for a provider from environment variables.
// || 从环境变量解析提供者的 API Key
// 参数：
//   provider - 提供者 ID
// 返回：
//   API Key（如果未找到返回空字符串）
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
// || 返回提供者已设置的环境变量列表
// 参数：
//   provider - 提供者 ID
// 返回：
//   已设置的环境变量名称列表
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

// providerEnvVars maps providers to their environment variable names.
// || 提供者到环境变量名称的映射（按优先级排序）
var providerEnvVars = map[KnownProvider][]string{
	ProviderAnthropic:     {"ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}, // OAuth 令牌优先
	ProviderAmazonBedrock: {"AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID"},
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
	ProviderKimi:          {"KIMI_API_KEY", "MOONSHOT_API_KEY"},
	ProviderGLM:           {"GLM_API_KEY", "ZAI_API_KEY"},
	ProviderZAI:           {"ZAI_API_KEY", "GLM_API_KEY"},
}

// ResolveAPIKey resolves an API key from options or environment.
// || 从选项或环境变量解析 API Key
// 参数：
//   provider - 提供者 ID
//   optsKey - 选项中提供的 API Key
// 返回：
//   API Key（优先使用选项中的，否则从环境变量获取）
func ResolveAPIKey(provider KnownProvider, optsKey string) string {
	if optsKey != "" {
		return optsKey
	}
	return GetEnvAPIKey(provider)
}

// ResolveBaseURL resolves the base URL for a provider, with fallback.
// || 解析提供者的基础 URL，支持回退
// 参数：
//   model - 模型信息（可能包含自定义 BaseURL）
//   defaultURL - 默认 URL
// 返回：
//   基础 URL（移除尾部斜杠）
func ResolveBaseURL(model Model, defaultURL string) string {
	if model.BaseURL != "" {
		return strings.TrimRight(model.BaseURL, "/")
	}
	return strings.TrimRight(defaultURL, "/")
}
