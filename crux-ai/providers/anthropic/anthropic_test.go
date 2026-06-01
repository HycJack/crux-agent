package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "crux-ai/core"
)

// TestStreamAnthropicNoAPIKey 测试无 API Key
func TestStreamAnthropicNoAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")

	_, err := streamAnthropic(
		context.Background(),
		core.Model{Provider: core.ProviderAnthropic},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)

	if err == nil {
		t.Fatal("Expected error for missing API key")
	}
	if err.Error() != "anthropic: no API key provided" {
		t.Errorf("Unexpected error: %v", err)
	}
}

// TestStreamAnthropicSuccess 测试成功流程
func TestStreamAnthropicSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("event: message_start\n"))
		w.Write([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}` + "\n\n"))
		w.Write([]byte("event: content_block_start\n"))
		w.Write([]byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n"))
		w.Write([]byte("event: content_block_delta\n"))
		w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}` + "\n\n"))
		w.Write([]byte("event: content_block_stop\n"))
		w.Write([]byte(`data: {"type":"content_block_stop","index":0}` + "\n\n"))
		w.Write([]byte("event: message_delta\n"))
		w.Write([]byte(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	stream, err := streamAnthropic(
		context.Background(),
		core.Model{Provider: core.ProviderAnthropic, BaseURL: server.URL},
		core.Context{Messages: []core.Message{core.UserMessage{Content: "Hi"}}},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamAnthropic failed: %v", err)
	}

	_, err = stream.Result()
	if err != nil {
		t.Fatalf("Result failed: %v", err)
	}
}

// TestStreamAnthropicAPIError 测试 API 错误
func TestStreamAnthropicAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "unauthorized"}`))
	}))
	defer server.Close()

	t.Setenv("ANTHROPIC_API_KEY", "bad-key")

	stream, err := streamAnthropic(
		context.Background(),
		core.Model{Provider: core.ProviderAnthropic, BaseURL: server.URL},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamAnthropic should not error on creation: %v", err)
	}

	_, err = stream.Result()
	if err == nil {
		t.Fatal("Expected error from API failure")
	}
}

// TestConvertUserContentString 测试字符串用户内容
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
		core.ImageContent{Type: "image", Data: "imgdata", MimeType: "image/png"},
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

// TestConvertAssistantContentWithSignatures 测试带签名的内容
func TestConvertAssistantContentWithSignatures(t *testing.T) {
	content := []core.ContentBlock{
		core.TextContent{Type: "text", Text: "Hi", TextSignature: "text_sig"},
		core.ThinkingContent{Type: "thinking", Thinking: "Reasoning", ThinkingSignature: "think_sig"},
		core.ToolCall{
			Type:      "toolCall",
			ID:        "1",
			Name:      "fn",
			Arguments: json.RawMessage(`{}`),
		},
	}

	blocks := convertAssistantContent(content)
	if len(blocks) != 3 {
		t.Fatalf("Expected 3 blocks, got %d", len(blocks))
	}

	textBlock := blocks[0].(map[string]any)
	if textBlock["signature"] != "text_sig" {
		t.Errorf("Expected text signature 'text_sig', got: %v", textBlock["signature"])
	}

	thinkingBlock := blocks[1].(map[string]any)
	if thinkingBlock["signature"] != "think_sig" {
		t.Errorf("Expected thinking signature 'think_sig', got: %v", thinkingBlock["signature"])
	}
}

// TestConvertToolResultContentEmpty 测试空内容
func TestConvertToolResultContentEmpty(t *testing.T) {
	result := convertToolResultContent([]core.ContentBlock{})
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	arr, ok := result.([]any)
	if !ok {
		t.Fatalf("Expected []any, got: %T", result)
	}
	if len(arr) != 0 {
		t.Errorf("Expected empty array, got %d items", len(arr))
	}
}

// TestConvertToolResultContentSingleText 测试单个文本块简化为字符串
func TestConvertToolResultContentSingleText(t *testing.T) {
	content := []core.ContentBlock{
		core.TextContent{Type: "text", Text: "Result"},
	}

	result := convertToolResultContent(content)
	if result != "Result" {
		t.Errorf("Expected 'Result' (simplified), got: %v", result)
	}
}

// TestConvertToolResultContentMultiple 测试多个块
func TestConvertToolResultContentMultiple(t *testing.T) {
	content := []core.ContentBlock{
		core.TextContent{Type: "text", Text: "Part 1"},
		core.TextContent{Type: "text", Text: "Part 2"},
	}

	result := convertToolResultContent(content)
	arr, ok := result.([]any)
	if !ok {
		t.Fatalf("Expected []any, got: %T", result)
	}
	if len(arr) != 2 {
		t.Errorf("Expected 2 blocks, got %d", len(arr))
	}
}

// TestConvertTools 测试工具转换
func TestConvertTools(t *testing.T) {
	tools := []core.Tool{
		{
			Name:        "test_tool",
			Description: "Test description",
			Parameters:  json.RawMessage(`{"type": "object"}`),
		},
	}

	result := convertTools(tools, false)
	if len(result) != 1 {
		t.Fatalf("Expected 1 tool, got %d", len(result))
	}

	tool := result[0]
	if tool["name"] != "test_tool" {
		t.Errorf("Expected name 'test_tool', got: %v", tool["name"])
	}
	if _, hasSchema := tool["input_schema"]; !hasSchema {
		t.Error("Expected input_schema")
	}
}

// TestConvertToolsWithEagerStreaming 测试 eager streaming
func TestConvertToolsWithEagerStreaming(t *testing.T) {
	tools := []core.Tool{{Name: "tool1"}}

	result := convertTools(tools, true)
	if len(result) != 1 {
		t.Fatalf("Expected 1 tool, got %d", len(result))
	}
	if result[0]["eager_input_streaming"] != true {
		t.Error("Expected eager_input_streaming to be true")
	}
}

// TestMapStopReason 测试 stop reason 映射
func TestMapStopReason(t *testing.T) {
	tests := []struct {
		input    string
		expected core.StopReason
	}{
		{"end_turn", core.StopStop},
		{"stop_sequence", core.StopStop},
		{"max_tokens", core.StopLength},
		{"tool_use", core.StopToolUse},
		{"unknown", core.StopStop},
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

// TestBuildRequestBodyWithThinking 测试带 thinking 的请求体
func TestBuildRequestBodyWithThinking(t *testing.T) {
	model := core.Model{ID: "claude-3", MaxTokens: 1000}
	opts := Options{
		ThinkingEnabled:      true,
		ThinkingBudgetTokens: 5000,
	}

	body, err := buildRequestBody(model, core.Context{}, core.StreamOptions{}, opts)
	if err != nil {
		t.Fatalf("buildRequestBody failed: %v", err)
	}

	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatal("Expected thinking in body")
	}
	if thinking["type"] != "enabled" {
		t.Errorf("Expected type 'enabled', got: %v", thinking["type"])
	}
	if thinking["budget_tokens"] != 5000 {
		t.Errorf("Expected budget 5000, got: %v", thinking["budget_tokens"])
	}
}

// TestBuildRequestBodyWithSystemPrompt 测试带系统提示
func TestBuildRequestBodyWithSystemPrompt(t *testing.T) {
	model := core.Model{ID: "claude-3"}
	c := core.Context{SystemPrompt: "You are helpful"}

	body, err := buildRequestBody(model, c, core.StreamOptions{}, Options{})
	if err != nil {
		t.Fatalf("buildRequestBody failed: %v", err)
	}

	if body["system"] != "You are helpful" {
		t.Errorf("Expected system prompt, got: %v", body["system"])
	}
}

// TestConvertMessagesEmpty 测试空消息
func TestConvertMessagesEmpty(t *testing.T) {
	result, err := convertMessages([]core.Message{})
	if err != nil {
		t.Fatalf("convertMessages failed: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(result))
	}
}

// TestConvertMessagesToolResultWithError 测试错误状态
func TestConvertMessagesToolResultWithError(t *testing.T) {
	msgs := []core.Message{
		core.ToolResultMessage{
			ToolCallID: "call_1",
			ToolName:   "test",
			Content:    []core.ContentBlock{core.TextContent{Text: "error message"}},
			IsError:    true,
		},
	}

	result, err := convertMessages(msgs)
	if err != nil {
		t.Fatalf("convertMessages failed: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(result))
	}

	content, ok := result[0]["content"].([]any)
	if !ok {
		t.Fatal("Expected content array")
	}
	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatal("Expected block map")
	}
	if block["is_error"] != true {
		t.Error("Expected is_error to be true")
	}
}

// TestStreamAnthropicThinkingContent 测试 thinking 内容
func TestStreamAnthropicThinkingContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("event: content_block_start\n"))
		w.Write([]byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}` + "\n\n"))
		w.Write([]byte("event: content_block_delta\n"))
		w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think"}}` + "\n\n"))
		w.Write([]byte("event: content_block_stop\n"))
		w.Write([]byte(`data: {"type":"content_block_stop","index":0}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	stream, err := streamAnthropic(
		context.Background(),
		core.Model{Provider: core.ProviderAnthropic, BaseURL: server.URL},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamAnthropic failed: %v", err)
	}

	_, err = stream.Result()
	if err != nil {
		t.Fatalf("Result failed: %v", err)
	}
}

// TestStreamAnthropicToolUse 测试工具调用
func TestStreamAnthropicToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("event: content_block_start\n"))
		w.Write([]byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather"}}` + "\n\n"))
		w.Write([]byte("event: content_block_delta\n"))
		w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}` + "\n\n"))
		w.Write([]byte("event: content_block_stop\n"))
		w.Write([]byte(`data: {"type":"content_block_stop","index":0}` + "\n\n"))
		w.Write([]byte("event: message_delta\n"))
		w.Write([]byte(`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	stream, err := streamAnthropic(
		context.Background(),
		core.Model{Provider: core.ProviderAnthropic, BaseURL: server.URL},
		core.Context{},
		core.StreamOptions{},
		Options{},
	)
	if err != nil {
		t.Fatalf("streamAnthropic failed: %v", err)
	}

	_, err = stream.Result()
	if err != nil {
		t.Fatalf("Result failed: %v", err)
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
		if !strings.Contains(r.Header.Get("anthropic-beta"), "interleaved-thinking") {
			t.Error("Expected interleaved-thinking header when thinking is enabled")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	provider := &Provider{}
	stream, err := provider.StreamSimple(
		context.Background(),
		core.Model{Provider: core.ProviderAnthropic, BaseURL: server.URL},
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
