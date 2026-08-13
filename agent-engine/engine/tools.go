package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	core "github.com/hycjack/crux-ai/core"
)

// executeToolCalls dispatches tool calls in parallel or sequential mode.
//
// Note: In parallel mode, if a tool returns Terminate=true, the function
// records termination but does NOT cancel other in-flight goroutines.
// All started tools complete; the agent loop stops after this turn.
// This differs from sequential mode where Terminate causes an immediate break.
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

// ─── Sequential execution ───────────────────────────────────────────────────

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

// ─── Parallel execution ─────────────────────────────────────────────────────

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
				ch <- indexedResult{
					index: idx,
					result: core.ToolResultMessage{
						Role: core.MessageRoleTool, ToolCallID: toolCall.ID, ToolName: toolCall.Name,
						Content: []core.ContentBlock{
							core.TextContent{Type: "text", Text: "Tool call cancelled"},
						},
						IsError: true,
					},
				}
				return
			}
			result, resultMsg := executeSingleToolCall(ctx, config, assistantMsg, toolCall, messages, stream)
			ch <- indexedResult{index: idx, result: resultMsg, terminate: result.Terminate}
		}(i, tc)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	terminated := false
	for r := range ch {
		results[r.index] = r.result
		if r.terminate {
			terminated = true
		}
	}
	return results, terminated
}

// ─── Single tool call ───────────────────────────────────────────────────────

func executeSingleToolCall(ctx context.Context, config AgentLoopConfig, assistantMsg core.AssistantMessage, tc core.ToolCall, messages []core.Message, stream *AgentEventStream) (result AgentToolResult, resultMsg core.ToolResultMessage) {
	defer func() {
		if r := recover(); r != nil {
			errText := fmt.Sprintf("panic in tool execution: %v", r)
			result = AgentToolResult{
				Content: []core.ContentBlock{core.TextContent{Type: "text", Text: errText}},
				IsError: true,
			}
			resultMsg = core.ToolResultMessage{
				Role: core.MessageRoleTool, ToolCallID: tc.ID, ToolName: tc.Name,
				Content: result.Content, IsError: true,
			}
			stream.Push(EventToolExecEnd{
				ToolCallID: tc.ID, ToolName: tc.Name,
				Result:     json.RawMessage(fmt.Sprintf(`{"error":%q}`, errText)),
				IsError:    true,
			})
		}
	}()
	stream.Push(EventToolExecStart{
		ToolCallID: tc.ID, ToolName: tc.Name, Args: tc.Arguments,
	})

	result, _ = prepareAndExecuteToolCall(ctx, config, assistantMsg, tc, messages, stream)
	result = finalizeToolCall(config, assistantMsg, tc, messages, result)

	resultJSON, _ := json.Marshal(result)
	stream.Push(EventToolExecEnd{
		ToolCallID: tc.ID, ToolName: tc.Name, Result: resultJSON, IsError: result.IsError,
	})

	resultMsg = core.ToolResultMessage{
		Role: core.MessageRoleTool, ToolCallID: tc.ID, ToolName: tc.Name,
		Content: result.Content, IsError: result.IsError,
	}
	return
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
