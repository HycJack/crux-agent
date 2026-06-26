package anthropic

import (
	"encoding/json"
	"testing"
	"time"

	piai "github.com/hycjack/crux-ai"
)

func TestConvertMessagesUserText(t *testing.T) {
	messages := []piai.Message{
		piai.UserMessage{
			Role:      "user",
			Content:   "Hello, world!",
			Timestamp: time.Now(),
		},
	}

	result, err := convertMessages(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0]["role"] != "user" {
		t.Errorf("expected role 'user', got %v", result[0]["role"])
	}
	if result[0]["content"] != "Hello, world!" {
		t.Errorf("expected content 'Hello, world!', got %v", result[0]["content"])
	}
}

func TestConvertMessagesUserContentBlocks(t *testing.T) {
	messages := []piai.Message{
		piai.UserMessage{
			Role: "user",
			Content: []piai.ContentBlock{
				piai.TextContent{Type: "text", Text: "describe this"},
				piai.ImageContent{Type: "image", Data: "base64data", MimeType: "image/png"},
			},
			Timestamp: time.Now(),
		},
	}

	result, err := convertMessages(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, ok := result[0]["content"].([]any)
	if !ok {
		t.Fatal("expected content to be a slice")
	}
	if len(content) != 2 {
		t.Errorf("expected 2 content blocks, got %d", len(content))
	}
}

func TestConvertMessagesAssistant(t *testing.T) {
	messages := []piai.Message{
		piai.AssistantMessage{
			Role: "assistant",
			Content: []piai.ContentBlock{
				piai.TextContent{Type: "text", Text: "Hello!"},
			},
			Timestamp: time.Now(),
		},
	}

	result, err := convertMessages(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0]["role"] != "assistant" {
		t.Errorf("expected role 'assistant', got %v", result[0]["role"])
	}
}

func TestConvertMessagesToolResult(t *testing.T) {
	messages := []piai.Message{
		piai.ToolResultMessage{
			Role:       "toolResult",
			ToolCallID: "call_123",
			ToolName:   "get_weather",
			Content: []piai.ContentBlock{
				piai.TextContent{Type: "text", Text: "sunny"},
			},
			IsError:   false,
			Timestamp: time.Now(),
		},
	}

	result, err := convertMessages(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result[0]["role"] != "user" {
		t.Errorf("expected role 'user', got %v", result[0]["role"])
	}

	content, ok := result[0]["content"].([]any)
	if !ok {
		t.Fatal("expected content to be a slice")
	}

	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatal("expected content block")
	}
	if block["type"] != "tool_result" {
		t.Errorf("expected type 'tool_result', got %v", block["type"])
	}
	if block["tool_use_id"] != "call_123" {
		t.Errorf("expected tool_use_id 'call_123', got %v", block["tool_use_id"])
	}
}

func TestConvertTools(t *testing.T) {
	tools := []piai.Tool{
		{
			Name:        "get_weather",
			Description: "Get weather for a location",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`),
		},
	}

	result := convertTools(tools, false)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if result[0]["name"] != "get_weather" {
		t.Errorf("expected name 'get_weather', got %v", result[0]["name"])
	}
}

func TestConvertToolsWithEagerStreaming(t *testing.T) {
	tools := []piai.Tool{
		{Name: "test", Description: "test tool"},
	}

	result := convertTools(tools, true)
	if result[0]["eager_input_streaming"] != true {
		t.Error("expected eager_input_streaming to be true")
	}
}

func TestMapStopReason(t *testing.T) {
	tests := []struct {
		input string
		want  piai.StopReason
	}{
		{"end_turn", piai.StopStop},
		{"stop_sequence", piai.StopStop},
		{"max_tokens", piai.StopLength},
		{"tool_use", piai.StopToolUse},
		{"unknown", piai.StopStop},
	}

	for _, tt := range tests {
		got := mapStopReason(tt.input)
		if got != tt.want {
			t.Errorf("mapStopReason(%s) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestBuildRequestBodyBasic(t *testing.T) {
	model := piai.Model{
		ID:        "claude-3-opus",
		MaxTokens: 4096,
	}
	ctx := piai.Context{
		SystemPrompt: "You are helpful.",
		Messages: []piai.Message{
			piai.UserMessage{
				Role:      "user",
				Content:   "Hello",
				Timestamp: time.Now(),
			},
		},
	}

	body, err := buildRequestBody(model, ctx, piai.StreamOptions{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body["model"] != "claude-3-opus" {
		t.Errorf("expected model 'claude-3-opus', got %v", body["model"])
	}
	if body["stream"] != true {
		t.Error("expected stream to be true")
	}
	if body["max_tokens"] != 4096 {
		t.Errorf("expected max_tokens 4096, got %v", body["max_tokens"])
	}
	if body["system"] != "You are helpful." {
		t.Errorf("expected system prompt, got %v", body["system"])
	}
}

func TestBuildRequestBodyWithThinking(t *testing.T) {
	model := piai.Model{ID: "claude-3-opus", MaxTokens: 4096}
	ctx := piai.Context{
		Messages: []piai.Message{
			piai.UserMessage{Role: "user", Content: "Hello", Timestamp: time.Now()},
		},
	}

	body, err := buildRequestBody(model, ctx, piai.StreamOptions{}, Options{
		ThinkingEnabled:     true,
		ThinkingBudgetTokens: 10000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatal("expected thinking config")
	}
	if thinking["type"] != "enabled" {
		t.Errorf("expected type 'enabled', got %v", thinking["type"])
	}
	if thinking["budget_tokens"] != 10000 {
		t.Errorf("expected budget_tokens 10000, got %v", thinking["budget_tokens"])
	}
}

func TestBuildRequestBodyWithTools(t *testing.T) {
	model := piai.Model{ID: "claude-3-opus", MaxTokens: 4096}
	ctx := piai.Context{
		Messages: []piai.Message{
			piai.UserMessage{Role: "user", Content: "Hello", Timestamp: time.Now()},
		},
		Tools: []piai.Tool{
			{
				Name:       "test_tool",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		},
	}

	body, err := buildRequestBody(model, ctx, piai.StreamOptions{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tools, ok := body["tools"].([]map[string]any)
	if !ok {
		t.Fatal("expected tools")
	}
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}
}
