package mistral

import (
	"strings"
	"testing"

	core "github.com/hycjack/crux-ai/core"
)

// runMistralSSE feeds SSE data through processMistralSSE and returns
// all events plus the final message.
func runMistralSSE(t *testing.T, sseData string) ([]core.AssistantMessageEvent, core.AssistantMessage) {
	t.Helper()
	model := core.Model{ID: "magistral-test", Provider: "mistral", API: "mistral"}
	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()

	var events []core.AssistantMessageEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		for evt := range stream.Events() {
			if evt.Done() {
				return
			}
			events = append(events, evt.Value())
		}
	}()

	r := strings.NewReader(sseData)
	msg, err := processMistralSSE(r, stream, model, core.StreamOptions{})
	if err != nil {
		t.Fatalf("processMistralSSE: %v", err)
	}
	// Match production: push End(msg) to signal EventDone to stream readers.
	stream.End(msg)
	<-done

	return events, msg
}

// Mistral Magistral exposes reasoning via delta.reasoning_content.
func TestMistral_ReasoningContent_EmitsThinkingEvents(t *testing.T) {
	sse := "" +
		`data: {"id":"abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"Let me think..."},"finish_reason":null}]}
` +
		`data: {"id":"abc","choices":[{"index":0,"delta":{"reasoning_content":" carefully."},"finish_reason":null}]}
` +
		`data: {"id":"abc","choices":[{"index":0,"delta":{"content":"42"},"finish_reason":null}]}
` +
		`data: {"id":"abc","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
` +
		`data: [DONE]
`
	events, msg := runMistralSSE(t, sse)

	gotTypes := make([]string, 0, len(events))
	for _, e := range events {
		switch v := e.(type) {
		case core.EventStart:
			gotTypes = append(gotTypes, v.Type)
		case core.EventThinkingStart:
			gotTypes = append(gotTypes, v.Type)
		case core.EventThinkingDelta:
			gotTypes = append(gotTypes, v.Type)
		case core.EventThinkingEnd:
			gotTypes = append(gotTypes, v.Type)
		case core.EventTextStart:
			gotTypes = append(gotTypes, v.Type)
		case core.EventTextDelta:
			gotTypes = append(gotTypes, v.Type)
		case core.EventTextEnd:
			gotTypes = append(gotTypes, v.Type)
		case core.EventDone:
			gotTypes = append(gotTypes, v.Type)
		}
	}

	// The Mistral SSE processor emits thinking content as it arrives in
	// the stream, then text content, and finally thinking_end on stream
	// completion (finish_reason: "stop"). This is because Mistral sends
	// reasoning_content deltas followed by content deltas, and the
	// processor only closes the thinking block when it sees the final
	// finish_reason event.
	wantSubstr := []string{
		"start",
		"thinking_start", "thinking_delta", "thinking_delta",
		"text_start", "text_delta", "text_end",
		"thinking_end",
		"done",
	}
	if len(gotTypes) < len(wantSubstr) {
		t.Fatalf("event count: got %d (%v), want at least %d", len(gotTypes), gotTypes, len(wantSubstr))
	}
	for i, w := range wantSubstr {
		if gotTypes[i] != w {
			t.Errorf("event %d: got %q, want %q (all=%v)", i, gotTypes[i], w, gotTypes)
		}
	}

	var sawThinking, sawText bool
	for _, b := range msg.Content {
		if tc, ok := b.(core.ThinkingContent); ok {
			sawThinking = true
			if tc.Thinking != "Let me think... carefully." {
				t.Errorf("reasoning: got %q", tc.Thinking)
			}
		}
		if tc, ok := b.(core.TextContent); ok {
			sawText = true
			if tc.Text != "42" {
				t.Errorf("text: got %q", tc.Text)
			}
		}
	}
	if !sawThinking {
		t.Errorf("missing ThinkingContent block")
	}
	if !sawText {
		t.Errorf("missing TextContent block")
	}
}

func TestMistral_NoReasoning_StillWorks(t *testing.T) {
	sse := "" +
		`data: {"id":"x","choices":[{"index":0,"delta":{"content":"plain"},"finish_reason":null}]}
` +
		`data: {"id":"x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
` +
		`data: [DONE]
`
	_, msg := runMistralSSE(t, sse)
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	if tc, ok := msg.Content[0].(core.TextContent); !ok || tc.Text != "plain" {
		t.Errorf("text: got %+v", msg.Content[0])
	}
}
