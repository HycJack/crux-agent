package core

import (
	"encoding/json"
	"testing"
	"time"
)

func TestModelJSON(t *testing.T) {
	m := Model{
		ID:        "claude-3-opus",
		Name:      "Claude 3 Opus",
		API:       APIAnthropicMessages,
		Provider:  ProviderAnthropic,
		Reasoning: true,
		Input:     []Modality{ModalityText, ModalityImage},
		Cost:      Cost{Input: 15, Output: 75},
		ContextWindow: 200000,
		MaxTokens:     4096,
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("failed to marshal model: %v", err)
	}

	var m2 Model
	if err := json.Unmarshal(data, &m2); err != nil {
		t.Fatalf("failed to unmarshal model: %v", err)
	}

	if m2.ID != m.ID {
		t.Errorf("ID mismatch: got %s, want %s", m2.ID, m.ID)
	}
	if m2.API != m.API {
		t.Errorf("API mismatch: got %s, want %s", m2.API, m.API)
	}
	if m2.Cost.Input != 15 {
		t.Errorf("Cost.Input mismatch: got %f, want 15", m2.Cost.Input)
	}
}

func TestUserMessageTag(t *testing.T) {
	msg := UserMessage{
		Role:      "user",
		Content:   "hello",
		Timestamp: time.Now(),
	}

	// Verify it implements Message interface
	var _ Message = msg

	if msg.GetTimestamp().IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestAssistantMessageTag(t *testing.T) {
	msg := AssistantMessage{
		Role:    "assistant",
		Content: []ContentBlock{TextContent{Type: "text", Text: "hi"}},
		API:     APIAnthropicMessages,
	}

	var _ Message = msg

	if msg.GetTimestamp().IsZero() {
		msg.Timestamp = time.Now()
	}
	if msg.GetTimestamp().IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestContentBlocks(t *testing.T) {
	// Verify all content blocks implement ContentBlock
	var _ ContentBlock = TextContent{}
	var _ ContentBlock = ThinkingContent{}
	var _ ContentBlock = ImageContent{}
	var _ ContentBlock = ToolCall{}
}

func TestEvents(t *testing.T) {
	// Verify all events implement AssistantMessageEvent
	var _ AssistantMessageEvent = EventStart{}
	var _ AssistantMessageEvent = EventTextStart{}
	var _ AssistantMessageEvent = EventTextDelta{}
	var _ AssistantMessageEvent = EventTextEnd{}
	var _ AssistantMessageEvent = EventThinkingStart{}
	var _ AssistantMessageEvent = EventThinkingDelta{}
	var _ AssistantMessageEvent = EventThinkingEnd{}
	var _ AssistantMessageEvent = EventToolCallStart{}
	var _ AssistantMessageEvent = EventToolCallDelta{}
	var _ AssistantMessageEvent = EventToolCallEnd{}
	var _ AssistantMessageEvent = EventDone{}
	var _ AssistantMessageEvent = EventError{}
}

func TestStopReasonConstants(t *testing.T) {
	tests := []struct {
		reason StopReason
		want   string
	}{
		{StopStop, "stop"},
		{StopLength, "length"},
		{StopToolUse, "toolUse"},
		{StopError, "error"},
		{StopAborted, "aborted"},
	}

	for _, tt := range tests {
		if string(tt.reason) != tt.want {
			t.Errorf("StopReason %v: got %s, want %s", tt.reason, string(tt.reason), tt.want)
		}
	}
}

func TestThinkingLevelConstants(t *testing.T) {
	levels := []ThinkingLevel{
		ThinkingMinimal,
		ThinkingLow,
		ThinkingMedium,
		ThinkingHigh,
		ThinkingXHigh,
	}

	for _, level := range levels {
		if level == "" {
			t.Error("empty ThinkingLevel constant")
		}
	}
}
