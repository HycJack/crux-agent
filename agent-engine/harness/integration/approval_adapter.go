package harnessreg

import (
	"context"

	"github.com/hycjack/agent-engine/harness/approval"

	"github.com/hycjack/crux-kernel/plugin"
)

// ApprovalAdapter 把 harness 的 *approval.Gate 适配为 agent-engine 的 plugin.ApprovalPlugin。
//
// harness approval.Gate.Evaluate(Request) Result
//   → agent-engine ApprovalPlugin.Evaluate(ctx, toolName, toolID, args) (ApprovalResult, string, error)
//
// Decision 映射：
//   DecisionAllow → ApprovalAllow
//   DecisionBlock → ApprovalBlock
//   DecisionAsk  → ApprovalAsk
type ApprovalAdapter struct {
	Gate *approval.Gate
}

// NewApprovalAdapter 包装 *approval.Gate 为 plugin.ApprovalPlugin。
func NewApprovalAdapter(gate *approval.Gate) *ApprovalAdapter {
	return &ApprovalAdapter{Gate: gate}
}

// Evaluate 实现 plugin.ApprovalPlugin 接口。
func (a *ApprovalAdapter) Evaluate(ctx context.Context, toolName, toolID string, args []byte) (plugin.ApprovalResult, string, error) {
	if a.Gate == nil {
		return plugin.ApprovalBlock, "approval gate is nil", nil
	}
	result := a.Gate.Evaluate(approval.Request{
		ToolName: toolName,
		ToolID:   toolID,
		Args:     args,
	})
	switch result.Decision {
	case approval.DecisionAllow:
		return plugin.ApprovalAllow, result.Reason, nil
	case approval.DecisionBlock:
		return plugin.ApprovalBlock, result.Reason, nil
	case approval.DecisionAsk:
		return plugin.ApprovalAsk, result.Reason, nil
	default:
		return plugin.ApprovalBlock, "unknown approval decision", nil
	}
}

// RegisterApprovalAsPlugin 把 harness approval.Gate 注册为 plugin.ApprovalPlugin。
//
// 内部用 RegisterApproval 注册 *approval.Gate（按具体类型查询），
// 再用 RegisterAs 把 ApprovalAdapter 注册到 plugin.ApprovalPlugin 接口类型。
//
// Preset 语义同 RegisterApproval：
//   - nil：使用 DangerousTools
//   - []Rule{}：显式空 preset，default Allow
//   - 非空：使用给定规则集
//
// 调用方既可以按 *approval.Gate 查询原始 Gate（用于 AddRule / SetAskHandler），
// 也可以按 plugin.ApprovalPlugin 查询适配后的接口（用于 ApplyApproval）。
func RegisterApprovalAsPlugin(c interface {
	Register(svc any) error
	RegisterAs(svc any, typ any) error
}, cfg ApprovalConfig) error {
	gate := cfg.Gate
	if gate == nil {
		gate = approval.New()
		var preset []approval.Rule
		if cfg.Preset == nil {
			preset = approval.DangerousTools()
		} else {
			preset = cfg.Preset
		}
		for _, rule := range preset {
			gate.AddRule(rule)
		}
	}
	if err := c.Register(gate); err != nil {
		return err
	}
	return c.RegisterAs(NewApprovalAdapter(gate), (*plugin.ApprovalPlugin)(nil))
}
