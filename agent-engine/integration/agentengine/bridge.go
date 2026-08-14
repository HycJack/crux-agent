package agentengine

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/crux-kernel/plugin"
	"github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-kernel/container"
	"github.com/hycjack/crux-kernel/events"
)

// AskHandler 处理 ApprovalPlugin 返回 ApprovalAsk 的情况。
//
// 当 ApprovalPlugin.Evaluate 返回 ApprovalAsk 时，桥接器调用 askHandler
// 让外部（如 CLI/UI）决定是否放行。若 askHandler 为 nil，默认阻塞。
type AskHandler func(ctx context.Context, toolName, toolID string, args []byte) (plugin.ApprovalResult, string, error)

// FromContainer 从 Container 读取服务构造 AgentOptions。
//
// 读取以下服务（全部可选，未注册则跳过）：
//   - core.Model（struct 值类型）     → InitialState.Model
//   - []engine.AgentTool              → InitialState.Tools
//   - engine.CompactionConfig         → Compaction
//
// 调用方通过 Register 注册：
//
//	c.Register(model)    // core.Model
//	c.Register(tools)    // []engine.AgentTool
//	c.Register(comp)     // engine.CompactionConfig
//
// 返回的 AgentOptions 已初始化 InitialState（非 nil），调用方可继续修改。
func FromContainer(c *container.Container) engine.AgentOptions {
	opts := engine.AgentOptions{
		InitialState: &engine.AgentState{},
	}

	// Model（接口类型，需 RegisterAs 注册）
	var model core.Model
	if err := c.Get(&model); err == nil {
		opts.InitialState.Model = model
	}

	// Tools（slice 类型）
	var tools []engine.AgentTool
	if err := c.Get(&tools); err == nil {
		opts.InitialState.Tools = tools
	}

	// CompactionConfig（struct 值类型）
	var comp engine.CompactionConfig
	if err := c.Get(&comp); err == nil {
		opts.Compaction = comp
	}

	return opts
}

// ApplyApproval 从 Container 读取 ApprovalPlugin，适配为 BeforeToolCall Hook。
//
// ApprovalPlugin.Evaluate(ctx, toolName, toolID, args) → (ApprovalResult, string, error)
// 适配为 engine.AgentLoopConfig.BeforeToolCall func(BeforeToolCallContext) *ToolCallBlock：
//   - ApprovalAllow → nil（放行）
//   - ApprovalBlock → ToolCallBlock{Block: true, Reason: reason}
//   - ApprovalAsk   → 调用 askHandler；askHandler 为 nil 时默认阻塞
//
// 未注册 ApprovalPlugin 时返回 ErrServiceNotFound（非 nil）。
func ApplyApproval(cfg *engine.AgentLoopConfig, c *container.Container, askHandler AskHandler) error {
	var ap plugin.ApprovalPlugin
	if err := c.Get(&ap); err != nil {
		return err
	}
	cfg.BeforeToolCall = approvalToBeforeToolCall(ap, askHandler)
	return nil
}

// approvalToBeforeToolCall 构造 BeforeToolCall Hook。
func approvalToBeforeToolCall(ap plugin.ApprovalPlugin, askHandler AskHandler) func(engine.BeforeToolCallContext) *engine.ToolCallBlock {
	return func(ctx engine.BeforeToolCallContext) *engine.ToolCallBlock {
		result, reason, err := ap.Evaluate(context.Background(), ctx.ToolCall.Name, ctx.ToolCall.ID, []byte(ctx.Args))
		if err != nil {
			return &engine.ToolCallBlock{Block: true, Reason: "approval error: " + err.Error()}
		}
		switch result {
		case plugin.ApprovalAllow:
			return nil
		case plugin.ApprovalBlock:
			return &engine.ToolCallBlock{Block: true, Reason: reason}
		case plugin.ApprovalAsk:
			if askHandler != nil {
				r2, reason2, err2 := askHandler(context.Background(), ctx.ToolCall.Name, ctx.ToolCall.ID, []byte(ctx.Args))
				if err2 != nil {
					return &engine.ToolCallBlock{Block: true, Reason: "ask handler error: " + err2.Error()}
				}
				if r2 == plugin.ApprovalBlock {
					return &engine.ToolCallBlock{Block: true, Reason: reason2}
				}
				return nil
			}
			return &engine.ToolCallBlock{Block: true, Reason: "approval required (no ask handler)"}
		}
		return nil
	}
}

// BridgeEvents 把 Agent 的事件桥接到 EventBus。
//
// Agent 的 11+ 种 AgentEvent 被包装为 events.Event 并 Emit 到 bus，
// 调用方可通过 bus.On(typeName, mode, handler) 订阅：
//
//	cancel := BridgeEvents(agent, bus)
//	bus.On("tool_exec_end", events.DispatchParallel, logger.Log)
//	bus.On("turn_end", events.DispatchSerial, persistor.Save)
//	defer cancel()  // 停止桥接
//
// 返回 cancel 函数，调用后停止向 bus Emit 事件。
// （agent.Subscribe 不支持取消订阅，通过 atomic flag 软停止。）
func BridgeEvents(agent *engine.Agent, bus *events.EventBus) (cancel func()) {
	var active atomic.Bool
	active.Store(true)
	agent.Subscribe(func(evt engine.AgentEvent) {
		if !active.Load() {
			return
		}
		_, _ = bus.Emit(context.Background(), AgentEventType(evt), eventAdapter{evt: evt, ts: time.Now()})
	})
	return func() { active.Store(false) }
}
