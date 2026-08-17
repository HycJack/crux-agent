// Package plugin adapt.go — 能力接口 → Hooks 的默认适配。
//
// 8 个能力契约（Session/Context/Memory/AutoLearn/Tool/Approval/Checkpoint/Observe）
// 各自提供「转成 Hooks/事件挂载」的适配函数。这样 engine 只需消费统一的 Hooks，
// 适配逻辑归契约层，不再散落在 integration/engine 的应用侧桥接代码里。
//
// 本文件只依赖 crux-ai/core 与本包类型。

package plugin

import (
	"context"

	core "github.com/hycjack/crux-ai/core"
)

// ─── ApprovalPlugin → Hooks.BeforeToolCall ──────────────────────────────────

// AskHandler 处理 ApprovalPlugin.Evaluate 返回 ApprovalAsk 的情况。
// 让外部（CLI/UI）决定是否放行；为 nil 时默认阻塞。
type AskHandler func(ctx context.Context, toolName, toolID string, args []byte) (ApprovalResult, string, error)

// MapApproval 把 ApprovalPlugin 适配成 Hooks.BeforeToolCall：
//   - ApprovalAllow → nil（放行）
//   - ApprovalBlock → ToolCallBlock{Block:true, Reason}
//   - ApprovalAsk   → 调 askHandler；askHandler 为 nil 时默认阻塞
func MapApproval(ap ApprovalPlugin, ask AskHandler) func(BeforeToolCallCtx) *ToolCallBlock {
	return func(ctx BeforeToolCallCtx) *ToolCallBlock {
		result, reason, err := ap.Evaluate(context.Background(), ctx.ToolCall.Name, ctx.ToolCall.ID, []byte(ctx.Args))
		if err != nil {
			return &ToolCallBlock{Block: true, Reason: "approval error: " + err.Error()}
		}
		switch result {
		case ApprovalAllow:
			return nil
		case ApprovalBlock:
			return &ToolCallBlock{Block: true, Reason: reason}
		case ApprovalAsk:
			if ask != nil {
				r2, reason2, err2 := ask(context.Background(), ctx.ToolCall.Name, ctx.ToolCall.ID, []byte(ctx.Args))
				if err2 != nil {
					return &ToolCallBlock{Block: true, Reason: "ask handler error: " + err2.Error()}
				}
				if r2 == ApprovalBlock {
					return &ToolCallBlock{Block: true, Reason: reason2}
				}
				return nil
			}
			return &ToolCallBlock{Block: true, Reason: "approval required (no ask handler)"}
		}
		return nil
	}
}

// ─── ContextPlugin → Hooks.TransformContext + Hooks.Compaction ──────────────

// MapContext 把 ContextPlugin 适配成 TransformContext + CompactionHooks。
//
// compactor 由调用方提供（通常是基于 ContextPlugin.Compact 的闭包）；
// 若 compactor 为 nil，则 ContextPlugin 只提供 TransformContext（追加/修剪）。
func MapContext(cp ContextPlugin, compactor func(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error)) (func([]core.Message) []core.Message, CompactionHooks) {
	transform := func(msgs []core.Message) []core.Message {
		// 以 ContextPlugin 当前状态为准：若接近上限则提示压缩
		if cp.IsNearLimit(0.9) {
			_ = cp.Compact(context.Background())
		}
		return cp.GetMessages()
	}

	var comp CompactionHooks
	if compactor != nil {
		comp.Compactor = compactor
		comp.MaxTokens = cp.GetStats().MaxTokens
	}
	return transform, comp
}

// ─── ToolPlugin → core.Tool 元数据 ──────────────────────────────────────────

// MapTool 把 ToolPlugin 转成 core.Tool（供模型调用声明 + token 估算）。
// 工具的执行走 crux-plugin 等后端，不直接进 Hooks。
func MapTool(t ToolPlugin) core.Tool {
	return core.Tool{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters:  t.Parameters(),
	}
}
