package compat

import (
	"context"
	"strings"
	"testing"

	core "github.com/hycjack/crux-ai/core"
)

// sseEvent builds a single SSE "data:" payload line for a chunk.
func sseEvent(payload string) string {
	return "data: " + payload + "\n\n"
}

// runProcessSSE feeds raw SSE text through processSSE and returns the final
// AssistantMessage plus the sequence of toolcall events emitted.
func runProcessSSE(t *testing.T, raw string) (core.AssistantMessage, []core.AssistantMessageEvent) {
	t.Helper()
	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()
	cfg := Config{Provider: "test", Path: "/chat/completions"}
	model := core.Model{ID: "m", API: core.APIOpenAICompletions, Provider: "test"}

	msg, err := processSSE(context.Background(), cfg, strings.NewReader(raw), stream, nil, model, core.StreamOptions{})
	if err != nil {
		t.Fatalf("processSSE error: %v", err)
	}
	stream.End(msg)

	var events []core.AssistantMessageEvent
	stream.ForEach(context.Background(), func(ev core.AssistantMessageEvent) error {
		events = append(events, ev)
		return nil
	})
	return msg, events
}

// countToolEvents returns the number of toolcall_start / toolcall_delta /
// toolcall_end events of a given concrete type.
func countToolEvents(events []core.AssistantMessageEvent, target string) int {
	n := 0
	for _, e := range events {
		switch e.(type) {
		case core.EventToolCallStart:
			if target == "start" {
				n++
			}
		case core.EventToolCallDelta:
			if target == "delta" {
				n++
			}
		case core.EventToolCallEnd:
			if target == "end" {
				n++
			}
		}
	}
	return n
}

// TestToolCallRepeatedID verifies that when a provider re-emits the same
// tool_call chunk with a repeated id/name, the accumulated arguments are NOT
// wiped out (the original bug).
func TestToolCallRepeatedID(t *testing.T) {
	raw := "" +
		sseEvent(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_weather","arguments":"{\"city\":"}}]}}]}`) +
		sseEvent(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_weather","arguments":"\"beijing\"}"}}]}}]}`) +
		sseEvent(`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)

	msg, events := runProcessSSE(t, raw)

	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	tc, ok := msg.Content[0].(core.ToolCall)
	if !ok {
		t.Fatalf("expected ToolCall content, got %T", msg.Content[0])
	}
	if tc.ID != "call_1" {
		t.Errorf("expected ID call_1, got %q", tc.ID)
	}
	if tc.Name != "get_weather" {
		t.Errorf("expected Name get_weather, got %q", tc.Name)
	}
	// Both argument fragments must be present, in order.
	if got := string(tc.Arguments); got != `{"city":"beijing"}` {
		t.Errorf("arguments were corrupted: got %q, want %q", got, `{"city":"beijing"}`)
	}
	// The start event must be emitted exactly once even though id repeated.
	if n := countToolEvents(events, "start"); n != 1 {
		t.Errorf("expected exactly 1 toolcall_start, got %d", n)
	}
	if n := countToolEvents(events, "end"); n != 1 {
		t.Errorf("expected exactly 1 toolcall_end, got %d", n)
	}
}

// TestToolCallIDArrivesLate verifies that when the id/name only arrive in a
// later chunk (after argument deltas already started), nothing is lost.
func TestToolCallIDArrivesLate(t *testing.T) {
	raw := "" +
		sseEvent(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":"}}]}}]}`) +
		sseEvent(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_9","function":{"name":"search","arguments":"\"hi\"}"}}]}}]}`)

	msg, _ := runProcessSSE(t, raw)

	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	tc, ok := msg.Content[0].(core.ToolCall)
	if !ok {
		t.Fatalf("expected ToolCall content, got %T", msg.Content[0])
	}
	if tc.ID != "call_9" {
		t.Errorf("expected late ID call_9, got %q", tc.ID)
	}
	if tc.Name != "search" {
		t.Errorf("expected late Name search, got %q", tc.Name)
	}
	if got := string(tc.Arguments); got != `{"q":"hi"}` {
		t.Errorf("arguments lost: got %q, want %q", got, `{"q":"hi"}`)
	}
}

// TestToolCallMultipleParallel verifies independent accumulation across
// multiple parallel tool calls sharing the same stream.
func TestToolCallMultipleParallel(t *testing.T) {
	raw := "" +
		sseEvent(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"f_a","arguments":"{\"x\":"}},{"index":1,"id":"b","function":{"name":"f_b","arguments":"{\"y\":"}}]}}]}`) +
		sseEvent(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}},{"index":1,"function":{"arguments":"2}"}}]}}]}`)

	msg, events := runProcessSSE(t, raw)

	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(msg.Content))
	}
	tc0, _ := msg.Content[0].(core.ToolCall)
	tc1, _ := msg.Content[1].(core.ToolCall)
	if got := string(tc0.Arguments); got != `{"x":1}` {
		t.Errorf("tc0 arguments wrong: got %q, want %q", got, `{"x":1}`)
	}
	if got := string(tc1.Arguments); got != `{"y":2}` {
		t.Errorf("tc1 arguments wrong: got %q, want %q", got, `{"y":2}`)
	}
	if n := countToolEvents(events, "end"); n != 2 {
		t.Errorf("expected 2 toolcall_end events, got %d", n)
	}
}
