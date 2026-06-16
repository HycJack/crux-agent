package google

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

	result, err := ConvertMessages(messages)
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

	result, err := ConvertMessages(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0]["role"] != "model" {
		t.Errorf("expected role 'model', got %v", result[0]["role"])
	}
}

func TestConvertMessagesToolResult(t *testing.T) {
	messages := []piai.Message{
		piai.ToolResultMessage{
			Role:       "toolResult",
			ToolCallID: "call_123",
			ToolName:   "get_weather",
			Content: []piai.ContentBlock{
				piai.TextContent{Type: "text", Text: `{"temp":72}`},
			},
			Timestamp: time.Now(),
		},
	}

	result, err := ConvertMessages(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0]["role"] != "user" {
		t.Errorf("expected role 'user', got %v", result[0]["role"])
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
		t.Fatalf("expected 1 tool config, got %d", len(result))
	}

	toolConfig := result[0]
	declarations, ok := toolConfig["functionDeclarations"].([]map[string]any)
	if !ok {
		t.Fatal("expected functionDeclarations")
	}
	if len(declarations) != 1 {
		t.Errorf("expected 1 declaration, got %d", len(declarations))
	}
	if declarations[0]["name"] != "get_weather" {
		t.Errorf("expected name 'get_weather', got %v", declarations[0]["name"])
	}
}

func TestMapStopReason(t *testing.T) {
	tests := []struct {
		input string
		want  piai.StopReason
	}{
		{"STOP", piai.StopStop},
		{"MAX_TOKENS", piai.StopLength},
		{"SAFETY", piai.StopError},
		{"RECITATION", piai.StopError},
		{"OTHER", piai.StopError},
		{"unknown", piai.StopStop},
	}

	for _, tt := range tests {
		got := MapStopReason(tt.input)
		if got != tt.want {
			t.Errorf("MapStopReason(%s) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestIsThinkingPart(t *testing.T) {
	if !IsThinkingPart(map[string]any{"thought": true}) {
		t.Error("expected true for thought=true")
	}
	if IsThinkingPart(map[string]any{"thought": false}) {
		t.Error("expected false for thought=false")
	}
	if IsThinkingPart(map[string]any{}) {
		t.Error("expected false for missing thought field")
	}
}

func TestMapThinkingLevel(t *testing.T) {
	tests := []struct {
		input piai.ThinkingLevel
		want  string
	}{
		{piai.ThinkingMinimal, "MINIMAL"},
		{piai.ThinkingLow, "LOW"},
		{piai.ThinkingMedium, "MEDIUM"},
		{piai.ThinkingHigh, "HIGH"},
		{piai.ThinkingXHigh, "HIGH"},
	}

	for _, tt := range tests {
		got := mapThinkingLevel(tt.input)
		if got != tt.want {
			t.Errorf("mapThinkingLevel(%s) = %s, want %s", tt.input, got, tt.want)
		}
	}
}
