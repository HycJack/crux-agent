package openai

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
			Content:   "Hello",
			Timestamp: time.Now(),
		},
	}

	result, err := ConvertMessages(messages, piai.Model{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0]["role"] != "user" {
		t.Errorf("expected role 'user', got %v", result[0]["role"])
	}
}

func TestConvertMessagesAssistant(t *testing.T) {
	messages := []piai.Message{
		piai.AssistantMessage{
			Role: "assistant",
			Content: []piai.ContentBlock{
				piai.TextContent{Type: "text", Text: "Hi!"},
			},
			Timestamp: time.Now(),
		},
	}

	result, err := ConvertMessages(messages, piai.Model{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0]["role"] != "assistant" {
		t.Errorf("expected role 'assistant', got %v", result[0]["role"])
	}
	if result[0]["content"] != "Hi!" {
		t.Errorf("expected content 'Hi!', got %v", result[0]["content"])
	}
}

func TestConvertMessagesToolResult(t *testing.T) {
	messages := []piai.Message{
		piai.ToolResultMessage{
			Role:       "toolResult",
			ToolCallID: "call_123",
			Content: []piai.ContentBlock{
				piai.TextContent{Type: "text", Text: "result"},
			},
			Timestamp: time.Now(),
		},
	}

	result, err := ConvertMessages(messages, piai.Model{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0]["role"] != "tool" {
		t.Errorf("expected role 'tool', got %v", result[0]["role"])
	}
	if result[0]["tool_call_id"] != "call_123" {
		t.Errorf("expected tool_call_id 'call_123', got %v", result[0]["tool_call_id"])
	}
}

func TestConvertTools(t *testing.T) {
	tools := []piai.Tool{
		{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`),
		},
	}

	result := ConvertTools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if result[0]["type"] != "function" {
		t.Errorf("expected type 'function', got %v", result[0]["type"])
	}

	fn, ok := result[0]["function"].(map[string]any)
	if !ok {
		t.Fatal("expected function field")
	}
	if fn["name"] != "get_weather" {
		t.Errorf("expected name 'get_weather', got %v", fn["name"])
	}
}

func TestMapStopReason(t *testing.T) {
	tests := []struct {
		input string
		want  piai.StopReason
	}{
		{"stop", piai.StopStop},
		{"length", piai.StopLength},
		{"tool_calls", piai.StopToolUse},
		{"function_call", piai.StopToolUse},
		{"unknown", piai.StopStop},
	}

	for _, tt := range tests {
		got := MapStopReason(tt.input)
		if got != tt.want {
			t.Errorf("MapStopReason(%s) = %s, want %s", tt.input, got, tt.want)
		}
	}
}
