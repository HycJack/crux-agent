// Package plugin hooks.go — 统一扩展主干（Top-Level Hooks）。
//
// 这是 crux 的「唯一扩展主干」。engine.AgentLoopConfig 散落的 12 个 func 字段
// 在此收敛为一组命名钩子（Hooks），engine 只消费 Hooks；每个能力插件
// （approval/context/session/memory/…）通过 adapt.go 的 MapXxx 把自己的能力
// 挂到 Hooks 或事件轴上，从而消除「func 字段 vs 接口」两套并行扩展机制。
//
// 本文件只依赖 crux-ai/core，不依赖 agent-engine 任何类型，保证 plugin
// 作为契约层可被任何实现方反向依赖。

package plugin

import (
	"context"
	"encoding/json"

	core "github.com/hycjack/crux-ai/core"
)

// ─── 工具结果 ────────────────────────────────────────────────────────────────

// AgentToolResult 是一次工具调用的返回。由 engine 执行工具后产生，
// 经 Hooks.AfterToolCall 可被覆写。
type AgentToolResult struct {
	Content   []core.ContentBlock
	Details   json.RawMessage
	IsError   bool
	Terminate bool
}

// ─── 工具钩子上下文 ──────────────────────────────────────────────────────────

// BeforeToolCallCtx 传给 Hooks.BeforeToolCall，用于在工具执行前拦截/放行。
type BeforeToolCallCtx struct {
	AssistantMessage core.AssistantMessage
	ToolCall         core.ToolCall
	Args             json.RawMessage
	Messages         []core.Message
}

// ToolCallBlock 是 BeforeToolCall 的返回，Block=true 时阻止执行。
type ToolCallBlock struct {
	Block  bool
	Reason string
}

// AfterToolCallCtx 传给 Hooks.AfterToolCall，用于在执行后覆写结果。
type AfterToolCallCtx struct {
	AssistantMessage core.AssistantMessage
	ToolCall         core.ToolCall
	Args             json.RawMessage
	Result           AgentToolResult
	IsError          bool
	Messages         []core.Message
}

// ToolCallOverride 是 AfterToolCall 的返回，任选字段覆写结果。
type ToolCallOverride struct {
	Content   []core.ContentBlock
	Details   json.RawMessage
	IsError   *bool
	Terminate *bool
}

// ─── 流式函数签名 ────────────────────────────────────────────────────────────

// StreamFn 是自定义流函数。nil 时 engine 用 crux-ai 默认 StreamSimpleWithContext。
type StreamFn func(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error)

// ProviderStreamFn 产生 ProviderEvent，engine 自动 canonicalize 为 AssistantMessageEvent。
// 与 StreamFn 互斥，ProviderStreamFn 优先。
type ProviderStreamFn func(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.ProviderEventStream, error)

// ─── 压缩钩子 ────────────────────────────────────────────────────────────────

// CompactionHooks 配置自动上下文压缩。engine 在两处调用：
//  1. 每次 LLM 调用前：估算 token 超预算则压缩；
//  2. LLM 返回 context-overflow 错误后：强制压缩并重试（至多 OverflowRetries 次）。
type CompactionHooks struct {
	// Compactor 执行实际压缩。nil 时不进行调用前压缩。
	Compactor func(ctx context.Context, msgs []core.Message) (newMsgs []core.Message, changed bool, err error)

	// TokenCounter 估算一次完整请求的 token。nil 时用简单字符计数兜底。
	TokenCounter func(systemPrompt string, messages []core.Message, tools []core.Tool) int

	// MaxTokens 是软预算，估算超出时触发压缩。默认 100000。
	MaxTokens int

	// ReserveTokens 为模型回复预留、不计入预算。默认 4096。
	ReserveTokens int

	// OverflowRetries 是 context-overflow 后的重试次数，每次重试前强制压缩。默认 1。
	OverflowRetries int

	// OnCompact 每次压缩后回调，用于遥测/日志 (prevTokens, newTokens, prevMsgs, newMsgs)。
	OnCompact func(prevTokens, newTokens, prevMsgs, newMsgs int)
}

// ─── 轮次钩子上下文 ──────────────────────────────────────────────────────────

// PrepareTurnCtx 传给 Hooks.PrepareNextTurn。内含 *Hooks 引用，插件可在
// 一个 turn 结束后改写下一个 turn 的扩展点（如动态改审批策略）。
type PrepareTurnCtx struct {
	Hooks            *Hooks
	AssistantMessage core.AssistantMessage
	ToolResults      []core.ToolResultMessage
	Messages         []core.Message
}

// ─── 顶部钩子（唯一扩展主干） ────────────────────────────────────────────────

// Hooks 聚合 agent 循环的全部可组合扩展点。engine 只消费 Hooks；
// defaults 的插件经 ctx.Mount 往这里挂载；不再散落在 AgentLoopConfig 上。
type Hooks struct {
	// ConvertToLlm 在每次 LLM 调用前转换消息。nil 用默认过滤。
	ConvertToLlm func([]core.Message) []core.Message

	// TransformContext 转换消息做上下文管理。nil 用默认（配合压缩）。
	TransformContext func([]core.Message) []core.Message

	// GetApiKey 动态解析 API key（如 OAuth 过期 token）。
	GetApiKey func() string

	// ShouldStopAfterTurn 每个 turn 后调用，true 则停止循环。
	ShouldStopAfterTurn func(core.AssistantMessage, []core.ToolResultMessage) bool

	// PrepareNextTurn 每个 turn 后调用，可改写下一 turn 的 Hooks。
	PrepareNextTurn func(*PrepareTurnCtx)

	// GetSteeringMessages 返回工具执行期间注入的消息。
	GetSteeringMessages func() []core.Message

	// GetFollowUpMessages 返回 agent 将停止时注入的消息。
	GetFollowUpMessages func() []core.Message

	// BeforeToolCall 工具执行前调用，可拦截。
	BeforeToolCall func(BeforeToolCallCtx) *ToolCallBlock

	// AfterToolCall 工具执行后调用，可覆写结果。
	AfterToolCall func(AfterToolCallCtx) *ToolCallOverride

	// StreamFn 自定义流。nil 用 crux-ai 默认。
	StreamFn StreamFn

	// ProviderStreamFn 产生 ProviderEvent 的流。与 StreamFn 互斥。
	ProviderStreamFn ProviderStreamFn

	// Compaction 自动上下文压缩配置。
	Compaction CompactionHooks
}
