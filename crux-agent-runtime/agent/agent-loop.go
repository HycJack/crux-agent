package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"crux-ai/ai"
	core "crux-ai/core"
)

// AgentEventStream is the type alias for the agent event stream.
type AgentEventStream = core.EventStream[AgentEvent, []core.Message]

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

// runLoop implements the core two-level agent loop.
func runLoop(ctx context.Context, config AgentLoopConfig, messages []core.Message, stream *AgentEventStream) {
	for {
		// Inner loop: process tool calls and steering messages
		for {
			if ctx.Err() != nil {
				stream.End(messages)
				return
			}

			// Inject steering messages before emitting turn_start
			hasSteering := false
			if config.GetSteeringMessages != nil {
				steering := config.GetSteeringMessages()
				if len(steering) > 0 {
					messages = append(messages, steering...)
					hasSteering = true
				}
			}

			stream.Push(EventTurnStart{})

			// Stream assistant response
			assistantMsg, trimmedMsgs, err := streamAssistantResponse(ctx, config, messages, stream)
			if err != nil {
				log.Printf("agent: streamAssistantResponse error: %v", err)
				errMsg := core.AssistantMessage{
					Role:         "assistant",
					StopReason:   core.StopError,
					ErrorMessage: err.Error(),
				}
				messages = append(messages, errMsg)
				stream.Push(EventTurnEnd{Message: errMsg})
				stream.End(messages)
				return
			}

			// Persist the trimmed/compacted messages so Agent.state.Messages reflects the
			// compacted state — this prevents large tool results from accumulating forever.
			messages = trimmedMsgs
			messages = append(messages, assistantMsg)

			// Check for error/aborted stop reasons
			if assistantMsg.StopReason == core.StopError || assistantMsg.StopReason == core.StopAborted {
				stream.Push(EventTurnEnd{Message: assistantMsg})
				stream.End(messages)
				return
			}

			// Extract tool calls
			toolCalls := extractToolCalls(assistantMsg)

			// Execute tool calls if any
			var toolResults []core.ToolResultMessage
			shouldTerminate := false
			if len(toolCalls) > 0 {
				toolResults, shouldTerminate = executeToolCalls(ctx, config, assistantMsg, toolCalls, messages, stream)
				messages = append(messages, msgSlice(toolResults)...)
			}

			stream.Push(EventTurnEnd{
				Message:     assistantMsg,
				ToolResults: toolResults,
			})

			// If any tool requested termination, stop the loop
			if shouldTerminate {
				stream.End(messages)
				return
			}

			// Prepare next turn hook
			if config.PrepareNextTurn != nil {
				config.PrepareNextTurn(&config, assistantMsg, toolResults, messages)
			}

			// Should stop after turn hook
			if config.ShouldStopAfterTurn != nil && config.ShouldStopAfterTurn(assistantMsg, toolResults) {
				stream.End(messages)
				return
			}

			// If no tool calls and no steering was injected, exit inner loop
			if len(toolCalls) == 0 && !hasSteering {
				break // Exit inner loop, check follow-up
			}
			// Has tool calls or had steering, continue inner loop
		}

		// Check follow-up messages (outer loop)
		hasFollowUp := false
		if config.GetFollowUpMessages != nil {
			followUp := config.GetFollowUpMessages()
			if len(followUp) > 0 {
				messages = append(messages, followUp...)
				hasFollowUp = true
			}
		}

		if !hasFollowUp {
			stream.End(messages)
			return
		}
		// Has follow-up, continue outer loop
	}
}

// streamAssistantResponse streams an LLM response and returns the final message
// together with the (possibly compacted/trimmed) messages that were actually sent to the LLM.
// The caller should use trimmedMessages to update its own message list so that
// trimming/compaction results are persisted back to Agent.state.Messages.
func streamAssistantResponse(ctx context.Context, config AgentLoopConfig, messages []core.Message, stream *AgentEventStream) (assistantMsg core.AssistantMessage, trimmedMessages []core.Message, err error) {
	// Transform context — the returned slice may be a trimmed/compacted copy.
	if config.TransformContext != nil {
		trimmedMessages = config.TransformContext(messages)
	} else {
		trimmedMessages = messages
	}

	// Convert to LLM messages
	convertFn := config.ConvertToLlm
	if convertFn == nil {
		convertFn = defaultConvertToLlm
	}
	llmMessages := convertFn(trimmedMessages)

	// Resolve API key
	apiKey := ""
	if config.GetApiKey != nil {
		apiKey = config.GetApiKey()
	}
	if apiKey == "" {
		apiKey = config.SimpleStreamOptions.APIKey
	}

	opts := config.SimpleStreamOptions
	opts.APIKey = apiKey

	// Build context
	llmCtx := toContextMessages(llmMessages, config.SystemPrompt, config.Tools)

	// Stream response
	streamFn := config.StreamFn
	if streamFn == nil {
		streamFn = func(ctx context.Context, m core.Model, c core.Context, o core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
			return ai.StreamSimpleWithContext(ctx, m, c, o)
		}
	}

	llmStream, err := streamFn(ctx, config.Model, llmCtx, opts)
	if err != nil {
		log.Printf("agent: streamFn error: %v", err)
		return core.AssistantMessage{}, trimmedMessages, err
	}
	if llmStream == nil {
		log.Printf("agent: streamFn returned nil stream")
		return core.AssistantMessage{}, trimmedMessages, fmt.Errorf("agent: stream function returned nil stream")
	}

	// Partial message for streaming updates
	var partialMsg core.AssistantMessage
	partialMsg.Role = "assistant"
	partialMsg.Timestamp = time.Now()

	stream.Push(EventMessageStart{Message: partialMsg})

	// Iterate over LLM events
	_, err = llmStream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		switch e := evt.(type) {
		case core.EventStart:
			partialMsg.API = e.API
			partialMsg.Provider = e.Provider
			partialMsg.Model = e.Model
		case core.EventTextDelta:
			partialMsg.Content = appendOrUpdateText(partialMsg.Content, e.Delta)
		case core.EventThinkingDelta:
			partialMsg.Content = appendOrUpdateThinking(partialMsg.Content, e.Delta)
		case core.EventToolCallStart:
			partialMsg.Content = append(partialMsg.Content, core.ToolCall{
				Type: "toolCall", ID: e.ID, Name: e.Name,
			})
		case core.EventToolCallDelta:
			partialMsg.Content = updateToolCallArgs(partialMsg.Content, e.ID, e.ArgumentsDelta)
		case core.EventToolCallEnd:
			partialMsg.Content = finalizeToolCallArgs(partialMsg.Content, e.ID, e.Arguments)
		case core.EventTextEnd:
			if e.TextSignature != "" {
				partialMsg.Content = setTextSignature(partialMsg.Content, e.TextSignature)
			}
		case core.EventThinkingEnd:
			if e.ThinkingSignature != "" {
				partialMsg.Content = setThinkingSignature(partialMsg.Content, e.ThinkingSignature)
			}
		case core.EventDone:
			partialMsg = e.Message
		case core.EventError:
			return fmt.Errorf("%s", e.ErrorMessage)
		}

		stream.Push(EventMessageUpdate{
			Message:        partialMsg,
			AssistantEvent: evt,
		})
		return nil
	})
	if err != nil {
		return core.AssistantMessage{}, trimmedMessages, err
	}

	// Use partialMsg which has been updated with EventDone.Message containing Usage data
	stream.Push(EventMessageEnd{Message: partialMsg})
	return partialMsg, trimmedMessages, nil
}

// executeToolCalls executes tool calls and returns the results.
func executeToolCalls(ctx context.Context, config AgentLoopConfig, assistantMsg core.AssistantMessage, toolCalls []core.ToolCall, messages []core.Message, stream *AgentEventStream) ([]core.ToolResultMessage, bool) {
	// Determine execution mode
	mode := config.ToolExecution
	if mode == "" {
		mode = ToolExecParallel
	}
	// Check if any tool requests sequential
	for _, tc := range toolCalls {
		if tool := findTool(config.Tools, tc.Name); tool != nil && tool.ExecutionMode == ToolExecSequential {
			mode = ToolExecSequential
			break
		}
	}

	if mode == ToolExecSequential {
		return executeToolCallsSequential(ctx, config, assistantMsg, toolCalls, messages, stream)
	}
	return executeToolCallsParallel(ctx, config, assistantMsg, toolCalls, messages, stream)
}

func executeToolCallsSequential(ctx context.Context, config AgentLoopConfig, assistantMsg core.AssistantMessage, toolCalls []core.ToolCall, messages []core.Message, stream *AgentEventStream) ([]core.ToolResultMessage, bool) {
	var results []core.ToolResultMessage
	shouldTerminate := false

	for _, tc := range toolCalls {
		if ctx.Err() != nil {
			break
		}
		result, resultMsg := executeSingleToolCall(ctx, config, assistantMsg, tc, messages, stream)
		results = append(results, resultMsg)
		if result.Terminate {
			shouldTerminate = true
			break
		}
	}
	return results, shouldTerminate
}

func executeToolCallsParallel(ctx context.Context, config AgentLoopConfig, assistantMsg core.AssistantMessage, toolCalls []core.ToolCall, messages []core.Message, stream *AgentEventStream) ([]core.ToolResultMessage, bool) {
	type indexedResult struct {
		index     int
		result    core.ToolResultMessage
		terminate bool
	}

	results := make([]core.ToolResultMessage, len(toolCalls))
	var wg sync.WaitGroup
	ch := make(chan indexedResult, len(toolCalls))

	for i, tc := range toolCalls {
		wg.Add(1)
		go func(idx int, toolCall core.ToolCall) {
			defer wg.Done()
			if ctx.Err() != nil {
				return
			}
			agentResult, resultMsg := executeSingleToolCall(ctx, config, assistantMsg, toolCall, messages, stream)
			ch <- indexedResult{index: idx, result: resultMsg, terminate: agentResult.Terminate}
		}(i, tc)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	shouldTerminate := false
	for r := range ch {
		results[r.index] = r.result
		if r.terminate {
			shouldTerminate = true
		}
	}
	return results, shouldTerminate
}

func executeSingleToolCall(ctx context.Context, config AgentLoopConfig, assistantMsg core.AssistantMessage, tc core.ToolCall, messages []core.Message, stream *AgentEventStream) (AgentToolResult, core.ToolResultMessage) {
	stream.Push(EventToolExecStart{
		ToolCallID: tc.ID, ToolName: tc.Name, Args: tc.Arguments,
	})

	result, _ := prepareAndExecuteToolCall(ctx, config, assistantMsg, tc, messages, stream)
	result = finalizeToolCall(config, assistantMsg, tc, messages, result)

	resultJSON, _ := json.Marshal(result)
	stream.Push(EventToolExecEnd{
		ToolCallID: tc.ID, ToolName: tc.Name, Result: resultJSON, IsError: result.IsError,
	})

	resultMsg := core.ToolResultMessage{
		Role: "tool", ToolCallID: tc.ID, ToolName: tc.Name,
		Content: result.Content, IsError: result.IsError,
	}
	return result, resultMsg
}

func prepareAndExecuteToolCall(ctx context.Context, config AgentLoopConfig, assistantMsg core.AssistantMessage, tc core.ToolCall, messages []core.Message, stream *AgentEventStream) (AgentToolResult, error) {
	tool := findTool(config.Tools, tc.Name)
	if tool == nil {
		return AgentToolResult{
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: fmt.Sprintf("Tool not found: %s", tc.Name)}},
			IsError: true,
		}, nil
	}

	if config.BeforeToolCall != nil {
		block := config.BeforeToolCall(BeforeToolCallContext{
			AssistantMessage: assistantMsg, ToolCall: tc, Args: tc.Arguments, Messages: messages,
		})
		if block != nil && block.Block {
			reason := block.Reason
			if reason == "" {
				reason = "Tool execution blocked"
			}
			return AgentToolResult{
				Content: []core.ContentBlock{core.TextContent{Type: "text", Text: reason}},
				IsError: true,
			}, nil
		}
	}

	onUpdate := func(partial json.RawMessage) {
		stream.Push(EventToolExecUpdate{
			ToolCallID: tc.ID, ToolName: tc.Name, Args: tc.Arguments, PartialResult: partial,
		})
	}

	result, err := tool.Execute(ctx, tc.ID, tc.Arguments, onUpdate)
	if err != nil {
		return AgentToolResult{
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: fmt.Sprintf("Tool execution error: %v", err)}},
			IsError: true,
		}, nil
	}
	return result, nil
}

func finalizeToolCall(config AgentLoopConfig, assistantMsg core.AssistantMessage, tc core.ToolCall, messages []core.Message, result AgentToolResult) AgentToolResult {
	if config.AfterToolCall == nil {
		return result
	}
	override := config.AfterToolCall(AfterToolCallContext{
		AssistantMessage: assistantMsg, ToolCall: tc, Args: tc.Arguments,
		Result: result, IsError: result.IsError, Messages: messages,
	})
	if override == nil {
		return result
	}
	if override.Content != nil {
		result.Content = override.Content
	}
	if override.Details != nil {
		result.Details = override.Details
	}
	if override.IsError != nil {
		result.IsError = *override.IsError
	}
	if override.Terminate != nil {
		result.Terminate = *override.Terminate
	}
	return result
}

// --- Helper functions for content manipulation ---

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
			blocks[i] = core.ThinkingContent{Type: "thinking", Thinking: tc.Thinking + delta, ThinkingSignature: tc.ThinkingSignature}
			return blocks
		}
	}
	return append(blocks, core.ThinkingContent{Type: "thinking", Thinking: delta})
}

func updateToolCallArgs(blocks []core.ContentBlock, id string, delta string) []core.ContentBlock {
	for i, block := range blocks {
		if tc, ok := block.(core.ToolCall); ok && tc.ID == id {
			blocks[i] = core.ToolCall{Type: "toolCall", ID: tc.ID, Name: tc.Name, Arguments: append(tc.Arguments, []byte(delta)...)}
			return blocks
		}
	}
	return blocks
}

func finalizeToolCallArgs(blocks []core.ContentBlock, id string, args json.RawMessage) []core.ContentBlock {
	for i, block := range blocks {
		if tc, ok := block.(core.ToolCall); ok && tc.ID == id {
			blocks[i] = core.ToolCall{Type: "toolCall", ID: tc.ID, Name: tc.Name, Arguments: args}
			return blocks
		}
	}
	return blocks
}

func setTextSignature(blocks []core.ContentBlock, sig string) []core.ContentBlock {
	for i := len(blocks) - 1; i >= 0; i-- {
		if tc, ok := blocks[i].(core.TextContent); ok {
			blocks[i] = core.TextContent{Type: "text", Text: tc.Text, TextSignature: sig}
			return blocks
		}
	}
	return blocks
}

func setThinkingSignature(blocks []core.ContentBlock, sig string) []core.ContentBlock {
	for i := len(blocks) - 1; i >= 0; i-- {
		if tc, ok := blocks[i].(core.ThinkingContent); ok {
			blocks[i] = core.ThinkingContent{Type: "thinking", Thinking: tc.Thinking, ThinkingSignature: sig}
			return blocks
		}
	}
	return blocks
}

func msgSlice(msgs []core.ToolResultMessage) []core.Message {
	result := make([]core.Message, len(msgs))
	for i, m := range msgs {
		result[i] = m
	}
	return result
}
