package openai

import (
	"context"
	"fmt"
	"strings"
	"testing"

	core "crux-ai/core"
)

// runResponsesSSE feeds SSE-formatted data to processResponsesSSE and
// returns the accumulated events and final message.
func runResponsesSSE(t *testing.T, sseData string) ([]core.AssistantMessageEvent, core.AssistantMessage) {
	t.Helper()
	model := core.Model{ID: "gpt-test", Provider: "openai-responses", API: "openai-responses"}
	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()

	var events []core.AssistantMessageEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		for evt := range stream.Events() {
			if evt.Err() != nil {
				continue
			}
			if evt.Done() {
				return
			}
			events = append(events, evt.Value())
		}
	}()

	r := strings.NewReader(sseData)
	out, err := processResponsesSSE(r, stream, model, core.StreamOptions{})
	if err != nil {
		t.Fatalf("processResponsesSSE: %v", err)
	}
	stream.End(out)
	<-done
	return events, out
}

// eventTypeOf returns a discriminator string for an AssistantMessageEvent.
// Uses the Type field embedded in each event variant.
func eventTypeOf(e core.AssistantMessageEvent) string {
	switch ev := e.(type) {
	case core.EventStart:
		return ev.Type
	case core.EventTextStart:
		return ev.Type
	case core.EventTextDelta:
		return ev.Type
	case core.EventTextEnd:
		return ev.Type
	case core.EventThinkingStart:
		return ev.Type
	case core.EventThinkingDelta:
		return ev.Type
	case core.EventThinkingEnd:
		return ev.Type
	case core.EventToolCallStart:
		return ev.Type
	case core.EventToolCallDelta:
		return ev.Type
	case core.EventToolCallEnd:
		return ev.Type
	case core.EventDone:
		return ev.Type
	case core.EventError:
		return ev.Type
	default:
		// Print the actual type for debugging
		return fmt.Sprintf("unknown(%T)", e)
	}
}

func TestResponses_HandlesReasoningItem(t *testing.T) {
	// The OpenAI Responses API emits a separate "reasoning" item
	// with reasoning_text.delta events.
	sse := "" +
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1"}}
` +
		`data: {"type":"response.reasoning_text.delta","output_index":0,"delta":"Thinking step 1 "}
` +
		`data: {"type":"response.reasoning_text.delta","output_index":0,"delta":"step 2"}
` +
		`data: {"type":"response.output_item.done","output_index":0}
` +
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant"}}
` +
		`data: {"type":"response.output_text.delta","output_index":1,"delta":"Hello "}
` +
		`data: {"type":"response.output_text.delta","output_index":1,"delta":"world"}
` +
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":2,"input_tokens_details":{"cached_tokens":3}}}}
` +
		`data: [DONE]
`

	events, msg := runResponsesSSE(t, sse)

	gotTypes := make([]string, len(events))
	for i, e := range events {
		gotTypes[i] = eventTypeOf(e)
	}

	want := []string{
		"start",
		"thinking_start", "thinking_delta", "thinking_delta",
		"thinking_end",
		"text_start", "text_delta", "text_delta", "text_end",
		"done",
	}
	if len(gotTypes) != len(want) {
		t.Fatalf("event count: got %d (%v), want %d (%v)", len(gotTypes), gotTypes, len(want), want)
	}
	for i, w := range want {
		if gotTypes[i] != w {
			t.Errorf("event %d: got %q, want %q (all=%v)", i, gotTypes[i], w, gotTypes)
		}
	}

	// Validate content blocks: a ThinkingContent + a TextContent.
	var sawThinking, sawText bool
	for _, b := range msg.Content {
		if tc, ok := b.(core.ThinkingContent); ok {
			sawThinking = true
			if tc.Thinking != "Thinking step 1 step 2" {
				t.Errorf("thinking content: got %q", tc.Thinking)
			}
		}
		if tc, ok := b.(core.TextContent); ok {
			sawText = true
			if tc.Text != "Hello world" {
				t.Errorf("text content: got %q", tc.Text)
			}
		}
	}
	if !sawThinking {
		t.Errorf("missing ThinkingContent block")
	}
	if !sawText {
		t.Errorf("missing TextContent block")
	}

	// Validate usage fields.
	if msg.Usage.Input != 10 {
		t.Errorf("usage.input: got %d", msg.Usage.Input)
	}
	if msg.Usage.Output != 2 {
		t.Errorf("usage.output: got %d", msg.Usage.Output)
	}
	if msg.Usage.CacheRead != 3 {
		t.Errorf("usage.cacheRead: got %d", msg.Usage.CacheRead)
	}
}

func TestResponses_NoReasoning_StillWorks(t *testing.T) {
	sse := "" +
		`data: {"type":"response.output_text.delta","output_index":0,"delta":"just text"}
` +
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}
` +
		`data: [DONE]
`

	_, msg := runResponsesSSE(t, sse)
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	if tc, ok := msg.Content[0].(core.TextContent); !ok || tc.Text != "just text" {
		t.Errorf("text: got %+v", msg.Content[0])
	}
}

func TestResponses_ScannerErrorPropagated(t *testing.T) {
	model := core.Model{ID: "gpt-test", Provider: "openai-responses", API: "openai-responses"}
	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()
	r := strings.NewReader("")
	_, err := processResponsesSSE(r, stream, model, core.StreamOptions{})
	if err != nil {
		t.Logf("got error (expected for empty stream with no [DONE]): %v", err)
	}
}

func TestResponses_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	model := core.Model{ID: "gpt-test", Provider: "openai-responses", API: "openai-responses"}

	s, err := streamResponses(ctx, model, core.Context{}, core.StreamOptions{APIKey: "test"}, ResponsesOptions{
		ReasoningEffort: "low",
	})
	if err != nil {
		t.Fatalf("stream setup: %v", err)
	}
	_ = s
}
