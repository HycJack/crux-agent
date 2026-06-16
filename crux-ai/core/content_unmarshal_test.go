package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAssistantMessageUnmarshalJSON(t *testing.T) {
	raw := `{
		"role": "assistant",
		"content": [
			{"type": "text", "text": "你好，我是小七"},
			{"type": "thinking", "thinking": "用户给我起名字了"},
			{"type": "tool_use", "id": "1", "name": "bash", "arguments": {"cmd": "ls"}}
		],
		"stopReason": "stop"
	}`

	var m AssistantMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(m.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(m.Content))
	}

	// TextContent
	tc, ok := m.Content[0].(TextContent)
	if !ok {
		t.Fatalf("block 0 type = %T, want TextContent", m.Content[0])
	}
	if tc.Text != "你好，我是小七" {
		t.Errorf("text = %q, want %q", tc.Text, "你好，我是小七")
	}

	// ThinkingContent
	th, ok := m.Content[1].(ThinkingContent)
	if !ok {
		t.Fatalf("block 1 type = %T, want ThinkingContent", m.Content[1])
	}
	if th.Thinking != "用户给我起名字了" {
		t.Errorf("thinking = %q", th.Thinking)
	}

	// ToolCall
	tool, ok := m.Content[2].(ToolCall)
	if !ok {
		t.Fatalf("block 2 type = %T, want ToolCall", m.Content[2])
	}
	if tool.Name != "bash" {
		t.Errorf("tool name = %q", tool.Name)
	}
}

func TestAssistantMessageUnmarshalRoundTrip(t *testing.T) {
	original := AssistantMessage{
		Role: "assistant",
		Content: []ContentBlock{
			TextContent{Type: "text", Text: "hello"},
			ThinkingContent{Type: "thinking", Thinking: "deep thought"},
		},
		StopReason: StopStop,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var loaded AssistantMessage
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}

	if len(loaded.Content) != 2 {
		t.Fatalf("round-trip: expected 2 blocks, got %d", len(loaded.Content))
	}
	if tc, ok := loaded.Content[0].(TextContent); !ok || tc.Text != "hello" {
		t.Errorf("round-trip block 0 = %v", loaded.Content[0])
	}
	if tc, ok := loaded.Content[1].(ThinkingContent); !ok || tc.Thinking != "deep thought" {
		t.Errorf("round-trip block 1 = %v", loaded.Content[1])
	}
}

func TestAssistantMessageUnmarshalEmptyContent(t *testing.T) {
	raw := `{"role":"assistant","content":[],"stopReason":"stop"}`
	var m AssistantMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Content) != 0 {
		t.Errorf("expected empty content, got %d blocks", len(m.Content))
	}
}

func TestAssistantMessageUnmarshalNilContent(t *testing.T) {
	raw := `{"role":"assistant","stopReason":"stop"}`
	var m AssistantMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m.Content != nil {
		t.Errorf("expected nil content, got %v", m.Content)
	}
}

func TestToolResultMessageUnmarshal(t *testing.T) {
	raw := `{"role":"toolResult","toolCallId":"1","toolName":"bash","content":[{"type":"text","text":"hello"}]}`
	var m ToolResultMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.Content))
	}
	if tc, ok := m.Content[0].(TextContent); !ok || tc.Text != "hello" {
		t.Errorf("content = %v", m.Content[0])
	}
	if m.ToolName != "bash" {
		t.Errorf("tool name = %q", m.ToolName)
	}
}

func TestUnmarshalContentBlocksMalformed(t *testing.T) {
	// 未知 type 不报错，降级为 TextContent
	data := `[{"type":"weird","foo":"bar"},{"type":"text","text":"hi"}]`
	blocks, err := UnmarshalContentBlocks([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if _, ok := blocks[0].(TextContent); !ok {
		t.Errorf("block 0 should be TextContent (fallback)")
	}
	if tc, ok := blocks[1].(TextContent); !ok || tc.Text != "hi" {
		t.Errorf("block 1 wrong: %v", blocks[1])
	}
	if !strings.Contains(blocks[0].(TextContent).Text, "weird") {
		t.Errorf("fallback should contain raw data")
	}
}