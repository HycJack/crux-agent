// Package harnessreg 提供把 agent-engine/harness 各 concern 注册到 crux-kernel Container 的辅助函数。
//
// 每个 Register 函数封装对应 concern 的构造 + RegisterAs，让调用方一行完成注册：
//
//	c, _ := container.New(context.Background(), nil)
//	harnessreg.RegisterSession(c, "./.sessions")
//	harnessreg.RegisterContext(c, ctxCfg)
//	harnessreg.RegisterApproval(c, harnessreg.ApprovalAllowByDefault)
//	harnessreg.RegisterCheckpoint(c)
//
// 现有直接构造方式（NewSessionManager / NewPipeline / approval.New 等）不受影响。
package harnessreg

import (
	"errors"
	"fmt"

	"github.com/hycjack/agent-engine/harness/approval"
	"github.com/hycjack/agent-engine/harness/checkpoint"
	ctxm "github.com/hycjack/agent-engine/harness/context"
	"github.com/hycjack/agent-engine/harness/session"

	"github.com/hycjack/crux-kernel/container"
)

// --- Approval ---

// ApprovalConfig 是 approval.Gate 的注册配置。
type ApprovalConfig struct {
	// Gate 是预构造的 Gate。若为 nil，使用 Preset 规则集。
	// Preset 仅在 Gate 为 nil 时生效。
	Gate   *approval.Gate
	Preset []approval.Rule
}

// RegisterApproval 构造 approval.Gate 并注册到 Container。
//
// Preset 语义：
//   - nil：使用 approval.DangerousTools()（默认安全）
//   - 空切片 []Rule{}：无规则，default Allow（用户显式要求 allow-by-default）
//   - 非空：使用给定规则集
//
// 注册后，可通过 Container.Get(&approval.Gate{}) 查询。
func RegisterApproval(c *container.Container, cfg ApprovalConfig) error {
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
	return c.Register(gate)
}

// --- Context ---

// RegisterContext 构造 context.Pipeline 并注册到 Container。
//
// 注意：Pipeline.NewPipeline 会创建 token.NewMessageCounter(config.Model.ID)，
// 若 model.ID 不在 tiktoken 已知列表中会返回错误。
//
// 注册后，可通过 Container.Get(&ctxm.Pipeline{}) 查询。
func RegisterContext(c *container.Container, cfg ctxm.PipelineConfig) error {
	p, err := ctxm.NewPipeline(cfg)
	if err != nil {
		return fmt.Errorf("harnessreg: register context: %w", err)
	}
	return c.Register(p)
}

// --- Session ---

// RegisterSession 构造 session.SessionManager 并注册到 Container。
//
// sessionsDir 必须是可写目录（不存在会自动创建）。
//
// 注册后，可通过 Container.Get(&session.SessionManager{}) 查询。
func RegisterSession(c *container.Container, sessionsDir string) error {
	mgr, err := session.NewSessionManager(sessionsDir)
	if err != nil {
		return fmt.Errorf("harnessreg: register session: %w", err)
	}
	return c.Register(mgr)
}

// --- Checkpoint ---

// RegisterCheckpoint 构造 checkpoint.Store 并注册到 Container。
//
// checkpoint.Store 是纯内存实现，无外部依赖，不会失败。
//
// 注册后，可通过 Container.Get(*checkpoint.Store) 查询。
func RegisterCheckpoint(c *container.Container) error {
	return c.Register(checkpoint.New())
}

// --- 预设 ---

// ApprovalAllowByDefault 是显式空 preset，default Allow。
// 必须用空切片（非 nil）才能区分"显式 allow-by-default"与"未设置"。
var ApprovalAllowByDefault = []approval.Rule{}

// ApprovalStrictByDefault 是 Block-By-Default 的 Rule preset。
// 调用方需要配合 approval.NewStrict() 使用。
var ApprovalStrictByDefault = []approval.Rule{
	{Name: "all", Match: approval.Always(), Approve: approval.DecisionAsk, Reason: "Strict mode: requires approval"},
}

// ErrInvalidConfig 在配置非法时返回。
var ErrInvalidConfig = errors.New("harnessreg: invalid config")
