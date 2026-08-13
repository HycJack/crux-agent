package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	core "github.com/hycjack/crux-ai/core"
)

// Helper: create a minimal model for testing.
func testModel(api core.KnownAPI, provider core.KnownProvider, id string) core.Model {
	return core.Model{
		ID:       id,
		API:      api,
		Provider: provider,
	}
}

// Helper: collect all events from a stream into a slice.
func collectEvents(ctx context.Context, stream *core.AssistantMessageEventStream) ([]core.AssistantMessageEvent, error) {
	var events []core.AssistantMessageEvent
	_, err := stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		events = append(events, evt)
		return nil
	})
	return events, err
}

// ============================================================================
// Basic tests: empty stream
// ============================================================================

func TestFakeNewReturnsEmpty(t *testing.T) {
	p := New()
	model := testModel("test-api", "test-provider", "test-model")
	ctx := context.Background()

	stream, err := p.Stream(ctx, model, core.Context{}, core.StreamOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events, err := collectEvents(ctx, stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have at least start + done
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}

	// Verify it's an empty stop response
	last := events[len(events)-1]
	done, ok := last.(core.EventDone)
	if !ok {
		t.Fatalf("expected EventDone, got %T", last)
	}
	if done.Reason != core.StopStop {
		t.Errorf("expected StopStop, got %v", done.Reason)
	}
}

// ============================================================================
// Pre-recorded AssistantMessageEvent streams
// ============================================================================

func TestFakeReplayAssistantEvents(t *testing.T) {
	ctx := context.Background()
	model := testModel("test-api", "test-provider", "test-model")

	// Build a pre-recorded stream
	s1 := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()
	go func() {
		msg := core.AssistantMessage{
			Role: "assistant", API: "test-api", Provider: "test-provider", Model: "test-model",
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "hello"}},
			StopReason: core.StopStop, Timestamp: time.Now(),
		}
		s1.Push(core.EventStart{Type: "start", API: "test-api", Provider: "test-provider", Model: "test-model", Timestamp: time.Now()})
		s1.Push(core.EventTextStart{Type: "text_start"})
		s1.Push(core.EventTextDelta{Type: "text_delta", Delta: "hello"})
		s1.Push(core.EventTextEnd{Type: "text_end"})
		s1.Push(core.EventDone{Type: "done", Message: msg})
		s1.End(msg)
	}()

	s2 := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()
	go func() {
		msg2 := core.AssistantMessage{
			Role: "assistant", API: "test-api", Provider: "test-provider", Model: "test-model",
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "world"}},
			StopReason: core.StopStop, Timestamp: time.Now(),
		}
		s2.Push(core.EventStart{Type: "start", API: "test-api", Provider: "test-provider", Model: "test-model", Timestamp: time.Now()})
		s2.Push(core.EventTextStart{Type: "text_start"})
		s2.Push(core.EventTextDelta{Type: "text_delta", Delta: "world"})
		s2.Push(core.EventTextEnd{Type: "text_end"})
		s2.Push(core.EventDone{Type: "done", Message: msg2})
		s2.End(msg2)
	}()

	p := NewFromAssistantStreams(s1, s2)

	// First call should get s1
	events1, err := collectEvents(ctx, mustGetStream(t, p, model, core.Context{}, core.StreamOptions{}))
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	text1 := extractText(t, events1)
	if text1 != "hello" {
		t.Errorf("expected 'hello', got %q", text1)
	}

	// Second call should get s2
	events2, err := collectEvents(ctx, mustGetStream(t, p, model, core.Context{}, core.StreamOptions{}))
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	text2 := extractText(t, events2)
	if text2 != "world" {
		t.Errorf("expected 'world', got %q", text2)
	}

	// Third call (no more recorded) should return empty/stop
	events3, err := collectEvents(ctx, mustGetStream(t, p, model, core.Context{}, core.StreamOptions{}))
	if err != nil {
		t.Fatalf("third call error: %v", err)
	}
	last := events3[len(events3)-1]
	if _, ok := last.(core.EventDone); !ok {
		t.Errorf("expected EventDone, got %T", last)
	}
}

// ============================================================================
// ProviderEvent streams (canonicalization bridge)
// ============================================================================

func TestFakeReplayProviderEvents(t *testing.T) {
	ctx := context.Background()

	// Build a provider-level event stream
	ps := core.NewProviderEventStream()
	go func() {
		ps.Push(core.ProviderResponseStart{Type: "response_start", Model: "test-model", Timestamp: time.Now()})
		ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: "Hello "})
		ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: "world"})
		msg := core.AssistantMessage{
			Role: "assistant", API: "test-api", Provider: "test-prov", Model: "test-model",
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Hello world"}},
			StopReason: core.StopStop, Timestamp: time.Now(),
		}
		ps.Push(core.ProviderResponseEnd{Type: "response_end", Message: msg, FinishReason: "stop"})
		ps.End(core.ProviderEventStreamResult{})
	}()

	p := NewFromProviderStreams(
		core.KnownAPI("test-api"),
		core.KnownProvider("test-prov"),
		"test-model",
		ps,
	)

	model := testModel("test-api", "test-prov", "test-model")
	stream := mustGetStream(t, p, model, core.Context{}, core.StreamOptions{})
	events, err := collectEvents(ctx, stream)
	if err != nil {
		t.Fatalf("collect error: %v", err)
	}

	if len(events) == 0 {
		t.Fatal("expected events, got none")
	}

	// Should see text_start / text_delta / text_end
	textStart := findEvent[core.EventTextStart](events)
	if textStart == nil {
		t.Error("expected EventTextStart")
	}
	textDelta := findEvent[core.EventTextDelta](events)
	if textDelta == nil {
		t.Error("expected EventTextDelta")
	} else if textDelta.Delta != "Hello " && textDelta.Delta != "world" {
		// either delta is fine
	}
	textEnd := findEvent[core.EventTextEnd](events)
	if textEnd == nil {
		t.Error("expected EventTextEnd")
	}

	done := findEvent[core.EventDone](events)
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Reason != core.StopStop {
		t.Errorf("expected StopStop, got %v", done.Reason)
	}
}

// ============================================================================
// Tool call via provider events
// ============================================================================

func TestFakeProviderToolCall(t *testing.T) {
	ctx := context.Background()

	args := json.RawMessage(`{"query": "weather"}`)
	ps := core.NewProviderEventStream()
	go func() {
		ps.Push(core.ProviderResponseStart{Type: "response_start", Model: "test-model", Timestamp: time.Now()})
		ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: "Let me check"})
		ps.Push(core.ProviderToolCall{
			Type: "tool_call", ID: "call-1", Name: "get_weather",
			Arguments: args,
		})
		msg := core.AssistantMessage{
			Role: "assistant", API: "test-api", Provider: "test-prov", Model: "test-model",
			StopReason: core.StopToolUse, Timestamp: time.Now(),
		}
		ps.Push(core.ProviderResponseEnd{Type: "response_end", Message: msg, FinishReason: "tool_use"})
		ps.End(core.ProviderEventStreamResult{})
	}()

	p := NewFromProviderStreams(
		core.KnownAPI("test-api"),
		core.KnownProvider("test-prov"),
		"test-model",
		ps,
	)

	model := testModel("test-api", "test-prov", "test-model")
	events, err := collectEvents(ctx, mustGetStream(t, p, model, core.Context{}, core.StreamOptions{}))
	if err != nil {
		t.Fatalf("collect error: %v", err)
	}

	toolStart := findEvent[core.EventToolCallStart](events)
	if toolStart == nil {
		t.Error("expected EventToolCallStart")
	} else if toolStart.ID != "call-1" {
		t.Errorf("expected call-1, got %q", toolStart.ID)
	}

	toolEnd := findEvent[core.EventToolCallEnd](events)
	if toolEnd == nil {
		t.Error("expected EventToolCallEnd")
	} else if string(toolEnd.Arguments) != string(args) {
		t.Errorf("expected %s, got %s", string(args), string(toolEnd.Arguments))
	}

	done := findEvent[core.EventDone](events)
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Reason != core.StopToolUse {
		t.Errorf("expected StopToolUse, got %v", done.Reason)
	}
}

// ============================================================================
// Thinking blocks via provider events
// ============================================================================

func TestFakeProviderThinking(t *testing.T) {
	ctx := context.Background()

	ps := core.NewProviderEventStream()
	go func() {
		ps.Push(core.ProviderResponseStart{Type: "response_start", Model: "test-model", Timestamp: time.Now()})
		ps.Push(core.ProviderThinkingDelta{Type: "thinking_delta", Delta: "Step 1: "})
		ps.Push(core.ProviderThinkingDelta{Type: "thinking_delta", Delta: "analyze"})
		ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: "Here's the answer"})
		msg := core.AssistantMessage{
			Role: "assistant", API: "test-api", Provider: "test-prov", Model: "test-model",
			StopReason: core.StopStop, Timestamp: time.Now(),
		}
		ps.Push(core.ProviderResponseEnd{Type: "response_end", Message: msg, FinishReason: "stop"})
		ps.End(core.ProviderEventStreamResult{})
	}()

	p := NewFromProviderStreams(
		core.KnownAPI("test-api"),
		core.KnownProvider("test-prov"),
		"test-model",
		ps,
	)

	model := testModel("test-api", "test-prov", "test-model")
	events, err := collectEvents(ctx, mustGetStream(t, p, model, core.Context{}, core.StreamOptions{}))
	if err != nil {
		t.Fatalf("collect error: %v", err)
	}

	thinkingStart := findEvent[core.EventThinkingStart](events)
	if thinkingStart == nil {
		t.Error("expected EventThinkingStart")
	}

	thinkingDelta := findEvent[core.EventThinkingDelta](events)
	if thinkingDelta == nil {
		t.Error("expected EventThinkingDelta")
	}

	thinkingEnd := findEvent[core.EventThinkingEnd](events)
	if thinkingEnd == nil {
		t.Error("expected EventThinkingEnd")
	}
}

// ============================================================================
// Error via provider events
// ============================================================================

func TestFakeProviderError(t *testing.T) {
	ctx := context.Background()

	ps := core.NewProviderEventStream()
	go func() {
		ps.Push(core.ProviderResponseStart{Type: "response_start", Model: "test-model", Timestamp: time.Now()})
		ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: "partial"})
		ps.Push(core.ProviderError{Type: "error", Message: "rate limited"})
		ps.End(core.ProviderEventStreamResult{})
	}()

	p := NewFromProviderStreams(
		core.KnownAPI("test-api"),
		core.KnownProvider("test-prov"),
		"test-model",
		ps,
	)

	model := testModel("test-api", "test-prov", "test-model")
	_, err := collectEvents(ctx, mustGetStream(t, p, model, core.Context{}, core.StreamOptions{}))
	if err == nil {
		t.Error("expected error, got nil")
	} else if err.Error() != "rate limited" {
		t.Errorf("expected 'rate limited', got %v", err)
	}
}

// ============================================================================
// Retry events are silently filtered by the bridge
// ============================================================================

func TestFakeRetryEventFiltered(t *testing.T) {
	ctx := context.Background()

	ps := core.NewProviderEventStream()
	go func() {
		ps.Push(core.ProviderRetryEvent{Type: "retry", Attempt: 1, MaxAttempts: 3, DelaySeconds: 0.1, Message: "transient"})
		ps.Push(core.ProviderResponseStart{Type: "response_start", Model: "test-model", Timestamp: time.Now()})
		ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: "final"})
		msg := core.AssistantMessage{
			Role: "assistant", API: "test-api", Provider: "test-prov", Model: "test-model",
			StopReason: core.StopStop, Timestamp: time.Now(),
		}
		ps.Push(core.ProviderResponseEnd{Type: "response_end", Message: msg, FinishReason: "stop"})
		ps.End(core.ProviderEventStreamResult{})
	}()

	p := NewFromProviderStreams(
		core.KnownAPI("test-api"),
		core.KnownProvider("test-prov"),
		"test-model",
		ps,
	)

	model := testModel("test-api", "test-prov", "test-model")
	events, err := collectEvents(ctx, mustGetStream(t, p, model, core.Context{}, core.StreamOptions{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Retry event should not appear in output
	for _, evt := range events {
		if fmt.Sprintf("%T", evt) == "core.ProviderRetryEvent" {
			t.Errorf("retry event leaked into output: %#v", evt)
		}
	}
	if len(events) < 2 {
		t.Fatalf("expected at least start + done, got %d", len(events))
	}
}

// ============================================================================
// Call history tracking
// ============================================================================

func TestFakeCallHistory(t *testing.T) {
	p := New()
	model := testModel("test-api", "test-prov", "test-model")
	llmCtx := core.Context{SystemPrompt: "be helpful"}

	if p.HasCalls() {
		t.Error("expected no calls initially")
	}

	mustGetStream(t, p, model, llmCtx, core.StreamOptions{})
	mustGetStream(t, p, model, llmCtx, core.StreamOptions{})

	calls := p.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Model.ID != "test-model" {
		t.Errorf("expected test-model, got %q", calls[0].Model.ID)
	}
	if calls[0].Context.SystemPrompt != "be helpful" {
		t.Errorf("expected 'be helpful', got %q", calls[0].Context.SystemPrompt)
	}
}

// ============================================================================
// Auto-replay mode
// ============================================================================

func TestFakeAutoReplay(t *testing.T) {
	ctx := context.Background()
	model := testModel("test-api", "test-prov", "test-model")

	p := New()
	p.SetAutoReplay(true)

	stream := mustGetStream(t, p, model, core.Context{}, core.StreamOptions{})
	events, err := collectEvents(ctx, stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have content that says "autoreplay response"
	text := extractText(t, events)
	if text != "autoreplay response" {
		t.Errorf("expected 'autoreplay response', got %q", text)
	}
}

// ============================================================================
// MustCollect helper
// ============================================================================

func TestMustCollect(t *testing.T) {
	ctx := context.Background()
	model := testModel("test-api", "test-prov", "test-model")

	p := New()
	p.SetAutoReplay(true)

	events := MustCollect(ctx, p, model, core.Context{}, core.StreamOptions{})
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}
	if _, ok := events[len(events)-1].(core.EventDone); !ok {
		t.Errorf("expected EventDone, got %T", events[len(events)-1])
	}
}

// ============================================================================
// Helpers
// ============================================================================

func mustGetStream(t *testing.T, p *Provider, model core.Model, llmCtx core.Context, opts core.StreamOptions) *core.AssistantMessageEventStream {
	t.Helper()
	stream, err := p.Stream(context.Background(), model, llmCtx, opts)
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}
	return stream
}

func extractText(t *testing.T, events []core.AssistantMessageEvent) string {
	t.Helper()
	var text string
	for _, evt := range events {
		if e, ok := evt.(core.EventTextDelta); ok {
			text += e.Delta
		}
	}
	return text
}

func findEvent[T core.AssistantMessageEvent](events []core.AssistantMessageEvent) *T {
	for _, evt := range events {
		if e, ok := evt.(T); ok {
			return &e
		}
	}
	return nil
}

// Verify compile-time interface satisfaction.
var _ core.APIProvider = (*Provider)(nil)
