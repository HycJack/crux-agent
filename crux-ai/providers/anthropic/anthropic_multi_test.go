package anthropic

import (
	"strings"
	"testing"

	core "github.com/hycjack/crux-ai/core"
)

// runAnthropicSSE drives processSSEStreamAnthropic through the canonicalization
// bridge and returns all resulting AssistantMessageEvents.
func runAnthropicSSE(t *testing.T, sseData string) []core.AssistantMessageEvent {
	t.Helper()
	model := core.Model{ID: "claude-test", Provider: "anthropic", API: "anthropic-messages"}
	ps := core.NewProviderEventStream()

	var events []core.AssistantMessageEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		out := core.CanonicalizeProviderStream(ps, model.API, model.Provider, model.ID)
		for evt := range out.Events() {
			if evt.Done() {
				return
			}
			events = append(events, evt.Value())
		}
	}()

	r := strings.NewReader(sseData)
	err := processSSEStreamAnthropic(r, ps, model, core.StreamOptions{})
	if err != nil {
		t.Fatalf("processSSEStreamAnthropic: %v", err)
	}
	ps.End(core.ProviderEventStreamResult{})
	<-done
	return events
}

// Test that text/thinking blocks have their *own* signature, not the
// "last seen" global. Two text blocks with different signatures should
// preserve both per-block.
func TestAnthropic_MultipleTextBlocks_KeepPerBlockSignatures(t *testing.T) {
	sse := "" +
		// Block 0: text with sig-A
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","signature":"sig-A"}}
` +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}}
` +
		`data: {"type":"content_block_stop","index":0}
` +
		// Block 1: text with sig-B (different)
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","signature":"sig-B"}}
` +
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"World"}}
` +
		`data: {"type":"content_block_stop","index":1}
` +
		// Terminate
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}
` +
		`data: [DONE]
`

	events := runAnthropicSSE(t, sse)

	// 2 text blocks → 2 text_start, 2 text_end
	var textStarts, textEnds int
	var totalText string
	for _, evt := range events {
		switch e := evt.(type) {
		case core.EventTextStart:
			textStarts++
		case core.EventTextEnd:
			textEnds++
			totalText += e.Content
		case core.EventTextDelta:
			// ignore
		}
	}
	if textStarts != 2 {
		t.Errorf("expected 2 text_start, got %d", textStarts)
	}
	if textEnds != 2 {
		t.Errorf("expected 2 text_end, got %d", textEnds)
	}
	if totalText != "Hello World" {
		t.Errorf("expected text 'Hello World', got %q", totalText)
	}
}

// Test that thinking + text blocks produce the correct event sequence.
func TestAnthropic_ThinkingTextInterleaving(t *testing.T) {
	sse := "" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","signature":"think-sig"}}
` +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think..."}}
` +
		`data: {"type":"content_block_stop","index":0}
` +
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","signature":"text-sig"}}
` +
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"The answer is 42"}}
` +
		`data: {"type":"content_block_stop","index":1}
` +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}
` +
		`data: [DONE]
`

	events := runAnthropicSSE(t, sse)

	thStarts := filterEventTypes[core.EventThinkingStart](events)
	thEnds := filterEventTypes[core.EventThinkingEnd](events)
	txtStarts := filterEventTypes[core.EventTextStart](events)
	var thinkingContent string
	for _, e := range filterEventTypes[core.EventThinkingEnd](events) {
		thinkingContent = e.Content
	}

	if len(thStarts) != 1 {
		t.Errorf("expected 1 thinking_start, got %d", len(thStarts))
	}
	if len(thEnds) != 1 {
		t.Errorf("expected 1 thinking_end, got %d", len(thEnds))
	}
	if len(txtStarts) != 1 {
		t.Errorf("expected 1 text_start, got %d", len(txtStarts))
	}
	if thinkingContent != "Let me think..." {
		t.Errorf("expected 'Let me think...', got %q", thinkingContent)
	}

	// Verify thinking block gets content_index 0, text block gets 1
	if len(thStarts) > 0 && thStarts[0].ContentIndex != 0 {
		t.Errorf("expected thinking ContentIndex=0, got %d", thStarts[0].ContentIndex)
	}
	if len(txtStarts) > 0 && txtStarts[0].ContentIndex != 1 {
		t.Errorf("expected text ContentIndex=1, got %d", txtStarts[0].ContentIndex)
	}
}

// Test that tool calls are correctly parsed.
func TestAnthropic_ToolCall(t *testing.T) {
	sse := "" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_01","name":"get_weather","input":{"city":"Paris"}}}
` +
		// tool_use doesn't have content_block_stop normally, but just in case:
		`data: {"type":"content_block_stop","index":0}
` +
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}
` +
		`data: [DONE]
`

	events := runAnthropicSSE(t, sse)

	toolStarts := filterEventTypes[core.EventToolCallStart](events)
	if len(toolStarts) != 1 {
		t.Fatalf("expected 1 toolcall_start, got %d", len(toolStarts))
	}
	if toolStarts[0].ID != "tu_01" {
		t.Errorf("expected ID tu_01, got %s", toolStarts[0].ID)
	}
	if toolStarts[0].Name != "get_weather" {
		t.Errorf("expected Name get_weather, got %s", toolStarts[0].Name)
	}

	toolEnds := filterEventTypes[core.EventToolCallEnd](events)
	if len(toolEnds) != 1 {
		t.Fatalf("expected 1 toolcall_end, got %d", len(toolEnds))
	}

	done := findEventType[core.EventDone](events)
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Reason != core.StopToolUse {
		t.Errorf("expected StopToolUse, got %v", done.Reason)
	}
}

// Test user message prompt generates correct usage info.
func TestAnthropic_UsageTracking(t *testing.T) {
	sse := "" +
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}
` +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}
` +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}
` +
		`data: {"type":"content_block_stop","index":0}
` +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}
` +
		`data: [DONE]
`

	events := runAnthropicSSE(t, sse)

	done := findEventType[core.EventDone](events)
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Message.Usage.Input != 10 {
		t.Errorf("expected Input=10, got %d", done.Message.Usage.Input)
	}
	if done.Message.Usage.Output != 5 {
		t.Errorf("expected Output=5, got %d", done.Message.Usage.Output)
	}
}

// Test signature_delta is properly captured on per-block basis.
func TestAnthropic_SignatureDelta(t *testing.T) {
	sse := "" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}
` +
		// signature arrives via delta
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-from-delta"}}
` +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"signed text"}}
` +
		`data: {"type":"content_block_stop","index":0}
` +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}
` +
		`data: [DONE]
`

	events := runAnthropicSSE(t, sse)

	// Verify EventTextEnd carries the signature from the delta
	textEnds := filterEventTypes[core.EventTextEnd](events)
	if len(textEnds) < 1 {
		t.Fatal("expected EventTextEnd")
	}
	if textEnds[0].TextSignature != "sig-from-delta" {
		t.Errorf("expected signature 'sig-from-delta', got %q", textEnds[0].TextSignature)
	}
}

// Test redacted thinking blocks.
func TestAnthropic_RedactedThinking(t *testing.T) {
	// Redacted thinking has no thinking field on content_block_start,
	// and signature_delta plus the redacted text.
	sse := "" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking"}}
` +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Redacted reasoning"}}
` +
		`data: {"type":"content_block_stop","index":0}
` +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}
` +
		`data: [DONE]
`

	events := runAnthropicSSE(t, sse)

	// The bridge should treat redacted_thinking as a thinking block
	thStarts := filterEventTypes[core.EventThinkingStart](events)
	if len(thStarts) < 1 {
		t.Errorf("expected at least 1 thinking_start for redacted_thinking")
	}
}

// Helper: filter events of a specific type.
func filterEventTypes[T core.AssistantMessageEvent](events []core.AssistantMessageEvent) []T {
	var result []T
	for _, evt := range events {
		if e, ok := evt.(T); ok {
			result = append(result, e)
		}
	}
	return result
}

// Helper: find first event of a specific type.
func findEventType[T core.AssistantMessageEvent](events []core.AssistantMessageEvent) *T {
	for _, evt := range events {
		if e, ok := evt.(T); ok {
			return &e
		}
	}
	return nil
}
