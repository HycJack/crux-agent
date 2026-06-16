// Package agent provides the runtime for executing autonomous LLM
// agents. It implements a two-level event loop:
//
//   outer loop  — checks for follow-up user messages between runs
//   inner loop  — processes tool calls and steering messages
//
// All agent state is passed in as values; the package has no
// process-wide mutable state. Use AgentLoop / AgentLoopContinue to
// start a run, and consume events from the returned AgentEventStream.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/llm"
)

// AgentEventStream is the type alias for the agent event stream.
type AgentEventStream = core.EventStream[AgentEvent, []core.Message]

// MaxAgentRounds is the default maximum number of inner-loop rounds
// before the agent forces a stop. Use 0 for unlimited.
const MaxAgentRounds = 50

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

		hasMoreTurns := runInnerLoop(ctx, config, messages, stream)
		if !hasMoreTurns {
			stream.End(messages)
			return
		}

		hasFollowUp := injectFollowUpMessages(&config, messages)
		if !hasFollowUp {
			log.Printf("agent: loop exiting - no follow-up messages")
			stream.End(messages)
			return
		}
	}
}

// runInnerLoop runs model turns until the LLM stops calling tools.
func runInnerLoop(ctx context.Context, config AgentLoopConfig, messages []core.Message, stream *AgentEventStream) bool {
	for round := 0; round < MaxAgentRounds; round++ {
		if err := ctx.Err(); err != nil {
			return false
		}

		hasSteering := injectSteeringMessages(&config, messages)
		stream.Push(EventTurnStart{})

		assistantMsg, trimmedMsgs, err := streamAssistantResponse(ctx, config, messages, stream)
		if err != nil {
			return handleStreamError(err, messages, stream)
		}
		messages = trimmedMsgs
		messages = append(messages, assistantMsg)

		if isTerminalStop(assistantMsg.StopReason) {
			stream.Push(EventTurnEnd{Message: assistantMsg})
			return false
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
			return false
		}
		if config.PrepareNextTurn != nil {
			config.PrepareNextTurn(&config, assistantMsg, toolResults, messages)
		}
		if config.ShouldStopAfterTurn != nil && config.ShouldStopAfterTurn(assistantMsg, toolResults) {
			return false
		}

		if len(toolCalls) == 0 && !hasSteering {
			return true
		}
	}
	log.Printf("agent: inner loop hit MaxAgentRounds=%d", MaxAgentRounds)
	return true
}

func handleStreamError(err error, messages []core.Message, stream *AgentEventStream) bool {
	log.Printf("agent: streamAssistantResponse error: %v", err)
	errMsg := core.AssistantMessage{
		Role:         core.MessageRoleAssistant,
		StopReason:   core.StopError,
		ErrorMessage: err.Error(),
	}
	messages = append(messages, errMsg)
	stream.Push(EventTurnEnd{Message: errMsg})
	return false
}

func isTerminalStop(reason core.StopReason) bool {
	return reason == core.StopError || reason == core.StopAborted
}

func injectSteeringMessages(config *AgentLoopConfig, messages []core.Message) bool {
	if config.GetSteeringMessages == nil {
		return false
	}
	steering := config.GetSteeringMessages()
	if len(steering) == 0 {
		return false
	}
	messages = append(messages, steering...)
	return true
}

func injectFollowUpMessages(config *AgentLoopConfig, messages []core.Message) bool {
	if config.GetFollowUpMessages == nil {
		return false
	}
	followUp := config.GetFollowUpMessages()
	if len(followUp) == 0 {
		return false
	}
	messages = append(messages, followUp...)
	return true
}

func streamAssistantResponse(ctx context.Context, config AgentLoopConfig, messages []core.Message, stream *AgentEventStream) (core.AssistantMessage, []core.Message, error) {
	trimmedMessages := transformContext(ctx, config, messages)
	llmMessages := convertToLLM(config, trimmedMessages)
	opts := resolveStreamOptions(&config)
	llmCtx := toContextMessages(llmMessages, config.SystemPrompt, config.Tools)

	llmStream, err := invokeStreamFn(ctx, config, llmCtx, opts)
	if err != nil {
		return core.AssistantMessage{}, trimmedMessages, err
	}

	partialMsg, err := consumeStreamEvents(ctx, llmStream, stream)
	if err != nil {
		return core.AssistantMessage{}, trimmedMessages, err
	}

	stream.Push(EventMessageEnd{Message: partialMsg})
	return partialMsg, trimmedMessages, nil
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
	return opts
}

func invokeStreamFn(ctx context.Context, config AgentLoopConfig, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	if config.StreamFn != nil {
		return config.StreamFn(ctx, config.Model, llmCtx, opts)
	}
	return llm.StreamSimpleWithContext(ctx, config.Model, llmCtx, opts)
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

func executeToolCalls(ctx context.Context, config AgentLoopConfig, assistantMsg core.AssistantMessage, toolCalls []core.ToolCall, messages []core.Message, stream *AgentEventStream) ([]core.ToolResultMessage, bool) {
	mode := resolveExecutionMode(config, toolCalls)
	switch mode {
	case ToolExecSequential:
		return executeToolCallsSequential(ctx, config, assistantMsg, toolCalls, messages, stream)
	default:
		return executeToolCallsParallel(ctx, config, assistantMsg, toolCalls, messages, stream)
	}
}

func resolveExecutionMode(config AgentLoopConfig, toolCalls []core.ToolCall) ToolExecutionMode {
	mode := config.ToolExecution
	if mode == "" {
		mode = ToolExecParallel
	}
	for _, tc := range toolCalls {
		if tool := findTool(config.Tools, tc.Name); tool != nil && tool.ExecutionMode == ToolExecSequential {
			return ToolExecSequential
		}
	}
	return mode
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
		Role: core.MessageRoleTool, ToolCallID: tc.ID, ToolName: tc.Name,
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
