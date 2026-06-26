package core

import (
	"encoding/json"
	"testing"
	"time"
)

// TestNewTimeoutClientWithPositiveValue 测试有效超时
func TestNewTimeoutClientWithPositiveValue(t *testing.T) {
	client := NewTimeoutClient(1000)
	expected := 1000 * time.Millisecond
	if client.Timeout != expected {
		t.Errorf("Expected timeout %v, got %v", expected, client.Timeout)
	}
}

// TestNewTimeoutClientWithZero 测试零值（应使用默认值）
func TestNewTimeoutClientWithZero(t *testing.T) {
	client := NewTimeoutClient(0)
	expected := 5 * time.Minute
	if client.Timeout != expected {
		t.Errorf("Expected default timeout %v, got %v", expected, client.Timeout)
	}
}

// TestNewTimeoutClientWithNegative 测试负值（应使用默认值）
func TestNewTimeoutClientWithNegative(t *testing.T) {
	client := NewTimeoutClient(-1)
	expected := 5 * time.Minute
	if client.Timeout != expected {
		t.Errorf("Expected default timeout %v, got %v", expected, client.Timeout)
	}
}

// TestNewTimeoutClientReturnsNonNil 测试返回值不为 nil
func TestNewTimeoutClientReturnsNonNil(t *testing.T) {
	client := NewTimeoutClient(1000)
	if client == nil {
		t.Fatal("NewTimeoutClient returned nil")
	}
}

// TestGetEnvAPIKeyNotSet 测试环境变量未设置
func TestGetEnvAPIKeyNotSet(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")

	key := GetEnvAPIKey(ProviderAnthropic)
	if key != "" {
		t.Errorf("Expected empty key, got: %s", key)
	}
}

// TestGetEnvAPIKeySet 测试环境变量已设置
func TestGetEnvAPIKeySet(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key-123")

	key := GetEnvAPIKey(ProviderAnthropic)
	if key != "test-key-123" {
		t.Errorf("Expected 'test-key-123', got: %s", key)
	}
}

// TestResolveAPIKeyFromOptions 测试从 options 获取 key
func TestResolveAPIKeyFromOptions(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")

	key := ResolveAPIKey(ProviderOpenAI, "opts-key")
	if key != "opts-key" {
		t.Errorf("Expected 'opts-key', got: %s", key)
	}
}

// TestResolveAPIKeyFromEnv 测试从环境变量获取 key
func TestResolveAPIKeyFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")

	key := ResolveAPIKey(ProviderOpenAI, "")
	if key != "env-key" {
		t.Errorf("Expected 'env-key', got: %s", key)
	}
}

// TestResolveAPIKeyNotFound 测试两者都没有
func TestResolveAPIKeyNotFound(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	key := ResolveAPIKey(ProviderOpenAI, "")
	if key != "" {
		t.Errorf("Expected empty key, got: %s", key)
	}
}

// TestResolveBaseURLFromModel 测试从 model 获取 baseURL
func TestResolveBaseURLFromModel(t *testing.T) {
	model := Model{BaseURL: "https://api.example.com/v1/"}
	url := ResolveBaseURL(model, "https://default.com")
	if url != "https://api.example.com/v1" {
		t.Errorf("Expected trimmed model URL, got: %s", url)
	}
}

// TestResolveBaseURLFromDefault 测试使用默认值
func TestResolveBaseURLFromDefault(t *testing.T) {
	model := Model{}
	url := ResolveBaseURL(model, "https://default.com/v1/")
	if url != "https://default.com/v1" {
		t.Errorf("Expected trimmed default URL, got: %s", url)
	}
}

// TestUserMessageSerialization 测试 UserMessage JSON 序列化
func TestUserMessageSerialization(t *testing.T) {
	msg := UserMessage{
		Role:      "user",
		Content:   "Hello, world!",
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if raw["role"] != "user" {
		t.Errorf("Expected role 'user', got: %v", raw["role"])
	}
	if raw["content"] != "Hello, world!" {
		t.Errorf("Expected content 'Hello, world!', got: %v", raw["content"])
	}
}

// TestAssistantMessageSerialization 测试 AssistantMessage 序列化
func TestAssistantMessageSerialization(t *testing.T) {
	msg := AssistantMessage{
		Role:       "assistant",
		Content:    []ContentBlock{TextContent{Type: "text", Text: "Hi"}},
		API:        APIAnthropicMessages,
		Provider:   ProviderAnthropic,
		Model:      "claude-3",
		StopReason: StopStop,
		Usage:      Usage{Input: 10, Output: 20, TotalTokens: 30},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if raw["stopReason"] != "stop" {
		t.Errorf("Expected stop reason 'stop', got: %v", raw["stopReason"])
	}

	usage, ok := raw["usage"].(map[string]any)
	if !ok {
		t.Fatal("Expected usage to be a map")
	}
	if int(usage["input"].(float64)) != 10 {
		t.Errorf("Expected input 10, got: %v", usage["input"])
	}
}

// TestEventErrorSerialization 测试 EventError 序列化
func TestEventErrorSerialization(t *testing.T) {
	evt := EventError{
		Type:         "error",
		ErrorMessage: "something went wrong",
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded["errorMessage"] != "something went wrong" {
		t.Errorf("Expected errorMessage 'something went wrong', got: %v", decoded["errorMessage"])
	}
}

// TestEventToolCallEndWithSignature 测试带签名的 tool call
func TestEventToolCallEndWithSignature(t *testing.T) {
	args := json.RawMessage(`{"x": 1}`)
	evt := EventToolCallEnd{
		Type:             "toolcall_end",
		ID:               "call_123",
		Arguments:        args,
		ThoughtSignature: "sig_abc",
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded["thoughtSignature"] != "sig_abc" {
		t.Errorf("Expected thoughtSignature 'sig_abc', got: %v", decoded["thoughtSignature"])
	}
}

// TestEventToolCallEndWithoutSignature 测试不带签名的 tool call
func TestEventToolCallEndWithoutSignature(t *testing.T) {
	args := json.RawMessage(`{"x": 1}`)
	evt := EventToolCallEnd{
		Type:      "toolcall_end",
		ID:        "call_123",
		Arguments: args,
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if _, ok := decoded["thoughtSignature"]; ok {
		t.Error("thoughtSignature should be omitted when empty")
	}
}
