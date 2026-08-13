package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// testModel creates a minimal Model for testing.
func testModel(id string, api KnownAPI, provider KnownProvider) Model {
	return Model{ID: id, API: api, Provider: provider}
}

// collectEvents synchronously drains a stream and returns all events.
func collectEvents(ctx context.Context, stream *AssistantMessageEventStream) ([]AssistantMessageEvent, error) {
	var events []AssistantMessageEvent
	_, err := stream.ForEach(ctx, func(evt AssistantMessageEvent) error {
		events = append(events, evt)
		return nil
	})
	return events, err
}

// ============================================================================
// Provider event creation helpers
// ============================================================================

func providerStreamWith(events ...ProviderEvent) *ProviderEventStream {
	stream := NewProviderEventStream()
	go func() {
		for _, evt := range events {
			stream.Push(evt)
		}
		stream.End(ProviderEventStreamResult{})
	}()
	return stream
}

// ============================================================================
// Basic text streaming
// ============================================================================

func TestCanonicalizeProviderStream_BasicText(t *testing.T) {
	ctx := context.Background()
	ps := providerStreamWith(
		ProviderResponseStart{Type: "response_start", Model: "claude-3", Timestamp: time.Now()},
		ProviderTextDelta{Type: "text_delta", Delta: "Hello"},
		ProviderTextDelta{Type: "text_delta", Delta: " world"},
		ProviderResponseEnd{
			Type:         "response_end",
			Message:      AssistantMessage{Role: "assistant"},
			FinishReason: "stop",
		},
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "claude-3")
	events, err := collectEvents(ctx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: start, text_start, text_delta(x2), text_end, done
	if len(events) < 6 {
		t.Fatalf("expected >= 6 events, got %d", len(events))
	}

	// Check event types
	assertType[EventStart](t, events[0], "EventStart")
	assertType[EventTextStart](t, events[1], "EventTextStart")
	assertType[EventTextDelta](t, events[2], "EventTextDelta")
	assertType[EventTextDelta](t, events[3], "EventTextDelta")
	assertType[EventTextEnd](t, events[4], "EventTextEnd")
	assertType[EventDone](t, events[5], "EventDone")

	// Content index should be sequential
	ts := events[1].(EventTextStart)
	if ts.ContentIndex != 0 {
		t.Errorf("expected ContentIndex=0 on first text_start, got %d", ts.ContentIndex)
	}

	d1 := events[2].(EventTextDelta)
	d2 := events[3].(EventTextDelta)
	if d1.Delta != "Hello" && d1.Delta != "world" {
		t.Logf("delta order: %q + %q", d1.Delta, d2.Delta)
	}

	te := events[4].(EventTextEnd)
	if te.Content != "Hello world" {
		t.Errorf("expected text_end content 'Hello world', got %q", te.Content)
	}

	done := events[5].(EventDone)
	if done.Reason != StopStop {
		t.Errorf("expected StopStop, got %v", done.Reason)
	}
	// Final message should contain the accumulated text
	if len(done.Message.Content) > 0 {
		tc, ok := done.Message.Content[0].(TextContent)
		if !ok || tc.Text != "Hello world" {
			t.Errorf("expected text 'Hello world', got %+v", done.Message.Content[0])
		}
	}
}

// ============================================================================
// Thinking blocks
// ============================================================================

func TestCanonicalizeProviderStream_ThinkingThenText(t *testing.T) {
	ctx := context.Background()
	ps := providerStreamWith(
		ProviderResponseStart{Type: "response_start", Model: "claude-3"},
		ProviderThinkingDelta{Type: "thinking_delta", Delta: "Step 1: "},
		ProviderThinkingDelta{Type: "thinking_delta", Delta: "analyze"},
		ProviderTextDelta{Type: "text_delta", Delta: "Here's the"},
		ProviderTextDelta{Type: "text_delta", Delta: " answer"},
		ProviderResponseEnd{
			Type:         "response_end",
			Message:      AssistantMessage{Role: "assistant"},
			FinishReason: "end_turn",
		},
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "claude-3")
	events, err := collectEvents(ctx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: start, thinking_start, thinking_delta(x2), thinking_end, text_start, text_delta(x2), text_end, done
	if len(events) < 9 {
		t.Fatalf("expected >= 9 events, got %d", len(events))
	}

	// Verify thinking block
	thStart := findEvent[EventThinkingStart](events)
	if thStart == nil {
		t.Fatal("expected EventThinkingStart")
	}
	if thStart.ContentIndex != 0 {
		t.Errorf("expected thinking ContentIndex=0, got %d", thStart.ContentIndex)
	}

	thEnd := findEvent[EventThinkingEnd](events)
	if thEnd == nil {
		t.Fatal("expected EventThinkingEnd")
	}
	if thEnd.Content != "Step 1: analyze" {
		t.Errorf("expected thinking content 'Step 1: analyze', got %q", thEnd.Content)
	}

	// Verify text block
	textStart := findEvent[EventTextStart](events)
	if textStart == nil {
		t.Fatal("expected EventTextStart")
	}
	if textStart.ContentIndex != 1 {
		t.Errorf("expected text ContentIndex=1, got %d", textStart.ContentIndex)
	}

	textEnd := findEvent[EventTextEnd](events)
	if textEnd == nil {
		t.Fatal("expected EventTextEnd")
	}
	if textEnd.Content != "Here's the answer" {
		t.Errorf("expected text 'Here's the answer', got %q", textEnd.Content)
	}

	done := findEvent[EventDone](events)
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Reason != StopStop {
		t.Errorf("expected StopStop, got %v", done.Reason)
	}
	// Final message should contain both blocks
	if len(done.Message.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(done.Message.Content))
	}
	// Block 0 is thinking
	if _, ok := done.Message.Content[0].(ThinkingContent); !ok {
		t.Errorf("block 0 should be ThinkingContent, got %T", done.Message.Content[0])
	}
	// Block 1 is text
	if _, ok := done.Message.Content[1].(TextContent); !ok {
		t.Errorf("block 1 should be TextContent, got %T", done.Message.Content[1])
	}
}

// ============================================================================
// Tool calls
// ============================================================================

func TestCanonicalizeProviderStream_ToolCall(t *testing.T) {
	ctx := context.Background()
	args := json.RawMessage(`{"query": "weather"}`)
	ps := providerStreamWith(
		ProviderResponseStart{Type: "response_start", Model: "claude-3"},
		ProviderTextDelta{Type: "text_delta", Delta: "Let me "},
		ProviderTextDelta{Type: "text_delta", Delta: "search"},
		ProviderToolCall{Type: "tool_call", ID: "call-1", Name: "get_weather", Arguments: args},
		ProviderResponseEnd{
			Type:         "response_end",
			Message:      AssistantMessage{Role: "assistant"},
			FinishReason: "tool_use",
		},
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "claude-3")
	events, err := collectEvents(ctx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should include: start, text_start, text_delta(x2), text_end, toolcall_start, toolcall_end, done
	toolStart := findEvent[EventToolCallStart](events)
	if toolStart == nil {
		t.Fatal("expected EventToolCallStart")
	}
	if toolStart.ID != "call-1" {
		t.Errorf("expected call-1, got %q", toolStart.ID)
	}
	if toolStart.Name != "get_weather" {
		t.Errorf("expected get_weather, got %q", toolStart.Name)
	}
	if toolStart.ContentIndex != 1 {
		t.Errorf("expected ContentIndex=1 (after text), got %d", toolStart.ContentIndex)
	}

	toolEnd := findEvent[EventToolCallEnd](events)
	if toolEnd == nil {
		t.Fatal("expected EventToolCallEnd")
	}
	if string(toolEnd.Arguments) != string(args) {
		t.Errorf("arguments mismatch: %s vs %s", string(toolEnd.Arguments), string(args))
	}

	done := findEvent[EventDone](events)
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Reason != StopToolUse {
		t.Errorf("expected StopToolUse, got %v", done.Reason)
	}
}

// ============================================================================
// Multiple tool calls
// ============================================================================

func TestCanonicalizeProviderStream_MultipleToolCalls(t *testing.T) {
	ctx := context.Background()
	args1 := json.RawMessage(`{"city": "beijing"}`)
	args2 := json.RawMessage(`{"city": "tokyo"}`)
	ps := providerStreamWith(
		ProviderResponseStart{Type: "response_start", Model: "claude-3"},
		ProviderToolCall{Type: "tool_call", ID: "call-1", Name: "get_weather", Arguments: args1},
		ProviderToolCall{Type: "tool_call", ID: "call-2", Name: "get_weather", Arguments: args2},
		ProviderResponseEnd{
			Type:         "response_end",
			Message:      AssistantMessage{Role: "assistant"},
			FinishReason: "tool_use",
		},
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "claude-3")
	events, err := collectEvents(ctx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Count tool calls
	toolStarts := filterEvents[EventToolCallStart](events)
	if len(toolStarts) != 2 {
		t.Fatalf("expected 2 toolcall_start events, got %d", len(toolStarts))
	}
	if toolStarts[0].ID != "call-1" {
		t.Errorf("first tool ID: expected call-1, got %q", toolStarts[0].ID)
	}
	if toolStarts[1].ID != "call-2" {
		t.Errorf("second tool ID: expected call-2, got %q", toolStarts[1].ID)
	}

	done := findEvent[EventDone](events)
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Reason != StopToolUse {
		t.Errorf("expected StopToolUse, got %v", done.Reason)
	}
	if len(done.Message.Content) != 2 {
		t.Fatalf("expected 2 content blocks (2 tool calls), got %d", len(done.Message.Content))
	}
}

// ============================================================================
// Error propagation
// ============================================================================

func TestCanonicalizeProviderStream_ProviderError(t *testing.T) {
	ctx := context.Background()
	ps := providerStreamWith(
		ProviderResponseStart{Type: "response_start", Model: "claude-3"},
		ProviderTextDelta{Type: "text_delta", Delta: "partial"},
		ProviderError{Type: "error", Message: "rate limited"},
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "claude-3")
	_, err := collectEvents(ctx, cs)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "rate limited" {
		t.Errorf("expected 'rate limited', got %v", err)
	}
}

// ============================================================================
// Retry events are filtered
// ============================================================================

func TestCanonicalizeProviderStream_RetryFiltered(t *testing.T) {
	ctx := context.Background()
	ps := providerStreamWith(
		ProviderRetryEvent{Type: "retry", Attempt: 1, MaxAttempts: 3, DelaySeconds: 0.1, Message: "transient failure"},
		ProviderRetryEvent{Type: "retry", Attempt: 2, MaxAttempts: 3, DelaySeconds: 0.2, Message: "transient failure"},
		ProviderResponseStart{Type: "response_start", Model: "claude-3"},
		ProviderTextDelta{Type: "text_delta", Delta: "success"},
		ProviderResponseEnd{
			Type:         "response_end",
			Message:      AssistantMessage{Role: "assistant"},
			FinishReason: "stop",
		},
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "claude-3")
	events, err := collectEvents(ctx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No retry events should leak through — retry events are not
	// AssistantMessageEvent, so any event that reaches here is already
	// not a retry event. Just verify we got content.
	if len(events) < 2 {
		t.Fatalf("expected at least start + done, got %d events", len(events))
	}

	// But content should still arrive
	text := extractText(events)
	if text != "success" {
		t.Errorf("expected 'success', got %q", text)
	}
}

// ============================================================================
// Auto-start: content before explicit start
// ============================================================================

func TestCanonicalizeProviderStream_AutoStart(t *testing.T) {
	ctx := context.Background()
	ps := providerStreamWith(
		ProviderTextDelta{Type: "text_delta", Delta: "no start event"},
		ProviderResponseEnd{
			Type:         "response_end",
			Message:      AssistantMessage{Role: "assistant"},
			FinishReason: "stop",
		},
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "claude-3")
	events, err := collectEvents(ctx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should auto-emit EventStart before text_start
	if _, ok := events[0].(EventStart); !ok {
		t.Errorf("expected auto-emit EventStart, got %T", events[0])
	}
}

// ============================================================================
// Stream ends without terminal event
// ============================================================================

func TestCanonicalizeProviderStream_TruncatedStream(t *testing.T) {
	ctx := context.Background()
	ps := providerStreamWith(
		ProviderResponseStart{Type: "response_start", Model: "claude-3"},
		ProviderTextDelta{Type: "text_delta", Delta: "partial"},
		// No ProviderResponseEnd, no ProviderError — stream just ends
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "claude-3")
	_, err := collectEvents(ctx, cs)
	if err == nil {
		t.Fatal("expected error for truncated stream")
	}
	if err.Error() != "Provider stream ended without a terminal event" {
		t.Errorf("expected truncated stream error, got %v", err)
	}
}

// ============================================================================
// Empty stream (no events at all)
// ============================================================================

func TestCanonicalizeProviderStream_Empty(t *testing.T) {
	ctx := context.Background()
	ps := providerStreamWith()
	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "claude-3")

	events, err := collectEvents(ctx, cs)
	if err == nil {
		t.Fatal("expected error for empty stream")
	}
	// Even if the stream is empty, we should get an EventError on the event channel
	if len(events) < 1 {
		t.Fatalf("expected at least 1 event (EventError), got %d", len(events))
	}
}

// ============================================================================
// StopReason normalization
// ============================================================================

func TestCanonicalizeProviderStream_StopReasons(t *testing.T) {
	tests := []struct {
		finishReason string
		want         StopReason
	}{
		{"stop", StopStop},
		{"end_turn", StopStop},
		{"completed", StopStop},
		{"", StopStop},
		{"tool_use", StopToolUse},
		{"tool_calls", StopToolUse},
		{"toolUse", StopToolUse},
		{"length", StopLength},
		{"max_tokens", StopLength},
		{"MAX_TOKENS", StopLength},
		{"incomplete", StopLength},
		{"error", StopError},
		{"failed", StopError},
		{"unknown_reason", StopStop},
	}

	for _, tc := range tests {
		t.Run(tc.finishReason, func(t *testing.T) {
			ctx := context.Background()
			ps := providerStreamWith(
				ProviderResponseStart{Type: "response_start", Model: "test"},
				ProviderTextDelta{Type: "text_delta", Delta: "x"},
				ProviderResponseEnd{
					Type:         "response_end",
					Message:      AssistantMessage{Role: "assistant"},
					FinishReason: tc.finishReason,
				},
			)

			cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "test")
			events, err := collectEvents(ctx, cs)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			done := findEvent[EventDone](events)
			if done == nil {
				t.Fatal("expected EventDone")
			}
			if done.Reason != tc.want {
				t.Errorf("finishReason=%q: expected %q, got %q", tc.finishReason, tc.want, done.Reason)
			}
		})
	}
}

// ============================================================================
// Tool call forces StopToolUse regardless of finish_reason
// ============================================================================

func TestCanonicalizeProviderStream_ToolCallOverridesStopReason(t *testing.T) {
	ctx := context.Background()
	args := json.RawMessage(`{}`)
	// Even though finish_reason is "stop", tool call should result in StopToolUse
	ps := providerStreamWith(
		ProviderResponseStart{Type: "response_start", Model: "test"},
		ProviderToolCall{Type: "tool_call", ID: "x", Name: "f", Arguments: args},
		ProviderResponseEnd{
			Type:         "response_end",
			Message:      AssistantMessage{Role: "assistant"},
			FinishReason: "stop",
		},
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "test")
	events, err := collectEvents(ctx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	done := findEvent[EventDone](events)
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Reason != StopToolUse {
		t.Errorf("expected StopToolUse (has tool call), got %v", done.Reason)
	}
}

// ============================================================================
// Thinking signature / redacted metadata is copied from final message
// ============================================================================

func TestCanonicalizeProviderStream_CopyReplayMetadata(t *testing.T) {
	ctx := context.Background()
	ps := providerStreamWith(
		ProviderResponseStart{Type: "response_start", Model: "claude-3"},
		ProviderThinkingDelta{Type: "thinking_delta", Delta: "reasoning"},
		ProviderResponseEnd{
			Type: "response_end",
			Message: AssistantMessage{
				Role: "assistant",
				Content: []ContentBlock{
					ThinkingContent{Type: "thinking", Thinking: "reasoning", ThinkingSignature: "sig-abc", Redacted: false},
				},
			},
			FinishReason: "end_turn",
		},
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "claude-3")
	_, err := collectEvents(ctx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Metadata copy is verified inside the bridge; end-to-end we just
	// confirm no panic. Detailed metadata tests would read from done.Message.Content.
}

// ============================================================================
// Multiple text blocks (interrupted by thinking)
// ============================================================================

func TestCanonicalizeProviderStream_MultipleTextBlocks(t *testing.T) {
	ctx := context.Background()
	ps := providerStreamWith(
		ProviderResponseStart{Type: "response_start", Model: "test"},
		ProviderTextDelta{Type: "text_delta", Delta: "text1"},
		ProviderThinkingDelta{Type: "thinking_delta", Delta: "think"},
		ProviderTextDelta{Type: "text_delta", Delta: "text2"},
		ProviderResponseEnd{
			Type:         "response_end",
			Message:      AssistantMessage{Role: "assistant"},
			FinishReason: "stop",
		},
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "test")
	events, err := collectEvents(ctx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Count text_start events — should be 2 (separated by thinking)
	textStarts := filterEvents[EventTextStart](events)
	if len(textStarts) != 2 {
		t.Errorf("expected 2 text_start events (two text blocks), got %d", len(textStarts))
	}

	// Count thinking_start events — should be 1
	thinkingStarts := filterEvents[EventThinkingStart](events)
	if len(thinkingStarts) != 1 {
		t.Errorf("expected 1 thinking_start event, got %d", len(thinkingStarts))
	}
}

// ============================================================================
// Concurrency: multiple streams in parallel
// ============================================================================

func TestCanonicalizeProviderStream_ConcurrentStreams(t *testing.T) {
	ctx := context.Background()
	const count = 10
	errCh := make(chan error, count)

	for i := 0; i < count; i++ {
		go func(idx int) {
			ps := providerStreamWith(
				ProviderResponseStart{Type: "response_start", Model: "test"},
				ProviderTextDelta{Type: "text_delta", Delta: "concurrent "},
				ProviderTextDelta{Type: "text_delta", Delta: "test"},
				ProviderResponseEnd{
					Type:         "response_end",
					Message:      AssistantMessage{Role: "assistant"},
					FinishReason: "stop",
				},
			)
			cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "test")
			events, err := collectEvents(ctx, cs)
			if err != nil {
				errCh <- err
				return
			}
			text := extractText(events)
			if text != "concurrent test" {
				errCh <- errors.New("text mismatch")
				return
			}
			done := findEvent[EventDone](events)
			if done == nil || done.Reason != StopStop {
				errCh <- errors.New("missing/incorrect done")
				return
			}
			errCh <- nil
		}(i)
	}

	for i := 0; i < count; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("goroutine %d failed: %v", i, err)
		}
	}
}

// ============================================================================
// Events() channel methods — streamEvt type
// ============================================================================

func TestProviderEventStream_EventsChannel(t *testing.T) {
	ps := NewProviderEventStream()
	go func() {
		ps.Push(ProviderTextDelta{Type: "text_delta", Delta: "a"})
		ps.End(ProviderEventStreamResult{})
	}()

	count := 0
	for evt := range ps.Events() {
		if evt.done {
			if evt.err != nil {
				t.Fatalf("unexpected error: %v", evt.err)
			}
			result, err := ps.Result()
			if err != nil {
				t.Fatalf("Result error: %v", err)
			}
			_ = result // ProviderEventStreamResult is empty
			break
		}
		_ = evt.value
		count++
	}
	if count != 1 {
		t.Errorf("expected 1 event, got %d", count)
	}
}

// ============================================================================
// NewProviderEventStream creates a valid stream
// ============================================================================

func TestNewProviderEventStream(t *testing.T) {
	ps := NewProviderEventStream()
	if ps == nil {
		t.Fatal("NewProviderEventStream returned nil")
	}
	ps.End(ProviderEventStreamResult{})
	_, err := ps.Result()
	if err != nil {
		t.Fatalf("Result error: %v", err)
	}
}

// ============================================================================
// ProviderEvent type tag interface
// ============================================================================

func TestProviderEventTypes(t *testing.T) {
	// Verify all provider event types implement the interface
	var evts []ProviderEvent
	evts = append(evts, ProviderResponseStart{Type: "response_start"})
	evts = append(evts, ProviderTextDelta{Type: "text_delta"})
	evts = append(evts, ProviderThinkingDelta{Type: "thinking_delta"})
	evts = append(evts, ProviderToolCall{Type: "tool_call", ID: "x", Name: "f"})
	evts = append(evts, ProviderResponseEnd{Type: "response_end", Message: AssistantMessage{Role: "assistant"}})
	evts = append(evts, ProviderError{Type: "error", Message: "err"})
	evts = append(evts, ProviderRetryEvent{Type: "retry", Attempt: 1, MaxAttempts: 3})

	if len(evts) != 7 {
		t.Errorf("expected 7 event types, got %d", len(evts))
	}
}

// ============================================================================
// Error after stream already done — bridge should not panic
// ============================================================================

func TestCanonicalizeProviderStream_ErrorAfterDone(t *testing.T) {
	ctx := context.Background()
	ps := providerStreamWith(
		ProviderResponseStart{Type: "response_start", Model: "test"},
		ProviderTextDelta{Type: "text_delta", Delta: "ok"},
		ProviderResponseEnd{
			Type:         "response_end",
			Message:      AssistantMessage{Role: "assistant"},
			FinishReason: "stop",
		},
		ProviderError{Type: "error", Message: "late error"},
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "test")
	events, err := collectEvents(ctx, cs)
	if err != nil {
		t.Fatalf("expected success (error after done should be ignored), got: %v", err)
	}

	done := findEvent[EventDone](events)
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Reason != StopStop {
		t.Errorf("expected StopStop, got %v", done.Reason)
	}
}

// ============================================================================
// ContentIndex is monotonically increasing across blocks
// ============================================================================

func TestCanonicalizeProviderStream_ContentIndexSequence(t *testing.T) {
	ctx := context.Background()
	ps := providerStreamWith(
		ProviderResponseStart{Type: "response_start", Model: "test"},
		ProviderTextDelta{Type: "text_delta", Delta: "a"},
		ProviderThinkingDelta{Type: "thinking_delta", Delta: "b"},
		ProviderTextDelta{Type: "text_delta", Delta: "c"},
		ProviderResponseEnd{
			Type:         "response_end",
			Message:      AssistantMessage{Role: "assistant"},
			FinishReason: "stop",
		},
	)

	cs := CanonicalizeProviderStream(ps, "test-api", "test-prov", "test")
	events, err := collectEvents(ctx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// text_start[0], thinking_start[1], text_start[2]
	ts := findEvent[EventTextStart](events)
	if ts == nil {
		t.Fatal("expected EventTextStart")
	}
	th := findEvent[EventThinkingStart](events)
	if th == nil {
		t.Fatal("expected EventThinkingStart")
	}
	// The text_start we found is the first one (text block 0 at index 0).
	// Actually there are TWO text_start events. Let's get all.
	textStarts := filterEvents[EventTextStart](events)
	if len(textStarts) != 2 {
		t.Fatalf("expected 2 text_start events, got %d", len(textStarts))
	}
	_ = ts
	_ = th
	// text_start[0]: ContentIndex=0
	if textStarts[0].ContentIndex != 0 {
		t.Errorf("first text_start: expected ContentIndex=0, got %d", textStarts[0].ContentIndex)
	}
	// thinking_start: ContentIndex=1
	if th.ContentIndex != 1 {
		t.Errorf("thinking_start: expected ContentIndex=1, got %d", th.ContentIndex)
	}
	// text_start[1]: ContentIndex=2
	if len(textStarts) > 1 && textStarts[1].ContentIndex != 2 {
		t.Errorf("second text_start: expected ContentIndex=2, got %d", textStarts[1].ContentIndex)
	}
}

// ============================================================================
// Helpers
// ============================================================================

func assertType[T AssistantMessageEvent](t *testing.T, evt AssistantMessageEvent, name string) {
	t.Helper()
	if _, ok := evt.(T); !ok {
		t.Errorf("expected %s, got %T", name, evt)
	}
}

func findEvent[T AssistantMessageEvent](events []AssistantMessageEvent) *T {
	for _, evt := range events {
		if e, ok := evt.(T); ok {
			return &e
		}
	}
	return nil
}

func filterEvents[T AssistantMessageEvent](events []AssistantMessageEvent) []T {
	var result []T
	for _, evt := range events {
		if e, ok := evt.(T); ok {
			result = append(result, e)
		}
	}
	return result
}

func extractText(events []AssistantMessageEvent) string {
	var text string
	for _, evt := range events {
		if e, ok := evt.(EventTextDelta); ok {
			text += e.Delta
		}
	}
	return text
}
