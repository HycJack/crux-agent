package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"crux-agent-tui/internal/provider"
)

const maxAgentRounds = 50

// runLoop runs the agent's main loop: LLM call → tool execution → LLM call...
func (a *Agent) runLoop(ctx context.Context) error {
	for round := 0; round < maxAgentRounds; round++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Compact context if needed
		a.maybeCompact()

		// Build LLM context
		llmCtx := a.buildLLMContext()

		// Stream the response
		content, toolCalls, stopReason, err := a.streamResponse(ctx, llmCtx)
		if err != nil {
			a.PublishEvent(EventTurnEnd{ErrorMessage: err.Error()})
			return err
		}

		// Build assistant message
		assistantMsg := provider.Message{
			Role:       provider.RoleAssistant,
			Content:    content,
			StopReason: stopReason,
			ToolCalls:  toolCalls,
		}
		a.state.Messages = append(a.state.Messages, assistantMsg)

		// Check for terminal stops
		if stopReason == provider.StopError || stopReason == provider.StopAborted {
			a.PublishEvent(EventTurnEnd{ErrorMessage: assistantMsg.ErrorMessage})
			return nil
		}

		// If no tool calls, we're done
		if len(toolCalls) == 0 {
			a.PublishEvent(EventTurnEnd{})
			return nil
		}

		// Execute tool calls
		for _, tc := range toolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}

			a.PublishEvent(EventToolExecStart{
				ToolName: tc.Name,
				Args:     truncate(string(tc.Arguments), 300),
				ToolID:   tc.ID,
			})

			result := a.executeTool(ctx, tc)

			status := "[ok]"
			if result.IsError {
				status = "[err]"
			}
			a.PublishEvent(EventToolExecEnd{
				ToolName: tc.Name,
				Result:   fmt.Sprintf("%s %s -> %s", status, tc.Name, truncate(result.Content, 500)),
				IsError:  result.IsError,
				ToolID:   tc.ID,
			})

			// Add tool result message
			a.state.Messages = append(a.state.Messages, provider.Message{
				Role:       provider.RoleTool,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Content:    result.Content,
				IsError:    result.IsError,
			})
		}
	}

	return fmt.Errorf("agent: exceeded max rounds (%d)", maxAgentRounds)
}

// streamResponse sends the request to the LLM and collects the response.
func (a *Agent) streamResponse(ctx context.Context, llmCtx provider.LLMContext) (content string, toolCalls []provider.ToolCallContent, stopReason provider.StopReason, err error) {
	opts := provider.StreamOptions{
		APIKey:    a.state.APIKey,
		BaseURL:   a.state.BaseURL,
		Model:     a.state.Model,
		MaxTokens: a.state.MaxTokens,
		Headers:   a.state.Headers,
	}

	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 4096
	}

	stream, err := a.provider.Stream(ctx, llmCtx, opts)
	if err != nil {
		return "", nil, provider.StopError, fmt.Errorf("llm call: %w", err)
	}

	// Buffer for tool call arguments (indexed by ID)
	toolCallBuf := make(map[string]*toolCallBuffer)
	var contentBuf strings.Builder

	_, err = stream.ForEach(ctx, func(evt provider.StreamEvent) error {
		switch e := evt.(type) {
		case provider.EventTextDelta:
			contentBuf.WriteString(e.Delta)
			a.PublishEvent(EventMessageUpdate{Type: "text", Delta: e.Delta})

		case provider.EventToolCallStart:
			toolCallBuf[e.ID] = &toolCallBuffer{id: e.ID, name: e.Name, args: &strings.Builder{}}

		case provider.EventToolCallDelta:
			if buf, ok := toolCallBuf[e.ID]; ok {
				buf.args.WriteString(e.Delta)
			}

		case provider.EventToolCallEnd:
			// Already handled by EventDone

		case provider.EventDone:
			stopReason = e.StopReason

			// Build tool calls from buffer
			for _, buf := range toolCallBuf {
				rawArgs := json.RawMessage(buf.args.String())
				toolCalls = append(toolCalls, provider.ToolCallContent{
					Type:      "toolCall",
					ID:        buf.id,
					Name:      buf.name,
					Arguments: rawArgs,
				})
			}
		}
		return nil
	})
	if err != nil {
		return "", nil, provider.StopError, err
	}

	return contentBuf.String(), toolCalls, stopReason, nil
}

type toolCallBuffer struct {
	id   string
	name string
	args *strings.Builder
}

func (a *Agent) executeTool(ctx context.Context, tc provider.ToolCallContent) AgentToolResult {
	// Find the tool
	tool := a.findTool(tc.Name)
	if tool == nil {
		return AgentToolResult{
			Content: fmt.Sprintf("Tool not found: %s", tc.Name),
			IsError: true,
		}
	}

	onUpdate := func(partial json.RawMessage) {
		// Partial updates not needed for TUI streaming
	}

	result, err := tool.Execute(ctx, tc.ID, tc.Arguments, onUpdate)
	if err != nil {
		return AgentToolResult{
			Content: fmt.Sprintf("Tool error: %v", err),
			IsError: true,
		}
	}
	return result
}

func (a *Agent) findTool(name string) *AgentTool {
	for i := range a.state.Tools {
		if a.state.Tools[i].Name == name {
			return &a.state.Tools[i]
		}
	}
	return nil
}

// buildLLMContext creates an LLMContext from the agent state.
func (a *Agent) buildLLMContext() provider.LLMContext {
	tools := make([]provider.Tool, len(a.state.Tools))
	for i, t := range a.state.Tools {
		tools[i] = provider.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}

	return provider.LLMContext{
		SystemPrompt: a.state.SystemPrompt,
		Messages:     a.state.Messages,
		Tools:        tools,
	}
}

// maybeCompact compacts the message history if it's too long.
func (a *Agent) maybeCompact() {
	if a.compaction.MaxTokens <= 0 {
		return
	}

	// Simple token estimation: ~4 chars per token
	estimatedTokens := 0
	for _, msg := range a.state.Messages {
		estimatedTokens += len(msg.Content) / 4
		estimatedTokens += a.compaction.TokenBudget
	}

	if estimatedTokens <= a.compaction.MaxTokens {
		return
	}

	// Simple sliding window: keep first 2 messages (system-ish) and last N
	if len(a.state.Messages) > 10 {
		keep := 8
		preserved := make([]provider.Message, 0, keep+2)

		// Keep first user message for context
		if len(a.state.Messages) > 2 {
			preserved = append(preserved, a.state.Messages[0])
		}

		// Keep last N messages
		start := len(a.state.Messages) - keep
		if start < 0 {
			start = 0
		}
		preserved = append(preserved, a.state.Messages[start:]...)

		a.state.Messages = preserved
	}
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
