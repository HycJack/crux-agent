package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/hycjack/crux-ai/core"
)

// TestStreamGoogleNoAPIKey 测试无 API Key
func TestStreamGoogleNoAPIKey(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	_, err := streamGoogle(
		context.Background(),
		core.Model{Provider: core.ProviderGoogle},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)
	if err == nil {
		t.Fatal("Expected error for missing API key")
	}
}

// TestStreamGoogleSuccess 测试成功流程
func TestStreamGoogleSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hello\"}]}}]}\n\n"))
		w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\" world\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":2,\"totalTokenCount\":7}}\n\n"))
	}))
	defer server.Close()

	t.Setenv("GOOGLE_API_KEY", "test-key")

	stream, err := streamGoogle(
		context.Background(),
		core.Model{Provider: core.ProviderGoogle, ID: "gemini-2.0-flash", BaseURL: server.URL},
		core.Context{Messages: []core.Message{core.UserMessage{Content: "Hi"}}},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamGoogle failed: %v", err)
	}

	_, err = stream.Result()
	if err != nil {
		t.Fatalf("Result failed: %v", err)
	}
}

// TestStreamGoogleAPIError 测试 API 错误
func TestStreamGoogleAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": "forbidden"}`))
	}))
	defer server.Close()

	t.Setenv("GOOGLE_API_KEY", "bad-key")

	stream, err := streamGoogle(
		context.Background(),
		core.Model{Provider: core.ProviderGoogle, ID: "gemini-2.0-flash", BaseURL: server.URL},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamGoogle should not error: %v", err)
	}

	_, err = stream.Result()
	if err == nil {
		t.Fatal("Expected error from API")
	}
}

// TestStreamGoogleThinkingContent 测试 thinking 内容
func TestStreamGoogleThinkingContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"thought\":true,\"text\":\"Reasoning...\"}]}}]}\n\n"))
		w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Answer\"}]}}]}\n\n"))
	}))
	defer server.Close()

	t.Setenv("GOOGLE_API_KEY", "test-key")

	stream, err := streamGoogle(
		context.Background(),
		core.Model{Provider: core.ProviderGoogle, ID: "gemini-2.0-flash", BaseURL: server.URL},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamGoogle failed: %v", err)
	}

	_, err = stream.Result()
	if err != nil {
		t.Fatalf("Result failed: %v", err)
	}
}

// TestStreamGoogleToolCall 测试工具调用
func TestStreamGoogleToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"get_weather\",\"args\":{\"city\":\"Beijing\"}}}]}}]}\n\n"))
	}))
	defer server.Close()

	t.Setenv("GOOGLE_API_KEY", "test-key")

	stream, err := streamGoogle(
		context.Background(),
		core.Model{Provider: core.ProviderGoogle, ID: "gemini-2.0-flash", BaseURL: server.URL},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamGoogle failed: %v", err)
	}

	_, err = stream.Result()
	if err != nil {
		t.Fatalf("Result failed: %v", err)
	}
}

// TestStreamGoogleAPIKeyInHeader 测试 API Key 通过 header 传递
func TestStreamGoogleAPIKeyInHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("Expected x-goog-api-key header, got: %s", r.Header.Get("x-goog-api-key"))
		}
		if strings.Contains(r.URL.RawQuery, "key=") {
			t.Error("API key should not be in URL")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("GOOGLE_API_KEY", "test-key")

	stream, err := streamGoogle(
		context.Background(),
		core.Model{Provider: core.ProviderGoogle, ID: "gemini-2.0-flash", BaseURL: server.URL},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamGoogle failed: %v", err)
	}

	_, _ = stream.Result()
}

// TestBuildGoogleBodyBasic 测试基本 body 构建
func TestBuildGoogleBodyBasic(t *testing.T) {
	model := core.Model{ID: "gemini-2.0-flash", MaxTokens: 1000}
	c := core.Context{
		SystemPrompt: "You are helpful",
		Messages:     []core.Message{core.UserMessage{Content: "Hello"}},
	}

	body, err := buildGoogleBody(model, c, core.StreamOptions{}, Options{})
	if err != nil {
		t.Fatalf("buildGoogleBody failed: %v", err)
	}

	if body["contents"] == nil {
		t.Error("Expected contents in body")
	}
	if body["systemInstruction"] == nil {
		t.Error("Expected systemInstruction in body")
	}
}

// TestBuildGoogleBodyWithThinking 测试带 thinking 的 body
func TestBuildGoogleBodyWithThinking(t *testing.T) {
	model := core.Model{ID: "gemini-2.0-flash"}
	opts := Options{
		Thinking: &ThinkingConfig{
			Enabled:      true,
			BudgetTokens: 5000,
			Level:        "HIGH",
		},
	}

	body, err := buildGoogleBody(model, core.Context{}, core.StreamOptions{}, opts)
	if err != nil {
		t.Fatalf("buildGoogleBody failed: %v", err)
	}

	thinkingConfig, ok := body["thinkingConfig"].(map[string]any)
	if !ok {
		t.Fatal("Expected thinkingConfig in body")
	}
	if thinkingConfig["includeThoughts"] != true {
		t.Error("Expected includeThoughts to be true")
	}
	if thinkingConfig["thinkingBudget"] != 5000 {
		t.Errorf("Expected thinkingBudget 5000, got: %v", thinkingConfig["thinkingBudget"])
	}
}

// TestBuildGoogleBodyWithThinkingLevelOnly 测试只设置 level
func TestBuildGoogleBodyWithThinkingLevelOnly(t *testing.T) {
	model := core.Model{ID: "gemini-2.0-flash"}
	opts := Options{
		Thinking: &ThinkingConfig{
			Enabled: true,
			Level:   "LOW",
		},
	}

	body, err := buildGoogleBody(model, core.Context{}, core.StreamOptions{}, opts)
	if err != nil {
		t.Fatalf("buildGoogleBody failed: %v", err)
	}

	thinkingConfig, ok := body["thinkingConfig"].(map[string]any)
	if !ok {
		t.Fatal("Expected thinkingConfig")
	}
	if thinkingConfig["thinkingLevel"] != "LOW" {
		t.Errorf("Expected level 'LOW', got: %v", thinkingConfig["thinkingLevel"])
	}
}

// TestBuildGoogleBodyWithToolChoice 测试带 toolChoice
func TestBuildGoogleBodyWithToolChoice(t *testing.T) {
	model := core.Model{ID: "gemini-2.0-flash"}
	opts := Options{ToolChoice: "ANY"}

	body, err := buildGoogleBody(model, core.Context{}, core.StreamOptions{}, opts)
	if err != nil {
		t.Fatalf("buildGoogleBody failed: %v", err)
	}

	if body["toolConfig"] == nil {
		t.Error("Expected toolConfig in body")
	}
}

// TestMapThinkingLevel 测试 thinking level 映射
func TestMapThinkingLevel(t *testing.T) {
	tests := []struct {
		input    core.ThinkingLevel
		expected string
	}{
		{core.ThinkingMinimal, "MINIMAL"},
		{core.ThinkingLow, "LOW"},
		{core.ThinkingMedium, "MEDIUM"},
		{core.ThinkingHigh, "HIGH"},
		{core.ThinkingXHigh, "HIGH"},
		{core.ThinkingLevel("unknown"), "MEDIUM"},
	}

	for _, tc := range tests {
		t.Run(string(tc.input), func(t *testing.T) {
			got := mapThinkingLevel(tc.input)
			if got != tc.expected {
				t.Errorf("mapThinkingLevel(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// TestProviderConstruction 测试构造函数
func TestProviderConstruction(t *testing.T) {
	p := New()
	if p == nil {
		t.Fatal("New() returned nil")
	}
}

// TestStreamSimpleWithReasoning 测试 StreamSimple
func TestStreamSimpleWithReasoning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	t.Setenv("GOOGLE_API_KEY", "test-key")

	provider := &Provider{}
	stream, err := provider.StreamSimple(
		context.Background(),
		core.Model{Provider: core.ProviderGoogle, ID: "gemini-2.0-flash", BaseURL: server.URL},
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

// TestStreamSimpleWithThinkingBudgets 测试带预算
func TestStreamSimpleWithThinkingBudgets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	t.Setenv("GOOGLE_API_KEY", "test-key")

	provider := &Provider{}
	stream, err := provider.StreamSimple(
		context.Background(),
		core.Model{Provider: core.ProviderGoogle, ID: "gemini-2.0-flash", BaseURL: server.URL},
		core.Context{},
		core.SimpleStreamOptions{
			StreamOptions:  core.StreamOptions{},
			Reasoning:      core.ThinkingMedium,
			ThinkingBudgets: map[string]int{"medium": 3000},
		},
	)
	if err != nil {
		t.Fatalf("StreamSimple failed: %v", err)
	}
	stream.End(core.AssistantMessage{})
}

// TestBuildGoogleBodyWithTools 测试带工具
func TestBuildGoogleBodyWithTools(t *testing.T) {
	model := core.Model{ID: "gemini-2.0-flash"}
	c := core.Context{
		Tools: []core.Tool{
			{Name: "test_tool", Description: "Test", Parameters: json.RawMessage(`{}`)},
		},
	}

	body, err := buildGoogleBody(model, c, core.StreamOptions{}, Options{})
	if err != nil {
		t.Fatalf("buildGoogleBody failed: %v", err)
	}
	if body["tools"] == nil {
		t.Error("Expected tools in body")
	}
}

// TestStreamGoogleUsageMetadata 测试 usage 统计
func TestStreamGoogleUsageMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hi\"}]}}],\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":50,\"totalTokenCount\":150}}\n\n"))
	}))
	defer server.Close()

	t.Setenv("GOOGLE_API_KEY", "test-key")

	stream, err := streamGoogle(
		context.Background(),
		core.Model{Provider: core.ProviderGoogle, ID: "gemini-2.0-flash", BaseURL: server.URL, Cost: core.Cost{Input: 0.001, Output: 0.002}},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamGoogle failed: %v", err)
	}

	msg, err := stream.Result()
	if err != nil {
		t.Fatalf("Result failed: %v", err)
	}

	if msg.Usage.Input != 100 {
		t.Errorf("Expected input 100, got: %d", msg.Usage.Input)
	}
	if msg.Usage.Output != 50 {
		t.Errorf("Expected output 50, got: %d", msg.Usage.Output)
	}
}
