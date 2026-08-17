// Package engine provides the core agent runtime — a lightweight,
// embeddable Agent loop with event-driven architecture.
//
// The engine has zero dependencies beyond crux-ai/core. All extended
// capabilities (session persistence, context management, memory,
// auto-learning) are injected through interfaces defined in the
// companion plugin package.
//
// Architecture:
//
//	AgentLoop (outer loop: follow-up messages)
//	  └── runInnerLoop (LLM → tool → LLM)
//	       ├── ContextCompactionStage (pre-call + overflow retry)
//	       ├── LLMInvocationStage (streamAssistantResponse)
//	       └── ToolExecutionStage (parallel/sequential)
//
// The Pipeline abstraction (pipeline.go) is an alternative, stage-based
// way to compose the loop. AgentLoop() is the default implementation —
// Pipeline is a superset for advanced customization.
package engine

import (
	"encoding/json"
	stdctx "context"

	"github.com/hycjack/crux-kernel/plugin"
	core "github.com/hycjack/crux-ai/core"
)

// ToolExecutionMode controls how tools are executed.
type ToolExecutionMode string

const (
	ToolExecParallel   ToolExecutionMode = "parallel"
	ToolExecSequential ToolExecutionMode = "sequential"
)

// ─── Agent Events ──────────────────────────────────────────────────────────

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

// EventQueueUpdate signals a change in the pending queue (steering/follow-up).
type EventQueueUpdate struct {
	SteeringCount  int
	FollowUpCount  int
}

func (EventQueueUpdate) agentEventTag() {}

// EventRetry signals a retry attempt during streaming.
type EventRetry struct {
	Attempt     int
	MaxAttempts int
	DelayMs     int
	Message     string
}

func (EventRetry) agentEventTag() {}

// ─── Tool types ─────────────────────────────────────────────────────────────

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
type ToolExecuteFunc func(ctx stdctx.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (AgentToolResult, error)

// AgentToolResult is the result of a tool execution.
// 类型别名，收敛到 plugin.AgentToolResult（消除双轨定义）。
type AgentToolResult = plugin.AgentToolResult

// ─── Hook contexts for tool lifecycle ───────────────────────────────────────

// BeforeToolCallContext is passed to the beforeToolCall hook.
// 类型别名，收敛到 plugin.BeforeToolCallCtx。
type BeforeToolCallContext = plugin.BeforeToolCallCtx

// ToolCallBlock is returned by beforeToolCall to block execution.
// 类型别名，收敛到 plugin.ToolCallBlock。
type ToolCallBlock = plugin.ToolCallBlock

// AfterToolCallContext is passed to the afterToolCall hook.
// 类型别名，收敛到 plugin.AfterToolCallCtx。
type AfterToolCallContext = plugin.AfterToolCallCtx

// ToolCallOverride is returned by afterToolCall to override the result.
// 类型别名，收敛到 plugin.ToolCallOverride。
type ToolCallOverride = plugin.ToolCallOverride

// ─── Streaming function type ────────────────────────────────────────────────

// StreamFn is the type for custom streaming functions.
// 类型别名，收敛到 plugin.StreamFn。
type StreamFn = plugin.StreamFn

// ProviderStreamFn is a streaming function that produces ProviderEvent.
// 类型别名，收敛到 plugin.ProviderStreamFn。
type ProviderStreamFn = plugin.ProviderStreamFn

// ─── CompactionConfig ───────────────────────────────────────────────────────

// CompactionConfig configures automatic context-window compaction.
//
// The agent loop applies compaction in two places:
//  1. Before every LLM call: if estimated tokens exceed MaxTokens, the
//     Compactor runs and replaces messages in place.
//  2. After a context-overflow error from the LLM: force-compact and
//     retry the call up to OverflowRetries times.
//
// Both Compactor and TokenCounter are function signatures rather than
// interface types, so the engine has zero dependency on any external
// context-management package.
type CompactionConfig struct {
	// Compactor performs the actual compaction.
	// Signature: (ctx, messages) → (newMessages, changed, error).
	// If nil, no pre-call compaction is performed.
	Compactor func(ctx stdctx.Context, msgs []core.Message) (newMsgs []core.Message, changed bool, err error)

	// TokenCounter estimates token usage for a complete request.
	// If nil, a simple character-count fallback is used.
	TokenCounter func(systemPrompt string, messages []core.Message, tools []core.Tool) int

	// MaxTokens is the soft budget for tokens. When the estimated count
	// exceeds this, compaction is triggered before the LLM call.
	// Default: 100000.
	MaxTokens int

	// ReserveTokens is reserved for the LLM's response and is NOT counted
	// against the budget. Default: 4096.
	ReserveTokens int

	// OverflowRetries is the number of times to retry after a
	// context-overflow error from the LLM. Each retry force-compacts
	// before the call. Default: 1.
	OverflowRetries int

	// OnCompact is called after each compaction, for telemetry / logging.
	// (prevTokens, newTokens, prevMsgs, newMsgs)
	OnCompact func(prevTokens, newTokens, prevMsgs, newMsgs int)
}

// ─── AgentLoopConfig ────────────────────────────────────────────────────────

// AgentLoopConfig configures the agent loop.
//
// 扩展点统一收敛到 Hooks（plugin.Hooks 是唯一扩展主干）。
// 下方 12 个 func 字段为 legacy 过渡字段（Deprecated）：
// engine 内部只读 config.Hooks（经 hooks() 归一化），
// legacy 字段在 hooks() 中被复制进 Hooks，保证既有调用方零迁移成本。
// 后续阶段（P5）将删除这些 legacy 字段，收敛为 Hooks 单轨。
type AgentLoopConfig struct {
	core.SimpleStreamOptions

	Model         core.Model
	SystemPrompt  string
	Tools         []AgentTool
	ToolExecution ToolExecutionMode

	// Hooks 是唯一扩展主干。engine 内部只读此字段（经 hooks() 归一化）。
	Hooks plugin.Hooks

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

	// ProviderStreamFn is a custom streaming function that produces ProviderEvent.
	// The engine canonicalizes it to AssistantMessageEvent automatically.
	// Mutually exclusive with StreamFn (ProviderStreamFn takes priority).
	ProviderStreamFn ProviderStreamFn

	// Compaction, if set, enables automatic context-window compaction.
	// See CompactionConfig for details.
	Compaction CompactionConfig
}

// hooks 归一化 Hooks 与 legacy 字段，返回 config.Hooks 的一个副本。
//
// 规则：Hooks 字段优先；若某 Hooks 字段为 nil 而对应 legacy 字段非 nil，
// 则把 legacy 复制进 Hooks（PrepareNextTurn 已做签名桥接）。
// engine 内部一律通过 config.hooks() 读取扩展点，保证单轨读。
func (c *AgentLoopConfig) hooks() plugin.Hooks {
	h := c.Hooks
	if h.ConvertToLlm == nil && c.ConvertToLlm != nil {
		h.ConvertToLlm = c.ConvertToLlm
	}
	if h.TransformContext == nil && c.TransformContext != nil {
		h.TransformContext = c.TransformContext
	}
	if h.GetApiKey == nil && c.GetApiKey != nil {
		h.GetApiKey = c.GetApiKey
	}
	if h.ShouldStopAfterTurn == nil && c.ShouldStopAfterTurn != nil {
		h.ShouldStopAfterTurn = c.ShouldStopAfterTurn
	}
	if h.GetSteeringMessages == nil && c.GetSteeringMessages != nil {
		h.GetSteeringMessages = c.GetSteeringMessages
	}
	if h.GetFollowUpMessages == nil && c.GetFollowUpMessages != nil {
		h.GetFollowUpMessages = c.GetFollowUpMessages
	}
	if h.BeforeToolCall == nil && c.BeforeToolCall != nil {
		h.BeforeToolCall = c.BeforeToolCall
	}
	if h.AfterToolCall == nil && c.AfterToolCall != nil {
		h.AfterToolCall = c.AfterToolCall
	}
	if h.StreamFn == nil && c.StreamFn != nil {
		h.StreamFn = c.StreamFn
	}
	if h.ProviderStreamFn == nil && c.ProviderStreamFn != nil {
		h.ProviderStreamFn = c.ProviderStreamFn
	}
	if c.Compaction.Compactor != nil {
		if h.Compaction.Compactor == nil {
			h.Compaction.Compactor = c.Compaction.Compactor
		}
		if h.Compaction.TokenCounter == nil {
			h.Compaction.TokenCounter = c.Compaction.TokenCounter
		}
		if h.Compaction.MaxTokens == 0 {
			h.Compaction.MaxTokens = c.Compaction.MaxTokens
		}
		if h.Compaction.ReserveTokens == 0 {
			h.Compaction.ReserveTokens = c.Compaction.ReserveTokens
		}
		if h.Compaction.OverflowRetries == 0 {
			h.Compaction.OverflowRetries = c.Compaction.OverflowRetries
		}
		if h.Compaction.OnCompact == nil {
			h.Compaction.OnCompact = c.Compaction.OnCompact
		}
	}
	return h
}

// ─── Helper functions ───────────────────────────────────────────────────────

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

// toCoreTools converts AgentTool slice to core.Tool slice for the token counter.
func toCoreTools(tools []AgentTool) []core.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]core.Tool, len(tools))
	for i, t := range tools {
		out[i] = core.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}
	return out
}

// msgSlice converts a typed slice to []core.Message for appending.
func msgSlice[T core.Message](msgs []T) []core.Message {
	out := make([]core.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
	}
	return out
}
