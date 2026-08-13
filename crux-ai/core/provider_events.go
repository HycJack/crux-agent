// Package core defines provider-internal streaming events and the bridge that
// canonicalizes them into the public AssistantMessageEvent protocol.
//
// Design motivation (from tau_ai):
//
//	Two-layer event system:
//	  1. Provider events  — simple, atomic deltas produced by adapter parsers
//	  2. Canonical events — richly structured AssistantMessageEvent stream
//	     consumed by the agent loop
//
// The bridge (CanonicalizeProviderStream) translates layer 1 → layer 2 so
// provider adapters can emit naive deltas while consumers get a consistent
// Start/Delta/End protocol with automatic content_index management.
//
// Reference: tau_ai _provider_events.py + stream.py (Pi coding-agent harness).
package core

import (
	"encoding/json"
	"fmt"
	"time"
)

// ============================================================================
// Provider-level events (internal, emitted by adapter parsers)
//
// These are the atomic events that provider adapters produce. They are
// intentionally simpler than AssistantMessageEvent — no content_index
// tracking, no Start/End boundaries. The bridge handles those.
// ============================================================================

// ProviderEvent is the interface for all provider-internal events.
type ProviderEvent interface {
	providerTag()
}

// ProviderResponseStart signals that the provider has started a response.
type ProviderResponseStart struct {
	Type      string        `json:"type"`
	Model     string        `json:"model"`
	Timestamp time.Time     `json:"timestamp"`
}

func (ProviderResponseStart) providerTag() {}

// ProviderTextDelta carries a streamed text fragment.
type ProviderTextDelta struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

func (ProviderTextDelta) providerTag() {}

// ProviderThinkingDelta carries a streamed thinking/reasoning fragment.
type ProviderThinkingDelta struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

func (ProviderThinkingDelta) providerTag() {}

// ProviderToolCall signals a complete tool call request from the model.
type ProviderToolCall struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (ProviderToolCall) providerTag() {}

// ProviderResponseEnd signals that the provider has completed a response.
type ProviderResponseEnd struct {
	Type         string           `json:"type"`
	Message      AssistantMessage `json:"message"`
	FinishReason string           `json:"finishReason,omitempty"`
}

func (ProviderResponseEnd) providerTag() {}

// ProviderError signals a provider-level failure.
type ProviderError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (ProviderError) providerTag() {}

// ProviderContentBlockStop signals the end of a content block in the
// provider's original event stream (e.g. Anthropic's content_block_stop).
//
// When the bridge sees this, it closes the currently active block (text,
// thinking, or tool call) by emitting the corresponding End event. This
// ensures consecutive same-type blocks produce separate Start/End pairs
// even though the text/thinking deltas between them are indistinguishable.
//
// Adapters should emit this event whenever the source protocol signals a
// block boundary — e.g. on content_block_stop for Anthropic, on choosing a
// finish_reason for OpenAI, etc.
//
// TextSignature and ThinkingSignature are optional; they are passed through
// to EventTextEnd.TextSignature / EventThinkingEnd.ThinkingSignature for
// replay verification.
type ProviderContentBlockStop struct {
	Type              string `json:"type"`
	TextSignature     string `json:"textSignature,omitempty"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
}

func (ProviderContentBlockStop) providerTag() {}

// ProviderRetryEvent signals a transient failure and retry attempt.
type ProviderRetryEvent struct {
	Type         string  `json:"type"`
	Attempt      int     `json:"attempt"`
	MaxAttempts  int     `json:"maxAttempts"`
	DelaySeconds float64 `json:"delaySeconds"`
	Message      string  `json:"message"`
}

func (ProviderRetryEvent) providerTag() {}

// ============================================================================
// Type aliases
// ============================================================================

// ProviderEventStream is a stream of provider-level events. The result type R
// is unused (struct{}) since the bridge produces its own AssistantMessage.
// ProviderEventStreamResult is the (unused) result type for ProviderEventStream.
// It exists so the EventStream generic has a concrete R parameter.
type ProviderEventStreamResult struct{}

// ProviderEventStream is a stream of provider-level events. The result type R
// is unused since the bridge produces its own AssistantMessage.
type ProviderEventStream = EventStream[ProviderEvent, ProviderEventStreamResult]

// NewProviderEventStream creates a new provider event stream.
func NewProviderEventStream() *ProviderEventStream {
	return NewEventStream[ProviderEvent, ProviderEventStreamResult]()
}

// ============================================================================
// Canonicalization bridge: ProviderEvent → AssistantMessageEvent
//
// canonicalizeProviderStream reads from a ProviderEvent source and writes
// canonical AssistantMessageEvent values to the output stream. It manages:
//   - content_index tracking for text/thinking blocks
//   - TextStart/TextEnd boundary events
//   - ThinkingStart/ThinkingEnd boundary events
//   - ToolCallStart → ToolCallEnd (no delta — tool calls arrive whole)
//   - Partial message snapshots on every event
//   - Stop reason normalization
//   - Safety guard for streams that end without a terminal event
//
// Usage from a provider adapter:
//
//	func (p *MyProvider) Stream(...) (*AssistantMessageEventStream, error) {
//	    providerStream := NewProviderEventStream()
//	    go func() {
//	        // ... do HTTP, parse SSE, push ProviderResponseStart/TextDelta/etc.
//	    }()
//	    canonical := CanonicalizeProviderStream(ctx, providerStream, model, providerName, api)
//	    return canonical, nil
//	}
// ============================================================================

// CanonicalizeProviderStream reads from a provider event stream (
// ProviderEvent) and produces a fully structured AssistantMessageEvent stream
// with block-boundary events (Start/Delta/End) and automatic content_index
// management.
//
// Provider adapters should use this bridge so they can emit simple atomic
// deltas while consumers get the full Start/Delta/End protocol. Retry events
// are silently filtered out (they are provider-internal).
func CanonicalizeProviderStream(
	providerStream *ProviderEventStream,
	api KnownAPI,
	provider KnownProvider,
	model string,
) *AssistantMessageEventStream {
	out := NewEventStream[AssistantMessageEvent, AssistantMessage]()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				out.Error(panicToError(r))
			}
		}()

		partial := AssistantMessage{
			Role:     "assistant",
			API:      api,
			Provider: provider,
			Model:    model,
		}
		activeIndex := -1
		activeKind := ""
		started := false
		terminal := false

		for evt := range providerStream.Events() {
			if evt.done {
				if evt.err != nil && !terminal {
					out.Push(EventError{
						Type:         "error",
						ErrorMessage: evt.err.Error(),
					})
					out.Error(evt.err)
					terminal = true
				}
				// End signal with no error = success already handled via ProviderResponseEnd
				break
			}
			pe := evt.value

			// --- Retry events are provider-internal; skip ---
			if _, ok := pe.(ProviderRetryEvent); ok {
				continue
			}

			// --- Start event ---
			if _, ok := pe.(ProviderResponseStart); ok {
				if !started {
					started = true
					out.Push(EventStart{
						Type:      "start",
						API:       api,
						Provider:  provider,
						Model:     model,
						Timestamp: time.Now(),
					})
				}
				continue
			}

			// Auto-start: content before any start event emits start implicitly
			if !started {
				started = true
				out.Push(EventStart{
					Type:      "start",
					API:       api,
					Provider:  provider,
					Model:     model,
					Timestamp: time.Now(),
				})
			}

			switch e := pe.(type) {
			case ProviderContentBlockStop:
				endActiveBlockWithSig(&partial, activeIndex, out, e.TextSignature, e.ThinkingSignature)
				activeIndex = -1
				activeKind = ""

			case ProviderTextDelta:
				if activeKind != "text" {
					endActiveBlock(&partial, activeIndex, out)
					activeIndex = len(partial.Content)
					activeKind = "text"
					partial.Content = append(partial.Content, TextContent{Type: "text", Text: ""})
					out.Push(EventTextStart{
						Type:         "text_start",
						ContentIndex: activeIndex,
					})
				}
				tc := partial.Content[activeIndex].(TextContent)
				tc.Text += e.Delta
				partial.Content[activeIndex] = tc

				out.Push(EventTextDelta{
					Type:         "text_delta",
					ContentIndex: activeIndex,
					Delta:        e.Delta,
				})

			case ProviderThinkingDelta:
				if activeKind != "thinking" {
					endActiveBlock(&partial, activeIndex, out)
					activeIndex = len(partial.Content)
					activeKind = "thinking"
					partial.Content = append(partial.Content, ThinkingContent{Type: "thinking", Thinking: ""})
					out.Push(EventThinkingStart{
						Type:         "thinking_start",
						ContentIndex: activeIndex,
					})
				}
				tc := partial.Content[activeIndex].(ThinkingContent)
				tc.Thinking += e.Delta
				partial.Content[activeIndex] = tc

				out.Push(EventThinkingDelta{
					Type:         "thinking_delta",
					ContentIndex: activeIndex,
					Delta:        e.Delta,
				})

			case ProviderToolCall:
				endActiveBlock(&partial, activeIndex, out)
				activeIndex = -1
				activeKind = ""

				index := len(partial.Content)
				tc := ToolCall{
					Type:      "toolCall",
					ID:        e.ID,
					Name:      e.Name,
					Arguments: e.Arguments,
				}
				partial.Content = append(partial.Content, tc)

				out.Push(EventToolCallStart{
					Type:         "toolcall_start",
					ContentIndex: index,
					ID:           e.ID,
					Name:         e.Name,
				})
				out.Push(EventToolCallEnd{
					Type:         "toolcall_end",
					ContentIndex: index,
					ID:           e.ID,
					Arguments:    e.Arguments,
				})

			case ProviderResponseEnd:
				endActiveBlock(&partial, activeIndex, out)
				activeIndex = -1
				activeKind = ""

				// Build final message, preserving streamed content order
				final := e.Message
				final.API = api
				final.Provider = provider
				final.Model = model

				if len(partial.Content) > 0 {
					final.Content = append([]ContentBlock{}, partial.Content...)
				} else if len(e.Message.Content) > 0 {
					final.Content = append([]ContentBlock{}, e.Message.Content...)
				}
				copyReplayMetadata(&final, &e.Message)
				final.StopReason = normalizeStopReason(e.FinishReason, hasToolCalls(final.Content))

				out.Push(EventDone{
					Type:    "done",
					Reason:  final.StopReason,
					Message: final,
				})
				out.End(final)
				terminal = true

			case ProviderError:
				if !terminal {
					partial.StopReason = StopError
					partial.ErrorMessage = e.Message
					out.Push(EventError{
						Type:         "error",
						ErrorMessage: e.Message,
					})
					out.Error(newProviderError(e.Message))
					terminal = true
				}
			}
		}

		if !terminal {
			out.Push(EventError{
				Type:         "error",
				ErrorMessage: "Provider stream ended without a terminal event",
			})
			out.Error(newProviderError("Provider stream ended without a terminal event"))
		}
	}()

	return out
}

// endActiveBlock emits the appropriate End event for the current active block.
func endActiveBlock(partial *AssistantMessage, index int, out *AssistantMessageEventStream) {
	endActiveBlockWithSig(partial, index, out, "", "")
}

// endActiveBlockWithSig emits the End event with optional text/thinking signatures.
func endActiveBlockWithSig(partial *AssistantMessage, index int, out *AssistantMessageEventStream, textSig, thinkSig string) {
	if index < 0 || index >= len(partial.Content) {
		return
	}
	switch block := partial.Content[index].(type) {
	case TextContent:
		out.Push(EventTextEnd{
			Type:          "text_end",
			ContentIndex:  index,
			Content:       block.Text,
			TextSignature: textSig,
		})
	case ThinkingContent:
		out.Push(EventThinkingEnd{
			Type:              "thinking_end",
			ContentIndex:      index,
			Content:           block.Thinking,
			ThinkingSignature: thinkSig,
		})
	}
}

// copyReplayMetadata copies thinking_signature/text_signature from the
// provider's final message onto the streamed blocks.
func copyReplayMetadata(target, source *AssistantMessage) {
	// Copy thinking signatures by position
	sourceThinking := filterThinking(source.Content)
	targetThinking := filterThinking(target.Content)
	for i := range targetThinking {
		if i < len(sourceThinking) {
			targetThinking[i].ThinkingSignature = sourceThinking[i].ThinkingSignature
			targetThinking[i].Redacted = sourceThinking[i].Redacted
		}
	}
	// Copy text signatures by position
	sourceText := filterText(source.Content)
	targetText := filterText(target.Content)
	for i := range targetText {
		if i < len(sourceText) {
			targetText[i].TextSignature = sourceText[i].TextSignature
		}
	}
}

func filterText(blocks []ContentBlock) []TextContent {
	var out []TextContent
	for _, b := range blocks {
		if t, ok := b.(TextContent); ok {
			out = append(out, t)
		}
	}
	return out
}

func filterThinking(blocks []ContentBlock) []ThinkingContent {
	var out []ThinkingContent
	for _, b := range blocks {
		if t, ok := b.(ThinkingContent); ok {
			out = append(out, t)
		}
	}
	return out
}

// normalizeStopReason maps raw finish_reason strings to StopReason constants.
func normalizeStopReason(reason string, hasToolCalls bool) StopReason {
	if hasToolCalls {
		return StopToolUse
	}
	switch reason {
	case "tool_calls", "tool_use", "toolUse":
		return StopToolUse
	case "length", "max_tokens", "MAX_TOKENS", "incomplete":
		return StopLength
	case "stop", "end_turn", "completed":
		return StopStop
	case "error", "failed":
		return StopError
	default:
		if reason == "" {
			return StopStop
		}
		return StopStop
	}
}

// hasToolCalls checks whether any content block is a ToolCall.
func hasToolCalls(blocks []ContentBlock) bool {
	for _, b := range blocks {
		if _, ok := b.(ToolCall); ok {
			return true
		}
	}
	return false
}

// newProviderError creates a simple error for provider failures.
func newProviderError(msg string) error {
	return &providerStreamError{message: msg}
}

type providerStreamError struct {
	message string
}

func (e *providerStreamError) Error() string {
	return e.message
}

// panicToError recovers a panic value as an error.
func panicToError(r interface{}) error {
	switch v := r.(type) {
	case error:
		return v
	default:
		return &providerStreamError{message: fmt.Sprintf("panic: %v", v)}
	}
}
