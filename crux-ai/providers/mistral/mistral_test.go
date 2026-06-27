package mistral

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	core "github.com/hycjack/crux-ai/core"
)

// TestNormalizeToolCallIDValid 测试有效的 9 位 ID 直接返回
func TestNormalizeToolCallIDValid(t *testing.T) {
	id := "abc123def"
	got := normalizeToolCallID(id)
	if got != id {
		t.Errorf("Expected %q, got %q", id, got)
	}
}

// TestNormalizeToolCallIDShort 测试短 ID 被填充
func TestNormalizeToolCallIDShort(t *testing.T) {
	got := normalizeToolCallID("abc")
	if len(got) != 9 {
		t.Errorf("Expected length 9, got %d", len(got))
	}
	if got[:3] != "abc" {
		t.Errorf("Expected first 3 chars to be 'abc', got: %s", got[:3])
	}
}

// TestNormalizeToolCallIDLong 测试长 ID 被截断
func TestNormalizeToolCallIDLong(t *testing.T) {
	got := normalizeToolCallID("abcdefghijklmnop")
	if len(got) != 9 {
		t.Errorf("Expected length 9, got %d", len(got))
	}
	if got != "abcdefghi" {
		t.Errorf("Expected 'abcdefghi', got: %s", got)
	}
}

// TestNormalizeToolCallIDEmpty 测试空字符串
func TestNormalizeToolCallIDEmpty(t *testing.T) {
	got := normalizeToolCallID("")
	if len(got) != 9 {
		t.Errorf("Expected length 9, got %d", len(got))
	}
}

// TestNormalizeToolCallIDInvalidChars 测试包含非字母数字字符
func TestNormalizeToolCallIDInvalidChars(t *testing.T) {
	got := normalizeToolCallID("abc-123-")
	if len(got) != 9 {
		t.Errorf("Expected length 9, got %d", len(got))
	}

	for _, c := range got {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			t.Errorf("Invalid char %q in result: %s", c, got)
		}
	}
}

// TestNormalizeToolCallIDNineCharsInvalid 测试 9 位但包含无效字符
func TestNormalizeToolCallIDNineCharsInvalid(t *testing.T) {
	got := normalizeToolCallID("abc-12-34")
	if len(got) != 9 {
		t.Errorf("Expected length 9, got %d", len(got))
	}

	for _, c := range got {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			t.Errorf("Invalid char %q in result: %s", c, got)
		}
	}
}

// TestNormalizeToolCallIDUppercase 测试大写字母被转换
func TestNormalizeToolCallIDUppercase(t *testing.T) {
	got := normalizeToolCallID("ABCDE")
	for _, c := range got {
		if c >= 'A' && c <= 'Z' {
			t.Errorf("Uppercase char %q should be normalized: %s", c, got)
		}
	}
}

// TestStreamMistralNoAPIKey 测试无 API Key 错误
func TestStreamMistralNoAPIKey(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")

	_, err := streamMistral(
		context.Background(),
		core.Model{Provider: core.ProviderMistral},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)

	if err == nil {
		t.Fatal("Expected error for missing API key")
	}
	if err.Error() != "mistral: no API key provided" {
		t.Errorf("Unexpected error: %v", err)
	}
}

// TestStreamMistralSuccess 测试成功流程
func TestStreamMistralSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	t.Setenv("MISTRAL_API_KEY", "test-key")

	stream, err := streamMistral(
		context.Background(),
		core.Model{
			Provider: core.ProviderMistral,
			BaseURL:  server.URL,
		},
		core.Context{Messages: []core.Message{core.UserMessage{Content: "Hi"}}},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamMistral failed: %v", err)
	}

	_, err = stream.Result()
	if err != nil {
		t.Fatalf("Result failed: %v", err)
	}
}

// TestStreamMistralAPIError 测试 API 错误
func TestStreamMistralAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "unauthorized"}`))
	}))
	defer server.Close()

	t.Setenv("MISTRAL_API_KEY", "bad-key")

	stream, err := streamMistral(
		context.Background(),
		core.Model{Provider: core.ProviderMistral, BaseURL: server.URL},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamMistral should not return error on creation: %v", err)
	}

	_, err = stream.Result()
	if err == nil {
		t.Fatal("Expected error for 401 response")
	}
}

// TestBuildMistralBody 测试请求体构建
func TestBuildMistralBody(t *testing.T) {
	model := core.Model{
		ID:        "mistral-large",
		MaxTokens: 1000,
	}

	temp := 0.7
	maxTokens := 500
	opts := core.StreamOptions{
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	}
	c := core.Context{
		SystemPrompt: "You are helpful",
		Messages:     []core.Message{core.UserMessage{Content: "Hello"}},
	}

	body, err := buildMistralBody(model, c, opts, Options{})
	if err != nil {
		t.Fatalf("buildMistralBody failed: %v", err)
	}

	if body["model"] != "mistral-large" {
		t.Errorf("Expected model 'mistral-large', got: %v", body["model"])
	}
	if body["max_tokens"] != 500 {
		t.Errorf("Expected max_tokens 500, got: %v", body["max_tokens"])
	}
	if body["temperature"] != 0.7 {
		t.Errorf("Expected temperature 0.7, got: %v", body["temperature"])
	}
	if body["stream"] != true {
		t.Error("Expected stream to be true")
	}
}

// TestBuildMistralBodyReasoning 测试推理选项
func TestBuildMistralBodyReasoning(t *testing.T) {
	model := core.Model{ID: "mistral-large"}
	opts := Options{
		PromptMode:      "reasoning",
		ReasoningEffort: "high",
	}

	body, err := buildMistralBody(model, core.Context{}, core.StreamOptions{}, opts)
	if err != nil {
		t.Fatalf("buildMistralBody failed: %v", err)
	}

	if body["prompt_mode"] != "reasoning" {
		t.Errorf("Expected prompt_mode 'reasoning', got: %v", body["prompt_mode"])
	}
}

// TestConvertUserContentString 测试字符串内容
func TestConvertUserContentString(t *testing.T) {
	got, err := convertUserContent("Hello")
	if err != nil {
		t.Fatalf("convertUserContent failed: %v", err)
	}
	if got != "Hello" {
		t.Errorf("Expected 'Hello', got: %v", got)
	}
}

// TestConvertUserContentBlocks 测试多种内容块
func TestConvertUserContentBlocks(t *testing.T) {
	content := []core.ContentBlock{
		core.TextContent{Type: "text", Text: "Hi"},
		core.ImageContent{Type: "image", Data: "imgdata", MimeType: "image/jpeg"},
	}

	got, err := convertUserContent(content)
	if err != nil {
		t.Fatalf("convertUserContent failed: %v", err)
	}

	blocks, ok := got.([]any)
	if !ok {
		t.Fatalf("Expected []any, got: %T", got)
	}
	if len(blocks) != 2 {
		t.Errorf("Expected 2 blocks, got %d", len(blocks))
	}
}

// TestConvertAssistantMessageWithToolCalls 测试带工具调用的助手消息
func TestConvertAssistantMessageWithToolCalls(t *testing.T) {
	content := []core.ContentBlock{
		core.ToolCall{
			Type:      "toolCall",
			ID:        "call_123",
			Name:      "test_fn",
			Arguments: json.RawMessage(`{"a": 1}`),
		},
	}

	msg := convertAssistantMessage(content)
	if msg["role"] != "assistant" {
		t.Errorf("Expected role 'assistant', got: %v", msg["role"])
	}
	calls, ok := msg["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatal("Expected one tool call")
	}
}

// TestMapStopReason 测试 stop reason 映射
func TestMapStopReason(t *testing.T) {
	tests := []struct {
		input    string
		expected core.StopReason
	}{
		{"stop", core.StopStop},
		{"length", core.StopLength},
		{"tool_calls", core.StopToolUse},
		{"other", core.StopStop},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := mapStopReason(tc.input)
			if got != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, got)
			}
		})
	}
}

// TestStreamSimpleNoAPIKey 测试 StreamSimple 在无 API Key 时的行为
func TestStreamSimpleNoAPIKey(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")

	provider := &Provider{}
	_, err := provider.StreamSimple(
		context.Background(),
		core.Model{Provider: core.ProviderMistral},
		core.Context{},
		core.SimpleStreamOptions{},
	)
	if err == nil {
		t.Fatal("Expected error for missing API key")
	}
}

// TestStreamSimpleWithReasoning 测试带推理的 StreamSimple
func TestStreamSimpleWithReasoning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	t.Setenv("MISTRAL_API_KEY", "test-key")

	provider := &Provider{}
	stream, err := provider.StreamSimple(
		context.Background(),
		core.Model{Provider: core.ProviderMistral, BaseURL: server.URL},
		core.Context{},
		core.SimpleStreamOptions{
			StreamOptions: core.StreamOptions{},
			Reasoning:     core.ThinkingHigh,
		},
	)
	if err != nil {
		t.Fatalf("StreamSimple failed: %v", err)
	}
	if stream == nil {
		t.Fatal("Expected non-nil stream")
	}
	stream.End(core.AssistantMessage{})
}

// TestProviderConstruction 测试构造函数
func TestProviderConstruction(t *testing.T) {
	p := New()
	if p == nil {
		t.Fatal("New() returned nil")
	}
}

// TestStreamEndpoint 测试流端点配置
func TestStreamEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("Expected path /chat/completions, got: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("Expected POST, got: %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Expected Bearer auth, got: %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	t.Setenv("MISTRAL_API_KEY", "test-key")

	_, err := streamMistral(
		context.Background(),
		core.Model{Provider: core.ProviderMistral, BaseURL: server.URL},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamMistral failed: %v", err)
	}
}

// TestResolveAPIKeyFromOptions 验证 ResolveAPIKey 行为
func TestResolveAPIKeyFromOptions(t *testing.T) {
	got := core.ResolveAPIKey(core.ProviderMistral, "explicit-key")
	if got != "explicit-key" {
		t.Errorf("Expected explicit-key, got: %s", got)
	}
}

// 确保导入使用避免 unused import 错误
var _ = errors.New
var _ = os.Getenv
