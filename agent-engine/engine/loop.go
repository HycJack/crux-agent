package engine

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hycjack/crux-ai/ai"
	core "github.com/hycjack/crux-ai/core"
)

// AgentEventStream is the event stream type for agent runs.
type AgentEventStream = core.EventStream[AgentEvent, []core.Message]

// MaxAgentRounds is the default maximum number of inner-loop rounds
// before the agent forces a stop. Use 0 for unlimited.
const MaxAgentRounds = 50

// ─── AgentLoop entry points ─────────────────────────────────────────────────

// AgentLoop starts a new agent run with the given prompt messages.
func AgentLoop(ctx context.Context, msgs []core.Message, config AgentLoopConfig) *AgentEventStream {
	stream := core.NewEventStream[AgentEvent, []core.Message]()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				stream.Error(fmt.Errorf("agent: panic: %v", r))
			}
		}()

		stream.Push(EventAgentStart{})

		messages := make([]core.Message, len(msgs))
		copy(messages, msgs)

		runLoop(ctx, config, messages, stream)
	}()

	return stream
}

// AgentLoopContinue resumes an agent run from existing context.
// The last message must be a user or toolResult message.
func AgentLoopContinue(ctx context.Context, config AgentLoopConfig, messages []core.Message) *AgentEventStream {
	stream := core.NewEventStream[AgentEvent, []core.Message]()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				stream.Error(fmt.Errorf("agent: panic: %v", r))
			}
		}()

		stream.Push(EventAgentStart{})
		runLoop(ctx, config, messages, stream)
	}()

	return stream
}

// ─── Outer loop ─────────────────────────────────────────────────────────────

// runLoop is the top-level dispatcher. It runs the inner loop until
// the LLM stops calling tools (or hits an error), then checks for
// follow-up messages.
func runLoop(ctx context.Context, config AgentLoopConfig, messages []core.Message, stream *AgentEventStream) {
	for {
		if err := ctx.Err(); err != nil {
			log.Printf("agent: loop exiting - context cancelled: %v", err)
			stream.End(messages)
			return
		}

		hasMoreTurns, updatedMsgs := runInnerLoop(ctx, config, messages, stream)
		messages = updatedMsgs
		if !hasMoreTurns {
			stream.End(messages)
			return
		}

		var hasFollowUp bool
		messages, hasFollowUp = injectFollowUpMessages(&config, messages)
		if hasFollowUp {
			stream.Push(EventQueueUpdate{FollowUpCount: 1})
		} else {
			log.Printf("agent: loop exiting - no follow-up messages")
			stream.End(messages)
			return
		}
	}
}

// ─── Inner loop ─────────────────────────────────────────────────────────────

// runInnerLoop runs model turns until the LLM stops calling tools.
func runInnerLoop(ctx context.Context, config AgentLoopConfig, messages []core.Message, stream *AgentEventStream) (bool, []core.Message) {
	for round := 0; round < MaxAgentRounds; round++ {
		if err := ctx.Err(); err != nil {
			return false, messages
		}

		var hasSteering bool
		messages, hasSteering = injectSteeringMessages(&config, messages)
		if hasSteering {
			stream.Push(EventQueueUpdate{SteeringCount: 1})
		}
		stream.Push(EventTurnStart{})

		assistantMsg, trimmedMsgs, err := streamAssistantResponse(ctx, config, messages, stream)
		if err != nil {
			return handleStreamError(err, messages, stream)
		}
		messages = trimmedMsgs
		messages = append(messages, assistantMsg)

		if isTerminalStop(assistantMsg.StopReason) {
			stream.Push(EventTurnEnd{Message: assistantMsg})
			return false, messages
		}

		toolCalls := extractToolCalls(assistantMsg)
		var toolResults []core.ToolResultMessage
		shouldTerminate := false
		if len(toolCalls) > 0 {
			toolResults, shouldTerminate = executeToolCalls(ctx, config, assistantMsg, toolCalls, messages, stream)
			messages = append(messages, msgSlice(toolResults)...)
		}

		stream.Push(EventTurnEnd{Message: assistantMsg, ToolResults: toolResults})

		if shouldTerminate {
			return false, messages
		}
		if config.PrepareNextTurn != nil {
			config.PrepareNextTurn(&config, assistantMsg, toolResults, messages)
		}
		if config.ShouldStopAfterTurn != nil && config.ShouldStopAfterTurn(assistantMsg, toolResults) {
			return false, messages
		}

		if len(toolCalls) == 0 && !hasSteering {
			return false, messages
		}
	}
	log.Printf("agent: inner loop hit MaxAgentRounds=%d", MaxAgentRounds)
	return true, messages
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func handleStreamError(err error, messages []core.Message, stream *AgentEventStream) (bool, []core.Message) {
	log.Printf("agent: streamAssistantResponse error: %v", err)
	errMsg := core.AssistantMessage{
		Role:         core.MessageRoleAssistant,
		StopReason:   core.StopError,
		ErrorMessage: err.Error(),
	}
	messages = append(messages, errMsg)
	stream.Push(EventTurnEnd{Message: errMsg})
	return false, messages
}

func isTerminalStop(reason core.StopReason) bool {
	return reason == core.StopError || reason == core.StopAborted
}

func injectSteeringMessages(config *AgentLoopConfig, messages []core.Message) ([]core.Message, bool) {
	if config.GetSteeringMessages == nil {
		return messages, false
	}
	steering := config.GetSteeringMessages()
	if len(steering) == 0 {
		return messages, false
	}
	return append(messages, steering...), true
}

func injectFollowUpMessages(config *AgentLoopConfig, messages []core.Message) ([]core.Message, bool) {
	if config.GetFollowUpMessages == nil {
		return messages, false
	}
	followUp := config.GetFollowUpMessages()
	if len(followUp) == 0 {
		return messages, false
	}
	return append(messages, followUp...), true
}

func transformContext(ctx context.Context, config AgentLoopConfig, messages []core.Message) []core.Message {
	if config.TransformContext != nil {
		return config.TransformContext(messages)
	}
	return messages
}

func convertToLLM(config AgentLoopConfig, messages []core.Message) []core.Message {
	if config.ConvertToLlm != nil {
		return config.ConvertToLlm(messages)
	}
	return defaultConvertToLlm(messages)
}

func resolveStreamOptions(config *AgentLoopConfig) core.SimpleStreamOptions {
	opts := config.SimpleStreamOptions
	if config.GetApiKey != nil {
		if key := config.GetApiKey(); key != "" {
			opts.APIKey = key
		}
	}
	// Write back so the caller sees the resolved options
	config.SimpleStreamOptions = opts
	return opts
}

func invokeStreamFn(ctx context.Context, config AgentLoopConfig, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	// ProviderStreamFn takes priority: produce ProviderEvent, canonicalize to AssistantMessageEvent
	if config.ProviderStreamFn != nil {
		providerStream, err := config.ProviderStreamFn(ctx, config.Model, llmCtx, opts)
		if err != nil {
			return nil, err
		}
		// CanonicalizeProviderStream translates ProviderEvent (simple deltas) →
		// AssistantMessageEvent (Start/Delta/End protocol) with automatic
		// content_index management and block boundary detection.
		return core.CanonicalizeProviderStream(
			providerStream,
			config.Model.API,
			config.Model.Provider,
			config.Model.ID,
		), nil
	}

	if config.StreamFn != nil {
		return config.StreamFn(ctx, config.Model, llmCtx, opts)
	}
	return ai.StreamSimpleWithContext(ctx, config.Model, llmCtx, opts)
}

func consumeStreamEvents(ctx context.Context, llmStream *core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], stream *AgentEventStream) (core.AssistantMessage, error) {
	partialMsg := core.AssistantMessage{
		Role:      core.MessageRoleAssistant,
		Timestamp: time.Now(),
	}
	stream.Push(EventMessageStart{Message: partialMsg})

	_, err := llmStream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		partialMsg = applyAssistantEvent(partialMsg, evt)
		stream.Push(EventMessageUpdate{Message: partialMsg, AssistantEvent: evt})
		return nil
	})
	if err != nil {
		return core.AssistantMessage{}, err
	}
	return partialMsg, nil
}

func applyAssistantEvent(msg core.AssistantMessage, evt core.AssistantMessageEvent) core.AssistantMessage {
	switch e := evt.(type) {
	case core.EventStart:
		msg.API = e.API
		msg.Provider = e.Provider
		msg.Model = e.Model
	case core.EventTextDelta:
		msg.Content = appendOrUpdateText(msg.Content, e.Delta)
	case core.EventThinkingDelta:
		msg.Content = appendOrUpdateThinking(msg.Content, e.Delta)
	case core.EventToolCallStart:
		msg.Content = append(msg.Content, core.ToolCall{
			Type: "toolCall", ID: e.ID, Name: e.Name,
		})
	case core.EventToolCallDelta:
		msg.Content = updateToolCallArgs(msg.Content, e.ID, e.ArgumentsDelta)
	case core.EventToolCallEnd:
		msg.Content = finalizeToolCallArgs(msg.Content, e.ID, e.Arguments)
	case core.EventTextEnd:
		if e.TextSignature != "" {
			msg.Content = setTextSignature(msg.Content, e.TextSignature)
		}
	case core.EventThinkingEnd:
		if e.ThinkingSignature != "" {
			msg.Content = setThinkingSignature(msg.Content, e.ThinkingSignature)
		}
	case core.EventDone:
		msg = e.Message
	}
	return msg
}

// ─── Content block helpers ──────────────────────────────────────────────────

func appendOrUpdateText(blocks []core.ContentBlock, delta string) []core.ContentBlock {
	for i := len(blocks) - 1; i >= 0; i-- {
		if tc, ok := blocks[i].(core.TextContent); ok {
			blocks[i] = core.TextContent{Type: "text", Text: tc.Text + delta, TextSignature: tc.TextSignature}
			return blocks
		}
	}
	return append(blocks, core.TextContent{Type: "text", Text: delta})
}

func appendOrUpdateThinking(blocks []core.ContentBlock, delta string) []core.ContentBlock {
	for i := len(blocks) - 1; i >= 0; i-- {
		if tc, ok := blocks[i].(core.ThinkingContent); ok {
			blocks[i] = core.ThinkingContent{
				Type: "thinking", Thinking: tc.Thinking + delta,
				ThinkingSignature: tc.ThinkingSignature,
			}
			return blocks
		}
	}
	return append(blocks, core.ThinkingContent{Type: "thinking", Thinking: delta})
}

func updateToolCallArgs(blocks []core.ContentBlock, id string, delta string) []core.ContentBlock {
	for i, block := range blocks {
		if tc, ok := block.(core.ToolCall); ok && tc.ID == id {
			newArgs := append([]byte(tc.Arguments), []byte(delta)...)
			blocks[i] = core.ToolCall{
				Type: "toolCall", ID: tc.ID, Name: tc.Name,
				Arguments: newArgs,
			}
			return blocks
		}
	}
	return blocks
}

func finalizeToolCallArgs(blocks []core.ContentBlock, id string, args []byte) []core.ContentBlock {
	for i, block := range blocks {
		if tc, ok := block.(core.ToolCall); ok && tc.ID == id {
			if len(args) > 0 {
				blocks[i] = core.ToolCall{
					Type: "toolCall", ID: tc.ID, Name: tc.Name,
					Arguments: args,
				}
			}
			return blocks
		}
	}
	return blocks
}

func setTextSignature(blocks []core.ContentBlock, sig string) []core.ContentBlock {
	for i, block := range blocks {
		if tc, ok := block.(core.TextContent); ok {
			blocks[i] = core.TextContent{Type: "text", Text: tc.Text, TextSignature: sig}
			return blocks
		}
	}
	return blocks
}

func setThinkingSignature(blocks []core.ContentBlock, sig string) []core.ContentBlock {
	for i, block := range blocks {
		if tc, ok := block.(core.ThinkingContent); ok {
			blocks[i] = core.ThinkingContent{
				Type: "thinking", Thinking: tc.Thinking,
				ThinkingSignature: sig,
			}
			return blocks
		}
	}
	return blocks
}
