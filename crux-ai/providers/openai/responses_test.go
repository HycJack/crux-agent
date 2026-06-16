package openai

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	piai "github.com/hycjack/crux-ai"
)

func TestNewResponses(t *testing.T) {
	p := NewResponses()
	if p == nil {
		t.Error("expected non-nil provider")
	}
}

func TestResponsesProviderImplementsInterface(t *testing.T) {
	var _ = NewResponses()
}

func TestResponsesStreamNoAPIKey(t *testing.T) {
	p := NewResponses()
	model := piai.Model{
		ID:       "gpt-4o",
		Provider: piai.ProviderOpenAI,
	}

	_, err := p.Stream(context.Background(), model, piai.Context{}, piai.StreamOptions{})
	if err == nil {
		t.Error("expected error for missing API key")
	}
}

func TestConvertResponsesMessages(t *testing.T) {
	messages := []piai.Message{
		piai.UserMessage{
			Role:      "user",
			Content:   "Hello",
			Timestamp: time.Now(),
		},
		piai.AssistantMessage{
			Role: "assistant",
			Content: []piai.ContentBlock{
				piai.TextContent{Type: "text", Text: "Hi!"},
			},
			Timestamp: time.Now(),
		},
		piai.ToolResultMessage{
			Role:       "toolResult",
			ToolCallID: "call_123",
			Content: []piai.ContentBlock{
				piai.TextContent{Type: "text", Text: "result"},
			},
			Timestamp: time.Now(),
		},
	}

	result, err := convertResponsesMessages(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("expected 3 items, got %d", len(result))
	}
}

func TestConvertResponsesTools(t *testing.T) {
	tools := []piai.Tool{
		{
			Name:        "test_tool",
			Description: "A test tool",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
	}

	result := convertResponsesTools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if result[0]["type"] != "function" {
		t.Errorf("expected type 'function', got %v", result[0]["type"])
	}
	if result[0]["name"] != "test_tool" {
		t.Errorf("expected name 'test_tool', got %v", result[0]["name"])
	}
}

func TestMapResponseStatus(t *testing.T) {
	tests := []struct {
		input string
		want  piai.StopReason
	}{
		{"completed", piai.StopStop},
		{"incomplete", piai.StopLength},
		{"failed", piai.StopError},
		{"cancelled", piai.StopAborted},
		{"unknown", piai.StopStop},
	}

	for _, tt := range tests {
		got := mapResponseStatus(tt.input)
		if got != tt.want {
			t.Errorf("mapResponseStatus(%s) = %s, want %s", tt.input, got, tt.want)
		}
	}
}
