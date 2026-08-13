package openai

import (
	"strings"
	"testing"

	core "github.com/hycjack/crux-ai/core"
)

// runCompletionsSSE feeds SSE data through processCompletionsSSE +
// CanonicalizeProviderStream and returns all events plus the final message.
func runCompletionsSSE(t *testing.T, sseData string) ([]core.AssistantMessageEvent, core.AssistantMessage) {
	t.Helper()
	model := core.Model{ID: "gpt-test", Provider: "openai", API: "openai-completions"}
	ps := core.NewProviderEventStream()

	// Collect the bridged events
	var events []core.AssistantMessageEvent
	done := make(chan struct{})
	out := core.CanonicalizeProviderStream(ps, model.API, model.Provider, model.ID)
	go func() {
		defer close(done)
		for evt := range out.Events() {
			if evt.Done() {
				return
			}
			events = append(events, evt.Value())
		}
	}()

	r := strings.NewReader(sseData)
	err := processCompletionsSSE(r, ps, model, core.StreamOptions{})
	if err != nil {
		t.Fatalf("processCompletionsSSE: %v", err)
	}
	ps.End(core.ProviderEventStreamResult{})
	<-done

	// Extract the final message from EventDone
	var msg core.AssistantMessage
	for _, evt := range events {
		if d, ok := evt.(core.EventDone); ok {
			msg = d.Message
		}
	}
	return events, msg
}

// OpenAI's reasoning_content (o1/o3 series, o4-mini) is reasoning, NOT
// user-visible text. The previous implementation stored it as TextContent,
// which broke downstream consumers that distinguish thinking vs text by
// block type. This test guards the ThinkingContent behaviour.
func TestCompletions_Reasoning_StoredAsThinkingContent(t *testing.T) {
	sse := "" +
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"Reasoning step 1 "},"finish_reason":null}]}
` +
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"step 2"},"finish_reason":null}]}
` +
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"the answer"},"finish_reason":null}]}
` +
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
` +
		`data: [DONE]
`
	_, msg := runCompletionsSSE(t, sse)
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 blocks (thinking + text), got %d (%+v)", len(msg.Content), msg.Content)
	}
	th, ok := msg.Content[0].(core.ThinkingContent)
	if !ok {
		t.Fatalf("block 0: expected ThinkingContent, got %T (%+v)", msg.Content[0], msg.Content[0])
	}
	if th.Thinking != "Reasoning step 1 step 2" {
		t.Errorf("thinking: got %q", th.Thinking)
	}
	tc, ok := msg.Content[1].(core.TextContent)
	if !ok {
		t.Fatalf("block 1: expected TextContent, got %T", msg.Content[1])
	}
	if tc.Text != "the answer" {
		t.Errorf("text: got %q", tc.Text)
	}
}
