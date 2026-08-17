// Package ctx 提供 agent 侧的统一装配上下文（类 Cordis ctx，但 Go 显式风格）。
//
// 这是「defaults 从一坨功能 → 可组合插件集」的装配与生命周期载体：
//   - Ctx 包装 crux-kernel 的 Container（服务注册 + 插件生命周期 fiber），
//     提供 Mount 装配插件、Dispose 逆序卸载。
//   - 每个插件实现 Plugin.Mount，往 Ctx 的 Hooks 挂载能力 + 订阅事件，
//     资源自持、卸载即撤销。
//   - Hooks() 返回聚合后的 plugin.Hooks（交给 engine.Agent.AttachHooks）。
//   - Scoped 派生 agent 级子上下文（多租户回到真实负载路径）。
//
// 本包属于 agent-engine 模块，依赖 crux-kernel（container/events/fiber/plugin）
// 与 engine。不依赖任何 defaults 具体实现（它们实现此处定义的 Plugin 接口）。

package ctx

import (
	stdctx "context"
	"sync"

	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/crux-kernel/container"
	"github.com/hycjack/crux-kernel/events"
	"github.com/hycjack/crux-kernel/fiber"
	"github.com/hycjack/crux-kernel/plugin"
)

// Ctx 是 agent 侧统一装配上下文。
type Ctx struct {
	c      *container.Container // 服务注册 + 插件生命周期
	events *events.EventBus
	mu     sync.Mutex   // 保护 hooks 聚合的并发读写
	hooks  plugin.Hooks // 聚合全部挂载插件的扩展点（唯一扩展主干）
	name   string       // 根 Ctx 为空，scoped Ctx 为隔离名
}

// New 创建根装配上下文。
func New() *Ctx {
	return newFrom(container.New(), "")
}

func newFrom(c *container.Container, name string) *Ctx {
	return &Ctx{
		c:      c,
		events: c.Events(),
		name:   name,
	}
}

// Name 返回上下文名（根为空，scoped 为隔离名）。
func (x *Ctx) Name() string { return x.name }

// Scoped 派生子装配上下文（等价 Container.Isolate）。
//
// 子上下文继承父容器的服务（只读，可覆盖同名服务），有自己的插件列表与
// 事件总线。典型用法：多租户下，每 agent 一个 scoped Ctx 挂载会话级插件，
// agent 结束调用 scoped.Dispose() 逆序卸载（清理会话/会话专属资源）。
func (x *Ctx) Scoped(name string) *Ctx {
	child := x.c.Isolate(name)
	if child == nil {
		return nil
	}
	return newFrom(child, name)
}

// ─── 服务注册/查询（委托 Container） ───────────────────────────────────────

// Register 注册服务实例（按 Go 类型索引）。
func (x *Ctx) Register(svc any) error { return x.c.Register(svc) }

// RegisterAs 注册服务到指定类型（如具体实现 → 接口类型）。
func (x *Ctx) RegisterAs(svc, typ any) error { return x.c.RegisterAs(svc, typ) }

// Get 按类型获取已注册服务。svc 必须是指针或接口指针。
func (x *Ctx) Get(svc any) error { return x.c.Get(svc) }

// ─── 插件装配与生命周期 ────────────────────────────────────────────────────

// Mount 装配一个插件。
//
// 以插件名为名注册到 Container 的 fiber（生命周期由 Container 管理），
// 立即执行 Plugin.Mount 加载；Dispose 时逆序调用其返回的清理函数。
// 同名插件重复 Mount 会得到一个 failed 状态的 fiber（可经返回的 fiber 检查）。
func (x *Ctx) Mount(p Plugin) *fiber.PluginFiber {
	return x.c.Plugin(p.Name(), func(c *container.Container) (func() error, error) {
		return p.Mount(x)
	})
}

// Binary 底层容器句柄（供高级用法，如 ReloadPlugin）。
func (x *Ctx) Container() *container.Container { return x.c }

// Start 启动上下文（委托 Container.Start，触发 container_ready 事件）。
func (x *Ctx) Start(ctx stdctx.Context) error { return x.c.Start(ctx) }

// Dispose 卸载上下文：逆序调用所有已挂载插件的清理函数，级联卸载子上下文。
func (x *Ctx) Dispose() error { return x.c.Dispose() }

// State 返回上下文生命周期状态。
func (x *Ctx) State() container.State { return x.c.State() }

// ─── Hooks 聚合（唯一扩展主干） ────────────────────────────────────────────

// Hooks 返回聚合后的扩展主干副本（交给 engine.Agent.AttachHooks）。
func (x *Ctx) Hooks() plugin.Hooks {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.hooks
}

// MergeHooks 把非 nil 的扩展点合并进聚合主干。
// 插件在 Mount 中调用，把自己的能力挂到统一扩展主干上。
// 多个插件挂同一扩展点时：后挂载者覆盖（调用方负责协调优先级）。
func (x *Ctx) MergeHooks(sub plugin.Hooks) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if sub.ConvertToLlm != nil {
		x.hooks.ConvertToLlm = sub.ConvertToLlm
	}
	if sub.TransformContext != nil {
		x.hooks.TransformContext = sub.TransformContext
	}
	if sub.GetApiKey != nil {
		x.hooks.GetApiKey = sub.GetApiKey
	}
	if sub.ShouldStopAfterTurn != nil {
		x.hooks.ShouldStopAfterTurn = sub.ShouldStopAfterTurn
	}
	if sub.PrepareNextTurn != nil {
		x.hooks.PrepareNextTurn = sub.PrepareNextTurn
	}
	if sub.GetSteeringMessages != nil {
		x.hooks.GetSteeringMessages = sub.GetSteeringMessages
	}
	if sub.GetFollowUpMessages != nil {
		x.hooks.GetFollowUpMessages = sub.GetFollowUpMessages
	}
	if sub.BeforeToolCall != nil {
		x.hooks.BeforeToolCall = sub.BeforeToolCall
	}
	if sub.AfterToolCall != nil {
		x.hooks.AfterToolCall = sub.AfterToolCall
	}
	if sub.StreamFn != nil {
		x.hooks.StreamFn = sub.StreamFn
	}
	if sub.ProviderStreamFn != nil {
		x.hooks.ProviderStreamFn = sub.ProviderStreamFn
	}
	if sub.Compaction.Compactor != nil {
		x.hooks.Compaction = sub.Compaction
	}
}

// Edge 事件总线（供插件订阅横切事件）。
func (x *Ctx) Events() *events.EventBus { return x.events }

// Bridge 把 engine.Agent 的事件桥接到本上下文的事件总线（带 agent 作用域标签）。
//
// 返回 cancel；cancel 后停止桥接。事件经 agentengine.WrapEvent 包装，
// 可用 events.On(typeName, mode, handler) 订阅。
func (x *Ctx) Bridge(agent *engine.Agent) (cancel func()) {
	return BridgeEvents(agent, x.events)
}
