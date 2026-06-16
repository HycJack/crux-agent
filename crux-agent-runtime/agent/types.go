package agent

import (
	"context"
	"encoding/json"

	core "github.com/hycjack/crux-ai/core"
)

// ToolExecutionMode controls how tools are executed.
type ToolExecutionMode string

const (
	ToolExecParallel   ToolExecutionMode = "parallel"
	ToolExecSequential ToolExecutionMode = "sequential"
)

// AgentEvent is the interface for all agent streaming events.
type AgentEvent interface {
	agentEventTag()
}

// EventAgentStart signals the start of an agent run.
type EventAgentStart struct{}

func (EventAgentStart) agentEventTag() {}

// EventAgentEnd signals the end of an agent run with final messages.
type EventAgentEnd struct {
	Messages []core.Message
}

func (EventAgentEnd) agentEventTag() {}

// EventTurnStart signals the start of a turn (LLM call + tool execution).
type EventTurnStart struct{}

func (EventTurnStart) agentEventTag() {}

// EventTurnEnd signals the end of a turn.
type EventTurnEnd struct {
	Message     core.AssistantMessage
	ToolResults []core.ToolResultMessage
}

func (EventTurnEnd) agentEventTag() {}

// EventMessageStart signals the start of an assistant message stream.
type EventMessageStart struct {
	Message core.AssistantMessage
}

func (EventMessageStart) agentEventTag() {}

// EventMessageUpdate signals a delta in the assistant message stream.
type EventMessageUpdate struct {
	Message        core.AssistantMessage
	AssistantEvent core.AssistantMessageEvent
}

func (EventMessageUpdate) agentEventTag() {}

// EventMessageEnd signals the end of an assistant message stream.
type EventMessageEnd struct {
	Message core.AssistantMessage
}

func (EventMessageEnd) agentEventTag() {}

// EventToolExecStart signals the start of a tool execution.
type EventToolExecStart struct {
	ToolCallID string
	ToolName   string
	Args       json.RawMessage
}

func (EventToolExecStart) agentEventTag() {}

// EventToolExecUpdate signals a partial result update during tool execution.
type EventToolExecUpdate struct {
	ToolCallID    string
	ToolName      string
	Args          json.RawMessage
	PartialResult json.RawMessage
}

func (EventToolExecUpdate) agentEventTag() {}

// EventToolExecEnd signals the end of a tool execution.
type EventToolExecEnd struct {
	ToolCallID string
	ToolName   string
	Result     json.RawMessage
	IsError    bool
}

func (EventToolExecEnd) agentEventTag() {}

// AgentTool defines a tool that the agent can call.
type AgentTool struct {
	Name          string
	Description   string
	Parameters    json.RawMessage // JSON Schema
	Label         string
	Execute       ToolExecuteFunc
	ExecutionMode ToolExecutionMode // "" = inherit from config
}

// ToolExecuteFunc is the function signature for tool execution.
type ToolExecuteFunc func(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (AgentToolResult, error)

// AgentToolResult is the result of a tool execution.
type AgentToolResult struct {
	Content   []core.ContentBlock
	Details   json.RawMessage
	IsError   bool
	Terminate bool
}

// BeforeToolCallContext is passed to the beforeToolCall hook.
type BeforeToolCallContext struct {
	AssistantMessage core.AssistantMessage
	ToolCall         core.ToolCall
	Args             json.RawMessage
	Messages         []core.Message
}

// ToolCallBlock is returned by beforeToolCall to block execution.
type ToolCallBlock struct {
	Block  bool
	Reason string
}

// AfterToolCallContext is passed to the afterToolCall hook.
type AfterToolCallContext struct {
	AssistantMessage core.AssistantMessage
	ToolCall         core.ToolCall
	Args             json.RawMessage
	Result           AgentToolResult
	IsError          bool
	Messages         []core.Message
}

// ToolCallOverride is returned by afterToolCall to override the result.
type ToolCallOverride struct {
	Content   []core.ContentBlock
	Details   json.RawMessage
	IsError   *bool
	Terminate *bool
}

// StreamFn is the type for custom streaming functions.
type StreamFn func(context.Context, core.Model, core.Context, core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error)

// AgentLoopConfig configures the agent loop.
type AgentLoopConfig struct {
	core.SimpleStreamOptions

	Model         core.Model
	SystemPrompt  string
	Tools         []AgentTool
	ToolExecution ToolExecutionMode

	// ConvertToLlm transforms messages before each LLM call.
	// If nil, default conversion (filter to user/assistant/toolResult) is used.
	ConvertToLlm func([]core.Message) []core.Message

	// TransformContext transforms messages for context window management.
	// If nil, default context management with automatic compaction is used.
	TransformContext func([]core.Message) []core.Message

	// GetApiKey resolves the API key dynamically (e.g., for expiring OAuth tokens).
	GetApiKey func() string

	// ShouldStopAfterTurn is called after each turn. Return true to stop the loop.
	ShouldStopAfterTurn func(core.AssistantMessage, []core.ToolResultMessage) bool

	// PrepareNextTurn is called after each turn. Can modify config for the next turn.
	PrepareNextTurn func(config *AgentLoopConfig, assistantMsg core.AssistantMessage, toolResults []core.ToolResultMessage, messages []core.Message)

	// GetSteeringMessages returns messages injected mid-run while tools are executing.
	GetSteeringMessages func() []core.Message

	// GetFollowUpMessages returns messages injected after the agent would otherwise stop.
	GetFollowUpMessages func() []core.Message

	// BeforeToolCall is called before tool execution. Can block execution.
	BeforeToolCall func(BeforeToolCallContext) *ToolCallBlock

	// AfterToolCall is called after tool execution. Can override result.
	AfterToolCall func(AfterToolCallContext) *ToolCallOverride

	// StreamFn is a custom streaming function. If nil, crux-ai StreamSimple is used.
	StreamFn StreamFn

	// Compaction configures automatic context compaction.
	Compaction *core.CompactionConfig
	// Compactor is the compaction strategy.
	Compactor core.Compactor
}

// findTool looks up a tool by name.
func findTool(tools []AgentTool, name string) *AgentTool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

// defaultConvertToLlm filters messages to LLM-compatible types.
func defaultConvertToLlm(msgs []core.Message) []core.Message {
	result := make([]core.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.(type) {
		case core.UserMessage, core.AssistantMessage, core.ToolResultMessage:
			result = append(result, m)
		}
	}
	return result
}

// extractToolCalls extracts tool calls from an assistant message.
func extractToolCalls(msg core.AssistantMessage) []core.ToolCall {
	var calls []core.ToolCall
	for _, block := range msg.Content {
		if tc, ok := block.(core.ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}

// toContextMessages converts messages to a context for LLM calls.
func toContextMessages(msgs []core.Message, systemPrompt string, tools []AgentTool) core.Context {
	llmTools := make([]core.Tool, len(tools))
	for i, t := range tools {
		llmTools[i] = core.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}
	return core.Context{
		SystemPrompt: systemPrompt,
		Messages:     msgs,
		Tools:        llmTools,
	}
}
