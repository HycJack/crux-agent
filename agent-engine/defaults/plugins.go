// Package defaults plugins.go — 把平铺的默认实现升级为可组合插件集。
//
// 每个默认实现在此包出一个 ctx.Plugin 包装：
//   - 实现 ctx.Plugin.Mount(x)：注册服务、把能力挂到统一扩展主干（x.MergeHooks）、
//     订阅事件；返回资源清理函数（Ctx.Dispose 时逆序调用）。
//   - BundleDefault 一键装配常用子集，供应用通过 ctx.New + ctx.Mount 组合装配。
//
// 依赖方向：defaults → agent-engine/ctx → engine。defaults 内的插件装配尽量
// 用 core/plugin/ctx 类型（不直接依赖 engine.Agent），保持实现层轻耦合，
// 由上层把聚合后的 x.Hooks() 交给 engine.Agent.AttachHooks。

package defaults

import (
	stdctx "context"

	"github.com/hycjack/agent-engine/ctx"
	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-kernel/events"
	"github.com/hycjack/crux-kernel/plugin"
)

// ─── 编译期断言：默认实现的插件包装均满足 ctx.Plugin ───────────────────────

var (
	_ ctx.Plugin = (*SessionPlugin)(nil)
	_ ctx.Plugin = (*CompactionPlugin)(nil)
	_ ctx.Plugin = (*ContextPipelinePlugin)(nil)
	_ ctx.Plugin = (*ApprovalPlugin)(nil)
	_ ctx.Plugin = (*MemoryPlugin)(nil)
	_ ctx.Plugin = (*AutoLearnPlugin)(nil)
	_ ctx.Plugin = (*ObservePlugin)(nil)
	_ ctx.Plugin = (*CheckpointPlugin)(nil)
)

// ─── SessionPlugin 会话持久化 ────────────────────────────────────────────────

// SessionPlugin 包装 *JSONLSession。
// 会话持久化是「调用方契约」（原设计如此）：本插件负责注册为服务（其他插件/
// 应用可 Get）、生命周期内持有并在 Dispose 时 Close；消息写入由上层应用经
// ctx.Events() 订阅完成（必要时在 P4 用 Hooks.PrepareNextTurn 挂接）。
type SessionPlugin struct {
	Inner *JSONLSession
}

func (p *SessionPlugin) Name() string { return "session" }

func (p *SessionPlugin) Mount(x *ctx.Ctx) (func() error, error) {
	x.Register(p.Inner)
	return func() error { return p.Inner.Close() }, nil
}

// ─── CompactionPlugin 上下文压缩 ────────────────────────────────────────────

// CompactionPlugin 把任意 Compactor + 预算参数挂到 Hooks.Compaction
// （调用前压缩 + 溢出重试两条路径都吃到）。
type CompactionPlugin struct {
	Compactor       Compactor
	TokenCounter    func(string, []core.Message, []core.Tool) int
	MaxTokens       int
	ReserveTokens   int
	OverflowRetries int
	OnCompact       func(prevTokens, newTokens, prevMsgs, newMsgs int)
}

func (p *CompactionPlugin) Name() string { return "compaction" }

func (p *CompactionPlugin) Mount(x *ctx.Ctx) (func() error, error) {
	comp := plugin.CompactionHooks{
		Compactor: func(c stdctx.Context, msgs []core.Message) ([]core.Message, bool, error) {
			if p.Compactor == nil {
				return msgs, false, nil
			}
			return p.Compactor.Compact(c, msgs)
		},
		TokenCounter:    p.TokenCounter,
		MaxTokens:       p.MaxTokens,
		ReserveTokens:   p.ReserveTokens,
		OverflowRetries: p.OverflowRetries,
		OnCompact:       p.OnCompact,
	}
	x.MergeHooks(plugin.Hooks{Compaction: comp})
	return nil, nil
}

// ─── ContextPipelinePlugin 滑动窗口上下文管理 ───────────────────────────────

// ContextPipelinePlugin 包装 *ContextPipeline，把 NewCompactor(pipeline) 挂到
// Hooks.Compaction，把 pipeline 注册为服务（上游 agent 可实时读 GetStats）。
type ContextPipelinePlugin struct {
	Pipeline *ContextPipeline
}

func (p *ContextPipelinePlugin) Name() string { return "context" }

func (p *ContextPipelinePlugin) Mount(x *ctx.Ctx) (func() error, error) {
	x.Register(p.Pipeline)
	compacter := NewCompactor(p.Pipeline)
	x.MergeHooks(plugin.Hooks{
		Compaction: plugin.CompactionHooks{
			Compactor: compacter,
			MaxTokens: p.Pipeline.maxTokens,
		},
	})
	return nil, nil
}

// ─── ApprovalPlugin 工具审批 ────────────────────────────────────────────────

// ApprovalPlugin 把 *ApprovalGate 挂到 Hooks.BeforeToolCall（经 plugin.MapApproval）。
type ApprovalPlugin struct {
	Gate *ApprovalGate
}

func (p *ApprovalPlugin) Name() string { return "approval" }

func (p *ApprovalPlugin) Mount(x *ctx.Ctx) (func() error, error) {
	x.RegisterAs(p.Gate, (*plugin.ApprovalPlugin)(nil))
	hook := plugin.MapApproval(p.Gate, nil)
	x.MergeHooks(plugin.Hooks{BeforeToolCall: hook})
	return nil, nil
}

// ─── MemoryPlugin 长期记忆 ──────────────────────────────────────────────────

// MemoryPlugin 把 *Memory 注册为服务，Dispose 时 Save 落盘。
type MemoryPlugin struct {
	Mem *Memory
}

func (p *MemoryPlugin) Name() string { return "memory" }

func (p *MemoryPlugin) Mount(x *ctx.Ctx) (func() error, error) {
	x.Register(p.Mem)
	return func() error { return p.Mem.Save() }, nil
}

// ─── AutoLearnPlugin 自动学习 ───────────────────────────────────────────────

// AutoLearnPlugin 把 *AutoLearner 注册为服务（离线上文自动跟学），
// 用户消息写入交给上层经事件订阅（Payload 携带用户输入）触发 ProcessUserInput。
type AutoLearnPlugin struct {
	Learner *AutoLearner
}

func (p *AutoLearnPlugin) Name() string { return "autolearn" }

func (p *AutoLearnPlugin) Mount(x *ctx.Ctx) (func() error, error) {
	x.Register(p.Learner)
	return nil, nil
}

// ─── ObservePlugin 观测/日志 ────────────────────────────────────────────────

// ObservePlugin 把 *Logger 挂到 ctx 事件总线，横切记录 Engine 关键事件。
type ObservePlugin struct {
	Logger *Logger
}

func (p *ObservePlugin) Name() string { return "observe" }

func (p *ObservePlugin) Mount(x *ctx.Ctx) (func() error, error) {
	x.Register(p.Logger)
	ids := []string{}
	for _, evtType := range []string{"turn_start", "tool_exec_start", "tool_exec_end", "message_end"} {
		t := evtType
		id := x.Events().On(t, events.DispatchBroadcast, func(_ stdctx.Context, e events.Event) (any, error) {
			p.Logger.Info("event", map[string]any{"type": t, "at": e.Timestamp()})
			return nil, nil
		})
		ids = append(ids, id)
	}
	return func() error {
		for _, id := range ids {
			x.Events().Off(id)
		}
		return nil
	}, nil
}

// ─── CheckpointPlugin 检查点 ────────────────────────────────────────────────

// CheckpointPlugin 把 *CheckpointStore 注册为服务；检查点保存的触发时机
// （turn 边界）由上层应用经事件订阅决定。
type CheckpointPlugin struct {
	Store *CheckpointStore
}

func (p *CheckpointPlugin) Name() string { return "checkpoint" }

func (p *CheckpointPlugin) Mount(x *ctx.Ctx) (func() error, error) {
	x.Register(p.Store)
	return nil, nil
}

// ─── BundleDefault 一键装配 ─────────────────────────────────────────────────

// BundleOpts 配置 BundleDefault 装配哪些插件。
type BundleOpts struct {
	// 会话与记忆（持久化路径）
	Session *JSONLSession
	Mem     *Memory

	// 上下文压缩：选其一。二者都传时 Compaction 优先（先到先挂，后挂覆盖）。
	Compactor Compactor
	Pipeline  *ContextPipeline

	// 审批、观测、自动学习、检查点（可选）
	Approval   *ApprovalGate
	Logger     *Logger
	Learner    *AutoLearner
	Checkpoint *CheckpointStore

	// 压缩预算（Compactor 路径用）
	MaxTokens       int
	ReserveTokens   int
	OverflowRetries int
	TokenCounter    func(string, []core.Message, []core.Tool) int

	// 装配哪些插件（默认全部可装配的）
	DisableSession, DisableMemory, DisableCompaction                     bool
	DisableApproval, DisableObserve, DisableAutolearn, DisableCheckpoint bool
}

// BundleDefault 把常用默认插件一次性 Mount 到 x，返回逆序清理函数 error。
// 各插件资源在 Ctx.Dispose 时由 Container 统一逆序清理；本函数返回其聚合
// 结果（首个失败即返回）。
func BundleDefault(x *ctx.Ctx, o BundleOpts) error {
	if o.Session != nil && !o.DisableSession {
		if err := mount(x, &SessionPlugin{Inner: o.Session}); err != nil {
			return err
		}
	}
	if o.Mem != nil && !o.DisableMemory {
		if err := mount(x, &MemoryPlugin{Mem: o.Mem}); err != nil {
			return err
		}
		if o.Learner != nil && !o.DisableAutolearn {
			if err := mount(x, &AutoLearnPlugin{Learner: o.Learner}); err != nil {
				return err
			}
		}
	}
	if !o.DisableCompaction {
		switch {
		case o.Pipeline != nil:
			if err := mount(x, &ContextPipelinePlugin{Pipeline: o.Pipeline}); err != nil {
				return err
			}
		case o.Compactor != nil:
			if err := mount(x, &CompactionPlugin{
				Compactor:       o.Compactor,
				TokenCounter:    o.TokenCounter,
				MaxTokens:       o.MaxTokens,
				ReserveTokens:   o.ReserveTokens,
				OverflowRetries: o.OverflowRetries,
			}); err != nil {
				return err
			}
		}
	}
	if o.Approval != nil && !o.DisableApproval {
		if err := mount(x, &ApprovalPlugin{Gate: o.Approval}); err != nil {
			return err
		}
	}
	if o.Logger != nil && !o.DisableObserve {
		if err := mount(x, &ObservePlugin{Logger: o.Logger}); err != nil {
			return err
		}
	}
	if o.Checkpoint != nil && !o.DisableCheckpoint {
		if err := mount(x, &CheckpointPlugin{Store: o.Checkpoint}); err != nil {
			return err
		}
	}
	return nil
}

// mount 调用 x.Mount 并忽略返回（错误/失败经 fiber 状态可查）。
func mount(x *ctx.Ctx, p ctx.Plugin) error {
	_ = x.Mount(p)
	return nil
}
