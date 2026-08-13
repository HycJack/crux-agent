// Package providers_test contains integration tests that exercise the full
// stack: FakeProvider registration, ai.Stream dispatch, and event consumption.
//
// These tests verify that the provider registry, ai API layer, and event
// systems work together correctly.
package cruxai_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hycjack/crux-ai/ai"
	"github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/providers/fake"
)

// TestFakeProviderViaAILayer registers a FakeProvider and calls it through
// the public ai.Stream API.
func TestFakeProviderViaAILayer(t *testing.T) {
	if err := setup(t); err != nil {
		t.Fatal(err)
	}
	defer teardown()

	model := core.Model{
		ID:       "fake-model",
		API:      "fake-api",
		Provider: "fake-prov",
	}

	ctx := context.Background()
	stream, err := ai.Stream(ctx, model, []core.Message{}, core.StreamOptions{})
	if err != nil {
		t.Fatalf("ai.Stream failed: %v", err)
	}

	var events []core.AssistantMessageEvent
	_, err = stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		events = append(events, evt)
		return nil
	})
	if err != nil {
		t.Fatalf("ForEach failed: %v", err)
	}

	// Should get at least start + done
	if len(events) < 2 {
		t.Fatalf("expected >= 2 events, got %d", len(events))
	}

	// Verify start event carries API/provider/model
	start, ok := events[0].(core.EventStart)
	if !ok {
		t.Fatalf("expected EventStart, got %T", events[0])
	}
	if start.API != "fake-api" {
		t.Errorf("API mismatch: %s", start.API)
	}
	if start.Model != "fake-model" {
		t.Errorf("Model mismatch: %s", start.Model)
	}

	// Verify done event
	done := findEvent[core.EventDone](events)
	if done == nil {
		t.Fatal("expected EventDone")
	}
	msg := done.Message
	if msg.Role != "assistant" {
		t.Errorf("expected role=assistant, got %s", msg.Role)
	}
	if msg.StopReason != core.StopStop {
		t.Errorf("expected StopStop, got %v", msg.StopReason)
	}
}

// TestFakeProviderToolCallViaAI exercises a tool call through ai.Stream.
func TestFakeProviderToolCallViaAI(t *testing.T) {
	if err := setupToolCallProvider(t); err != nil {
		t.Fatal(err)
	}
	defer teardown()

	model := core.Model{ID: "fake-model", API: "fake-tool-api", Provider: "fake-tool-prov"}
	ctx := context.Background()
	stream, err := ai.Stream(ctx, model, []core.Message{}, core.StreamOptions{})
	if err != nil {
		t.Fatalf("ai.Stream failed: %v", err)
	}

	events, err := collectEvents(ctx, stream)
	if err != nil {
		t.Fatalf("ForEach failed: %v", err)
	}

	toolStart := findEvent[core.EventToolCallStart](events)
	if toolStart == nil {
		t.Fatal("expected EventToolCallStart")
	}
	if toolStart.Name != "get_weather" {
		t.Errorf("expected get_weather, got %s", toolStart.Name)
	}
	if toolStart.ID != "call-1" {
		t.Errorf("expected call-1, got %s", toolStart.ID)
	}

	toolEnd := findEvent[core.EventToolCallEnd](events)
	if toolEnd == nil {
		t.Fatal("expected EventToolCallEnd")
	}
	var args map[string]any
	if err := json.Unmarshal(toolEnd.Arguments, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args["city"] != "beijing" {
		t.Errorf("expected city=beijing, got %v", args["city"])
	}

	done := findEvent[core.EventDone](events)
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Reason != core.StopToolUse {
		t.Errorf("expected StopToolUse, got %v", done.Reason)
	}
}

// TestFakeProviderErrorViaAI verifies error propagation through ai layer.
func TestFakeProviderErrorViaAI(t *testing.T) {
	if err := setupErrorProvider(t); err != nil {
		t.Fatal(err)
	}
	defer teardown()

	model := core.Model{ID: "fake-model", API: "fake-error-api", Provider: "fake-error-prov"}
	ctx := context.Background()
	stream, err := ai.Stream(ctx, model, []core.Message{}, core.StreamOptions{})
	if err != nil {
		t.Fatalf("ai.Stream failed: %v", err)
	}

	_, err = collectEvents(ctx, stream)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "test error" {
		t.Errorf("expected 'test error', got %v", err)
	}
}

// TestFakeProviderMultipleCalls verifies sequential calls return different streams.
func TestFakeProviderMultipleCalls(t *testing.T) {
	p := fake.NewFromAssistantStreams(
		newSingleTextStream("response-1"),
		newSingleTextStream("response-2"),
	)
	core.RegisterProvider("fake-multi-api", p, "test-fake")
	defer core.UnregisterProviders("test-fake")

	model := core.Model{ID: "multi", API: "fake-multi-api", Provider: "fake-prov"}

	ctx := context.Background()

	// First call
	e1, err := collectEvents(ctx, mustStream(t, model))
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	t1 := extractText(e1)
	if t1 != "response-1" {
		t.Errorf("expected 'response-1', got %q", t1)
	}

	// Second call
	e2, err := collectEvents(ctx, mustStream(t, model))
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	t2 := extractText(e2)
	if t2 != "response-2" {
		t.Errorf("expected 'response-2', got %q", t2)
	}
}

// TestFakeProviderCallHistory verifies call recording.
func TestFakeProviderCallHistory(t *testing.T) {
	p := fake.New()
	core.RegisterProvider("fake-hist-api", p, "test-fake")
	defer core.UnregisterProviders("test-fake")

	model := core.Model{ID: "hist-model", API: "fake-hist-api", Provider: "fake-prov"}

	mustStream(t, model)
	mustStream(t, model)

	calls := p.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Model.ID != "hist-model" {
		t.Errorf("expected hist-model, got %s", calls[0].Model.ID)
	}
}

// ============================================================================
// ProviderEvent canonicalization integration
// ============================================================================

// TestFakeProviderViaProviderEventStream creates a provider-level stream,
// wraps it in FakeProvider, registers it, and calls ai.Stream.
func TestFakeProviderCanonicalizationIntegration(t *testing.T) {
	ps := core.NewProviderEventStream()
	go func() {
		ps.Push(core.ProviderResponseStart{Type: "response_start", Model: "canonical-model"})
		ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: "Hello "})
		ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: "World"})
		msg := core.AssistantMessage{Role: "assistant"}
		ps.Push(core.ProviderResponseEnd{Type: "response_end", Message: msg, FinishReason: "stop"})
		ps.End(core.ProviderEventStreamResult{})
	}()

	p := fake.NewFromProviderStreams("canon-api", "canon-prov", "canon-model", ps)
	core.RegisterProvider("canon-api", p, "test-fake")
	defer core.UnregisterProviders("test-fake")

	model := core.Model{ID: "canon-model", API: "canon-api", Provider: "canon-prov"}
	ctx := context.Background()

	stream, err := ai.Stream(ctx, model, []core.Message{}, core.StreamOptions{})
	if err != nil {
		t.Fatalf("ai.Stream failed: %v", err)
	}

	events, err := collectEvents(ctx, stream)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	text := extractText(events)
	if text != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", text)
	}

	done := findEvent[core.EventDone](events)
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Reason != core.StopStop {
		t.Errorf("expected StopStop, got %v", done.Reason)
	}
	// Verify message fields were populated from canonicalize API args
	if done.Message.API != "canon-api" {
		t.Errorf("expected API=canon-api, got %s", done.Message.API)
	}
	if done.Message.Provider != "canon-prov" {
		t.Errorf("expected Provider=canon-prov, got %s", done.Message.Provider)
	}
	if done.Message.Model != "canon-model" {
		t.Errorf("expected Model=canon-model, got %s", done.Message.Model)
	}
}

// TestFakeProviderCanonicalizationError tests error through canonicalization bridge.
func TestFakeProviderCanonicalizationError(t *testing.T) {
	ps := core.NewProviderEventStream()
	go func() {
		ps.Push(core.ProviderResponseStart{Type: "response_start", Model: "test"})
		ps.Push(core.ProviderError{Type: "error", Message: "rate limit exceeded"})
		ps.End(core.ProviderEventStreamResult{})
	}()

	p := fake.NewFromProviderStreams("err-api", "err-prov", "test", ps)
	core.RegisterProvider("err-api", p, "test-fake")
	defer core.UnregisterProviders("test-fake")

	model := core.Model{ID: "test", API: "err-api", Provider: "err-prov"}
	ctx := context.Background()

	stream, err := ai.Stream(ctx, model, []core.Message{}, core.StreamOptions{})
	if err != nil {
		t.Fatalf("ai.Stream failed: %v", err)
	}

	_, err = collectEvents(ctx, stream)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "rate limit exceeded" {
		t.Errorf("expected 'rate limit exceeded', got %v", err)
	}
}

// TestFakeProviderMultipleCanonicalizationStreams tests that multiple
// canonicalized streams work correctly in sequence.
func TestFakeProviderMultipleCanonicalizedStreams(t *testing.T) {
	// Create provider-level streams
	ps1 := core.NewProviderEventStream()
	go func() {
		ps1.Push(core.ProviderTextDelta{Type: "text_delta", Delta: "first"})
		ps1.Push(core.ProviderResponseEnd{Type: "response_end", Message: core.AssistantMessage{Role: "assistant"}, FinishReason: "stop"})
		ps1.End(core.ProviderEventStreamResult{})
	}()

	ps2 := core.NewProviderEventStream()
	go func() {
		ps2.Push(core.ProviderTextDelta{Type: "text_delta", Delta: "second"})
		ps2.Push(core.ProviderResponseEnd{Type: "response_end", Message: core.AssistantMessage{Role: "assistant"}, FinishReason: "stop"})
		ps2.End(core.ProviderEventStreamResult{})
	}()

	p := fake.NewFromProviderStreams("multi-api", "multi-prov", "test", ps1, ps2)
	core.RegisterProvider("multi-api", p, "test-fake")
	defer core.UnregisterProviders("test-fake")

	model := core.Model{ID: "test", API: "multi-api", Provider: "multi-prov"}
	ctx := context.Background()

	// First call
	e1, err := collectEvents(ctx, mustStream(t, model))
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if extractText(e1) != "first" {
		t.Errorf("expected 'first', got %q", extractText(e1))
	}

	// Second call
	e2, err := collectEvents(ctx, mustStream(t, model))
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if extractText(e2) != "second" {
		t.Errorf("expected 'second', got %q", extractText(e2))
	}
}

// ============================================================================
// AutoReplay mode integration
// ============================================================================

func TestFakeProviderAutoReplay(t *testing.T) {
	p := fake.New()
	p.SetAutoReplay(true)
	core.RegisterProvider("auto-api", p, "test-fake")
	defer core.UnregisterProviders("test-fake")

	model := core.Model{ID: "auto", API: "auto-api", Provider: "auto-prov"}
	ctx := context.Background()

	events, err := collectEvents(ctx, mustStream(t, model))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := extractText(events)
	if text != "autoreplay response" {
		t.Errorf("expected 'autoreplay response', got %q", text)
	}
}

// ============================================================================
// Helpers
// ============================================================================

// setup registers a basic FakeProvider with no pre-recorded streams.
func setup(t *testing.T) error {
	t.Helper()
	p := fake.New()
	core.RegisterProvider("fake-api", p, "test-fake")
	return nil
}

// setupToolCallProvider registers a FakeProvider that serves a tool call.
func setupToolCallProvider(t *testing.T) error {
	t.Helper()
	ps := core.NewProviderEventStream()
	go func() {
		ps.Push(core.ProviderResponseStart{Type: "response_start", Model: "test"})
		ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: "Getting "})
		ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: "weather"})
		ps.Push(core.ProviderToolCall{
			Type: "tool_call", ID: "call-1", Name: "get_weather",
			Arguments: json.RawMessage(`{"city": "beijing"}`),
		})
		msg := core.AssistantMessage{Role: "assistant"}
		ps.Push(core.ProviderResponseEnd{Type: "response_end", Message: msg, FinishReason: "tool_use"})
		ps.End(core.ProviderEventStreamResult{})
	}()
	p := fake.NewFromProviderStreams("fake-tool-api", "fake-tool-prov", "fake-model", ps)
	core.RegisterProvider("fake-tool-api", p, "test-fake")
	return nil
}

// setupErrorProvider registers a FakeProvider that emits an error.
func setupErrorProvider(t *testing.T) error {
	t.Helper()
	ps := core.NewProviderEventStream()
	go func() {
		ps.Push(core.ProviderResponseStart{Type: "response_start", Model: "test"})
		ps.Push(core.ProviderError{Type: "error", Message: "test error"})
		ps.End(core.ProviderEventStreamResult{})
	}()
	p := fake.NewFromProviderStreams("fake-error-api", "fake-error-prov", "fake-model", ps)
	core.RegisterProvider("fake-error-api", p, "test-fake")
	return nil
}

func teardown() {
	core.UnregisterProviders("test-fake")
}

func newSingleTextStream(text string) *core.AssistantMessageEventStream {
	s := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()
	go func() {
		msg := core.AssistantMessage{
			Role: "assistant", StopReason: core.StopStop, Timestamp: time.Now(),
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: text}},
		}
		s.Push(core.EventStart{Type: "start", Timestamp: time.Now()})
		s.Push(core.EventTextStart{Type: "text_start"})
		s.Push(core.EventTextDelta{Type: "text_delta", Delta: text})
		s.Push(core.EventTextEnd{Type: "text_end"})
		s.Push(core.EventDone{Type: "done", Reason: core.StopStop, Message: msg})
		s.End(msg)
	}()
	return s
}

func mustStream(t *testing.T, model core.Model) *core.AssistantMessageEventStream {
	t.Helper()
	ctx := context.Background()
	stream, err := ai.Stream(ctx, model, []core.Message{}, core.StreamOptions{})
	if err != nil {
		t.Fatalf("ai.Stream: %v", err)
	}
	return stream
}

func collectEvents(ctx context.Context, stream *core.AssistantMessageEventStream) ([]core.AssistantMessageEvent, error) {
	var events []core.AssistantMessageEvent
	_, err := stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		events = append(events, evt)
		return nil
	})
	return events, err
}

func findEvent[T core.AssistantMessageEvent](events []core.AssistantMessageEvent) *T {
	for _, evt := range events {
		if e, ok := evt.(T); ok {
			return &e
		}
	}
	return nil
}

func extractText(events []core.AssistantMessageEvent) string {
	var text string
	for _, evt := range events {
		if e, ok := evt.(core.EventTextDelta); ok {
			text += e.Delta
		}
	}
	return text
}
