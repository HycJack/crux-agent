package core

import (
	"testing"
)

// TestGetEnvAPIKeyMultipleVars 测试多个环境变量按顺序查找
func TestGetEnvAPIKeyMultipleVars(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-token-123")

	got := GetEnvAPIKey(ProviderAnthropic)
	if got != "oauth-token-123" {
		t.Errorf("Expected 'oauth-token-123', got: %s", got)
	}
}

// TestGetEnvAPIKeyPrimaryVar 测试第一个找到的环境变量返回
// (注意：Anthropic 的环境变量顺序是 OAuth 在前，所以 OAuth 会被优先返回)
func TestGetEnvAPIKeyPrimaryVar(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "primary-key")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-token")

	got := GetEnvAPIKey(ProviderAnthropic)
	// OAuth 变量在 providerEnvVars 中排在 API_KEY 之前
	if got != "oauth-token" {
		t.Errorf("Expected 'oauth-token' (first in env var list), got: %s", got)
	}
}

// TestGetEnvAPIKeyGoogle 测试 Google Provider 多变量
func TestGetEnvAPIKeyGoogle(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "gemini-key")

	got := GetEnvAPIKey(ProviderGoogle)
	if got != "gemini-key" {
		t.Errorf("Expected 'gemini-key', got: %s", got)
	}
}

// TestGetEnvAPIKeyAllEmpty 测试所有环境变量为空
func TestGetEnvAPIKeyAllEmpty(t *testing.T) {
	for _, v := range providerEnvVars[ProviderAnthropic] {
		t.Setenv(v, "")
	}

	got := GetEnvAPIKey(ProviderAnthropic)
	if got != "" {
		t.Errorf("Expected empty string, got: %s", got)
	}
}

// TestGetEnvAPIKeyUnregisteredProvider 测试未注册的 provider
func TestGetEnvAPIKeyUnregisteredProvider(t *testing.T) {
	got := GetEnvAPIKey(KnownProvider("unknown-provider"))
	if got != "" {
		t.Errorf("Expected empty string for unknown provider, got: %s", got)
	}
}

// TestFindEnvKeysNoneFound 测试没有找到任何环境变量
func TestFindEnvKeysNoneFound(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	keys := FindEnvKeys(ProviderOpenAI)
	if len(keys) != 0 {
		t.Errorf("Expected 0 keys, got %d: %v", len(keys), keys)
	}
}

// TestFindEnvKeysMultipleFound 测试找到多个环境变量
func TestFindEnvKeysMultipleFound(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "key1")
	t.Setenv("GEMINI_API_KEY", "key2")

	keys := FindEnvKeys(ProviderGoogle)
	if len(keys) != 2 {
		t.Errorf("Expected 2 keys, got %d: %v", len(keys), keys)
	}
}

// TestFindEnvKeysOneFound 测试只找到一个
func TestFindEnvKeysOneFound(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "gemini-key")

	keys := FindEnvKeys(ProviderGoogle)
	if len(keys) != 1 {
		t.Errorf("Expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != "GEMINI_API_KEY" {
		t.Errorf("Expected 'GEMINI_API_KEY', got: %s", keys[0])
	}
}

// TestResolveBaseURLTrimTrailingSlash 测试去除尾部斜杠
func TestResolveBaseURLTrimTrailingSlash(t *testing.T) {
	model := Model{BaseURL: "https://api.example.com/v1/"}
	got := ResolveBaseURL(model, "")
	if got != "https://api.example.com/v1" {
		t.Errorf("Expected trimmed URL, got: %s", got)
	}
}

// TestResolveBaseURLMultipleSlashes 测试多个尾部斜杠
func TestResolveBaseURLMultipleSlashes(t *testing.T) {
	model := Model{BaseURL: "https://api.example.com/v1///"}
	got := ResolveBaseURL(model, "")
	if got != "https://api.example.com/v1" {
		t.Errorf("Expected trimmed URL, got: %s", got)
	}
}

// TestResolveBaseURLNoTrailingSlash 测试没有尾部斜杠
func TestResolveBaseURLNoTrailingSlash(t *testing.T) {
	model := Model{BaseURL: "https://api.example.com/v1"}
	got := ResolveBaseURL(model, "")
	if got != "https://api.example.com/v1" {
		t.Errorf("Expected unchanged URL, got: %s", got)
	}
}

// TestResolveBaseURLEmptyModelURL 测试空的 model URL
func TestResolveBaseURLEmptyModelURL(t *testing.T) {
	model := Model{}
	got := ResolveBaseURL(model, "https://default.com/v1/")
	if got != "https://default.com/v1" {
		t.Errorf("Expected default URL trimmed, got: %s", got)
	}
}

// TestResolveBaseURLEmptyBoth 测试两个 URL 都为空
func TestResolveBaseURLEmptyBoth(t *testing.T) {
	model := Model{}
	got := ResolveBaseURL(model, "")
	if got != "" {
		t.Errorf("Expected empty string, got: %s", got)
	}
}

// TestResolveAPIKeyAllProviders 为所有 provider 测试 API Key 解析
func TestResolveAPIKeyAllProviders(t *testing.T) {
	tests := []struct {
		provider KnownProvider
		envVar   string
		envValue string
	}{
		{ProviderOpenAI, "OPENAI_API_KEY", "openai-key"},
		{ProviderMistral, "MISTRAL_API_KEY", "mistral-key"},
		{ProviderGroq, "GROQ_API_KEY", "groq-key"},
		{ProviderXAI, "XAI_API_KEY", "xai-key"},
		{ProviderDeepSeek, "DEEPSEEK_API_KEY", "deepseek-key"},
		{ProviderTogether, "TOGETHER_API_KEY", "together-key"},
		{ProviderFireworks, "FIREWORKS_API_KEY", "fireworks-key"},
		{ProviderOpenRouter, "OPENROUTER_API_KEY", "openrouter-key"},
		{ProviderCerebras, "CEREBRAS_API_KEY", "cerebras-key"},
		{ProviderHuggingFace, "HUGGINGFACE_API_KEY", "hf-key"},
	}

	for _, tc := range tests {
		t.Run(string(tc.provider), func(t *testing.T) {
			t.Setenv(tc.envVar, tc.envValue)
			got := ResolveAPIKey(tc.provider, "")
			if got != tc.envValue {
				t.Errorf("Expected %s, got %s", tc.envValue, got)
			}
		})
	}
}
