package anthropic

import (
	"strings"
	"testing"

	core "crux-ai/core"
)

// runAnthropicSSE drives processSSEStream with the supplied SSE payload and
// returns all pushed events in order plus the final message.
func runAnthropicSSE(t *testing.T, sseData string) ([]core.AssistantMessageEvent, core.AssistantMessage) {
	t.Helper()
	model := core.Model{ID: "claude-test", Provider: "anthropic", API: "anthropic-messages"}
	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()

	var events []core.AssistantMessageEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		for evt := range stream.Events() {
			if evt.Err() != nil {
				continue
			}
			events = append(events, evt.Value())
			if evt.Done() {
				return
			}
		}
	}()

	r := strings.NewReader(sseData)
	out, err := processSSEStream(r, stream, model, core.StreamOptions{})
	if err != nil {
		t.Fatalf("processSSEStream: %v", err)
	}
	stream.End(out)
	<-done
	return events, out
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
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}
` +
		`data: [DONE]
`

	_, msg := runAnthropicSSE(t, sse)

	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 text blocks, got %d (%+v)", len(msg.Content), msg.Content)
	}
	t0, ok := msg.Content[0].(core.TextContent)
	if !ok {
		t.Fatalf("block 0: not TextContent, got %T", msg.Content[0])
	}
	t1, ok := msg.Content[1].(core.TextContent)
	if !ok {
		t.Fatalf("block 1: not TextContent, got %T", msg.Content[1])
	}
	if t0.Text != "Hello " {
		t.Errorf("block 0 text: got %q", t0.Text)
	}
	if t0.TextSignature != "sig-A" {
		t.Errorf("block 0 sig: got %q, want sig-A", t0.TextSignature)
	}
	if t1.Text != "World" {
		t.Errorf("block 1 text: got %q", t1.Text)
	}
	if t1.TextSignature != "sig-B" {
		t.Errorf("block 1 sig: got %q, want sig-B (must not be overwritten by block 0)", t1.TextSignature)
	}
}

func TestAnthropic_ThinkingAndText_Coexist(t *testing.T) {
	sse := "" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","signature":"think-sig"}}
` +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reasoning"}}
` +
		`data: {"type":"content_block_stop","index":0}
` +
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","signature":"text-sig"}}
` +
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"final answer"}}
` +
		`data: {"type":"content_block_stop","index":1}
` +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}
` +
		`data: [DONE]
`

	_, msg := runAnthropicSSE(t, sse)
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(msg.Content))
	}
	th, ok := msg.Content[0].(core.ThinkingContent)
	if !ok {
		t.Fatalf("block 0: not ThinkingContent, got %T", msg.Content[0])
	}
	if th.ThinkingSignature != "think-sig" {
		t.Errorf("thinking sig: got %q", th.ThinkingSignature)
	}
	tx, ok := msg.Content[1].(core.TextContent)
	if !ok {
		t.Fatalf("block 1: not TextContent, got %T", msg.Content[1])
	}
	if tx.TextSignature != "text-sig" {
		t.Errorf("text sig: got %q, want text-sig (not think-sig)", tx.TextSignature)
	}
}

func TestAnthropic_SignatureDelta_StoresPerIndex(t *testing.T) {
	// Some Anthropic responses deliver the signature as a separate
	// signature_delta event rather than embedded in the block.
	sse := "" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}
` +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"..."}}
` +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"late-sig"}}
` +
		`data: {"type":"content_block_stop","index":0}
` +
		`data: [DONE]
`

	_, msg := runAnthropicSSE(t, sse)
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(msg.Content))
	}
	th, ok := msg.Content[0].(core.ThinkingContent)
	if !ok {
		t.Fatalf("not ThinkingContent, got %T", msg.Content[0])
	}
	if th.ThinkingSignature != "late-sig" {
		t.Errorf("late signature: got %q, want late-sig", th.ThinkingSignature)
	}
}

// When the stream ends mid-block (no content_block_stop), the per-block
// drain should still produce a content entry with the captured signature.
func TestAnthropic_TruncatedStream_DrainsBuffers(t *testing.T) {
	sse := "" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","signature":"trunc-sig"}}
` +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}
` +
		// No content_block_stop, no [DONE].
		""

	_, msg := runAnthropicSSE(t, sse)
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 drained block, got %d", len(msg.Content))
	}
	tc, ok := msg.Content[0].(core.TextContent)
	if !ok || tc.Text != "partial" {
		t.Errorf("drained text: got %+v", msg.Content[0])
	}
	if tc.TextSignature != "trunc-sig" {
		t.Errorf("drained sig: got %q, want trunc-sig", tc.TextSignature)
	}
}
