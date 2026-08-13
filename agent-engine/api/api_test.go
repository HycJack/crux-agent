package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hycjack/crux-ai/core"
)

// ─── Tests ──────────────────────────────────────────────────────────────────

func TestParseChatRequest_Basic(t *testing.T) {
	raw := `{
		"model": "gpt-4",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hello!"}
		],
		"temperature": 0.7,
		"max_tokens": 100,
		"stream": false
	}`

	var req ChatRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	model := core.Model{ID: "gpt-4", MaxTokens: 4096}
	ctx, opts, err := ParseChatRequest(&req, model)
	if err != nil {
		t.Fatalf("ParseChatRequest: %v", err)
	}

	if ctx.SystemPrompt != "You are helpful." {
		t.Fatalf("system prompt: got %q, want %q", ctx.SystemPrompt, "You are helpful.")
	}

	if len(ctx.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(ctx.Messages))
	}

	um, ok := ctx.Messages[0].(core.UserMessage)
	if !ok {
		t.Fatalf("expected UserMessage, got %T", ctx.Messages[0])
	}
	if um.Content != "Hello!" {
		t.Fatalf("user content: got %v, want %q", um.Content, "Hello!")
	}

	if opts.Temperature == nil || *opts.Temperature != 0.7 {
		t.Fatalf("temperature: got %v, want 0.7", opts.Temperature)
	}

	if opts.MaxTokens == nil || *opts.MaxTokens != 100 {
		t.Fatalf("max_tokens: got %v, want 100", opts.MaxTokens)
	}
}

func TestParseChatRequest_MultiContent(t *testing.T) {
	raw := `{
		"model": "gpt-4",
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "Describe this image:"},
					{"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo="}}
				]
			}
		]
	}`

	var req ChatRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	model := core.Model{ID: "gpt-4"}
	ctx, _, err := ParseChatRequest(&req, model)
	if err != nil {
		t.Fatalf("ParseChatRequest: %v", err)
	}

	if len(ctx.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(ctx.Messages))
	}

	um, ok := ctx.Messages[0].(core.UserMessage)
	if !ok {
		t.Fatalf("expected UserMessage, got %T", ctx.Messages[0])
	}

	blocks, ok := um.Content.([]core.ContentBlock)
	if !ok {
		t.Fatalf("expected []ContentBlock, got %T", um.Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(blocks))
	}

	tc, ok := blocks[0].(core.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", blocks[0])
	}
	if tc.Text != "Describe this image:" {
		t.Fatalf("text: got %q", tc.Text)
	}

	ic, ok := blocks[1].(core.ImageContent)
	if !ok {
		t.Fatalf("expected ImageContent, got %T", blocks[1])
	}
	if ic.Data != "iVBORw0KGgo=" {
		t.Fatalf("image data: got %s", ic.Data)
	}
}

func TestParseChatRequest_Tools(t *testing.T) {
	raw := `{
		"model": "gpt-4",
		"messages": [
			{"role": "user", "content": "What's the weather?"}
		],
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "get_weather",
					"description": "Get weather for a city",
					"parameters": {
						"type": "object",
						"properties": {
							"city": {"type": "string"}
						},
						"required": ["city"]
					}
				}
			}
		]
	}`

	var req ChatRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	model := core.Model{ID: "gpt-4"}
	ctx, _, err := ParseChatRequest(&req, model)
	if err != nil {
		t.Fatalf("ParseChatRequest: %v", err)
	}

	if len(ctx.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(ctx.Tools))
	}
	if ctx.Tools[0].Name != "get_weather" {
		t.Fatalf("tool name: got %q", ctx.Tools[0].Name)
	}
}

func TestParseChatRequest_ToolCalls(t *testing.T) {
	raw := `{
		"model": "gpt-4",
		"messages": [
			{"role": "user", "content": "What's the weather?"},
			{
				"role": "assistant",
				"content": null,
				"tool_calls": [
					{
						"id": "call_1",
						"type": "function",
						"function": {
							"name": "get_weather",
							"arguments": "{\"city\": \"Beijing\"}"
						}
					}
				]
			},
			{
				"role": "tool",
				"tool_call_id": "call_1",
				"content": "22°C, sunny"
			}
		]
	}`

	var req ChatRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	model := core.Model{ID: "gpt-4"}
	ctx, _, err := ParseChatRequest(&req, model)
	if err != nil {
		t.Fatalf("ParseChatRequest: %v", err)
	}

	if len(ctx.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(ctx.Messages))
	}

	// Check assistant message with tool call
	am, ok := ctx.Messages[1].(core.AssistantMessage)
	if !ok {
		t.Fatalf("expected AssistantMessage, got %T", ctx.Messages[1])
	}
	if len(am.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(am.Content))
	}
	tc, ok := am.Content[0].(core.ToolCall)
	if !ok {
		t.Fatalf("expected ToolCall, got %T", am.Content[0])
	}
	if tc.ID != "call_1" || tc.Name != "get_weather" {
		t.Fatalf("tool call: id=%q name=%q", tc.ID, tc.Name)
	}

	// Check tool result
	tr, ok := ctx.Messages[2].(core.ToolResultMessage)
	if !ok {
		t.Fatalf("expected ToolResultMessage, got %T", ctx.Messages[2])
	}
	if tr.ToolCallID != "call_1" {
		t.Fatalf("tool result id: got %q", tr.ToolCallID)
	}
}

func TestToChatResponse(t *testing.T) {
	msg := core.AssistantMessage{
		Role:      core.MessageRoleAssistant,
		Timestamp: time.Now(),
		StopReason: core.StopStop,
		Content: []core.ContentBlock{
			core.TextContent{Type: "text", Text: "Hello! How can I help?"},
		},
		Usage: core.Usage{
			Input:  10,
			Output: 20,
			TotalTokens: 30,
		},
	}

	resp := ToChatResponse(msg, "gpt-4", "test-id")

	if resp.ID != "test-id" {
		t.Fatalf("id: got %q", resp.ID)
	}
	if resp.Model != "gpt-4" {
		t.Fatalf("model: got %q", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason: got %q", resp.Choices[0].FinishReason)
	}
	if resp.Choices[0].Message.Content == nil || *resp.Choices[0].Message.Content != "Hello! How can I help?" {
		t.Fatalf("content: got %v", resp.Choices[0].Message.Content)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 30 {
		t.Fatalf("usage: got %+v", resp.Usage)
	}
}

func TestToChatResponse_ToolCalls(t *testing.T) {
	msg := core.AssistantMessage{
		Role:      core.MessageRoleAssistant,
		Timestamp: time.Now(),
		StopReason: core.StopToolUse,
		Content: []core.ContentBlock{
			core.TextContent{Type: "text", Text: "Let me check the weather."},
			core.ToolCall{
				Type: "toolCall", ID: "tc-1", Name: "get_weather",
				Arguments: json.RawMessage(`{"city":"Beijing"}`),
			},
		},
	}

	resp := ToChatResponse(msg, "gpt-4", "test-id")

	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason: got %q", resp.Choices[0].FinishReason)
	}
	if resp.Choices[0].Message.Content == nil {
		t.Fatal("content should not be nil")
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.Choices[0].Message.ToolCalls))
	}
	tc := resp.Choices[0].Message.ToolCalls[0]
	if tc.ID != "tc-1" || tc.Function.Name != "get_weather" {
		t.Fatalf("tool call: id=%q name=%q", tc.ID, tc.Function.Name)
	}
}

func TestStreamDeltaBuilder(t *testing.T) {
	b := NewStreamDeltaBuilder("gpt-4", "test-id")

	// Simulate streaming events
	events := []struct {
		name string
		evt  core.AssistantMessageEvent
	}{
		{"start", core.EventStart{Type: "start", API: "openai", Provider: "openai", Model: "gpt-4", Timestamp: time.Now()}},
		{"text", core.EventTextDelta{Type: "text_delta", Delta: "Hello"}},
		{"text", core.EventTextDelta{Type: "text_delta", Delta: " world"}},
		{"tool_start", core.EventToolCallStart{Type: "toolcall_start", ContentIndex: 1, ID: "tc-1", Name: "get_weather"}},
		{"tool_delta", core.EventToolCallDelta{Type: "toolcall_delta", ContentIndex: 1, ID: "tc-1", ArgumentsDelta: `{"city":"`}},
		{"tool_delta", core.EventToolCallDelta{Type: "toolcall_delta", ContentIndex: 1, ID: "tc-1", ArgumentsDelta: `Beijing"}`}},
		{"tool_end", core.EventToolCallEnd{Type: "toolcall_end", ContentIndex: 1, ID: "tc-1", Arguments: json.RawMessage(`{"city":"Beijing"}`)}},
		{"done", core.EventDone{Type: "done", Reason: core.StopToolUse, Message: core.AssistantMessage{}}},
	}

	var chunks []*SSEChatChunk
	for _, e := range events {
		chunk := b.OnEvent(e.evt)
		if chunk != nil {
			chunks = append(chunks, chunk)
		}
		t.Logf("event=%s chunk=%v", e.name, chunk != nil)
	}

	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}

	// First chunk should have role
	if chunks[0].Choices[0].Delta.Role != "assistant" {
		t.Fatalf("first chunk role: got %q", chunks[0].Choices[0].Delta.Role)
	}

	// Second chunk should have text content
	if chunks[1].Choices[0].Delta.Content != "Hello" {
		t.Fatalf("second chunk content: got %q", chunks[1].Choices[0].Delta.Content)
	}

	// Check tool call chunk
	var foundTool bool
	for _, c := range chunks {
		for _, choice := range c.Choices {
			if len(choice.Delta.ToolCalls) > 0 {
				foundTool = true
				tc := choice.Delta.ToolCalls[0]
				t.Logf("tool call id=%q name=%q args=%q", tc.ID, tc.Function.Name, tc.Function.Arguments)
			}
		}
	}
	if !foundTool {
		t.Fatal("expected tool call chunk not found")
	}

	// Last chunk should have finish_reason
	last := chunks[len(chunks)-1]
	if last.Choices[0].FinishReason == nil || *last.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("last chunk finish_reason: got %v", last.Choices[0].FinishReason)
	}
}

// ─── HTTP Handler Test ──────────────────────────────────────────────────────

func TestChatHandler_NonStreaming_Faux(t *testing.T) {
	// Use the faux provider for offline testing
	model := core.Model{
		ID: "faux-test", Name: "Faux Test",
		API: "faux", Provider: "openai",
		ContextWindow: 8192, MaxTokens: 4096,
	}

	handler := NewChatHandler(HandlerConfig{
		Model: model,
		SystemPrompt: "You are a helpful assistant.",
		SimpleStreamOptions: core.SimpleStreamOptions{},
	})

	body := map[string]any{
		"model":    "faux-test",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello!"},
		},
		"max_tokens":   50,
		"temperature":  0.5,
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]any
		json.NewDecoder(resp.Body).Decode(&errResp)
		t.Fatalf("status: %d, body: %v", resp.StatusCode, errResp)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(chatResp.Choices) == 0 {
		t.Fatal("no choices in response")
	}
	t.Logf("response: %+v", chatResp)
}

// ─── Integration-style helpers ──────────────────────────────────────────────

// TestChatHandler_MethodCheck tests that non-POST requests return 405.
func TestChatHandler_MethodCheck(t *testing.T) {
	handler := NewChatHandler(HandlerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// TestChatHandler_MissingMessages tests validation.
func TestChatHandler_MissingMessages(t *testing.T) {
	handler := NewChatHandler(HandlerConfig{})

	body := map[string]any{"model": "test", "messages": []any{}}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestSSEChatChunk_Marshal_Empty verifies that a zero-value SSEChatChunk
// marshals correctly and omits empty usage.
func TestSSEChatChunk_Marshal_Empty(t *testing.T) {
	chunk := &SSEChatChunk{
		ID:      "test",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []SSEChoice{{
			Index: 0,
			Delta: SSEDelta{Content: "Hello"},
		}},
	}

	data, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "Hello") {
		t.Fatalf("expected Hello in JSON, got %s", data)
	}
	if strings.Contains(string(data), "usage") {
		t.Fatalf("expected no usage, got %s", data)
	}
	t.Logf("SSE chunk: %s", data)
}

// TestContentPart_Marshal verifies ContentPart serialization.
func TestContentPart_Marshal(t *testing.T) {
	parts := []ChatMessage{
		{
			Role: "user",
			Content: mustJSON([]any{
				map[string]any{"type": "text", "text": "Hello"},
				map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": "data:image/png;base64,abc",
					},
				},
			}),
		},
	}

	var req ChatRequest
	req.Messages = parts
	req.Model = "gpt-4"

	model := core.Model{ID: "gpt-4"}
	ctx, _, err := ParseChatRequest(&req, model)
	if err != nil {
		t.Fatalf("ParseChatRequest: %v", err)
	}
	if len(ctx.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(ctx.Messages))
	}
	t.Logf("converted user message: %+v", ctx.Messages[0])
}

// TestParseDataURL verifies base64 data URL parsing.
func TestParseDataURL(t *testing.T) {
	data, mimeType := parseDataURL("data:image/png;base64,iVBORw0KGgo=")
	if data != "iVBORw0KGgo=" {
		t.Fatalf("data: got %q", data)
	}
	if mimeType != "image/png" {
		t.Fatalf("mime: got %q", mimeType)
	}

	// Non data URL
	data, mimeType = parseDataURL("https://example.com/image.png")
	if data != "" {
		t.Fatalf("expected empty, got %q", data)
	}
}

// ─── Context-aware test ─────────────────────────────────────────────────────

func TestChatRequest_ReasoningEffort(t *testing.T) {
	raw := `{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "Think hard"}],
		"reasoning_effort": "high"
	}`

	var req ChatRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	model := core.Model{ID: "gpt-4"}
	_, opts, err := ParseChatRequest(&req, model)
	if err != nil {
		t.Fatalf("ParseChatRequest: %v", err)
	}

	if opts.Reasoning != core.ThinkingHigh {
		t.Fatalf("reasoning: got %q, want %q", opts.Reasoning, core.ThinkingHigh)
	}
}

// ─── Utilities ──────────────────────────────────────────────────────────────

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// init triggers crux-ai built-in provider registration via crux-ai/providers init().
