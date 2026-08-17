# Engine「顶部钩子（Top-Level Hooks）」统一改造方案

> 目标：消除 crux「两套扩展机制」（`engine` 的 12 个 func 字段 vs `crux-kernel/plugin`
> 的 8 接口），收敛为**单一扩展主干 `plugin.Hooks`**；参考 dsh 的「一切皆插件」，
> 让 `defaults` 成为可组合插件集，用 `ctx` 统一装配与生命周期，scope 回到真实负载路径。
>
> 版本：v0.2 · 2026-08-17 · 已核实代码现状

---

## 0. 为什么 Hooks 放 `crux-kernel/plugin`（关键决策）

`agent-engine/go.mod` **已经依赖 `crux-kernel`**（`replace => ../crux-kernel`）。
因此把 `Hooks` 类型放在 `crux-kernel/plugin` 并让 `engine` 消费它，**不新增任何跨模块依赖**，
只是让 `engine` 从「消费自己的私有 func 字段」改为「消费契约层的公共 Hooks」。

结论：**`plugin.Hooks` 定义在 `crux-kernel/plugin/hooks.go`，`engine` 引用它。**
这是消除双轨、且不破坏 `engine` 依赖图谱的最小路径。

---

## 1. 现状（已核实）

### 1.1 `engine.AgentLoopConfig` 内嵌 12 个 func 字段（`engine/types.go`）

| 字段 | 调用点 |
|---|---|
| `ConvertToLlm` | `convertToLLM()` (loop.go) |
| `TransformContext` | `transformContext()` (loop.go) |
| `GetApiKey` | `resolveStreamOptions()` (loop.go) |
| `ShouldStopAfterTurn` | `runInnerLoop()` (loop.go) |
| `PrepareNextTurn` | `runInnerLoop()` (loop.go) |
| `GetSteeringMessages` | `injectSteeringMessages()` (loop.go) |
| `GetFollowUpMessages` | `injectFollowUpMessages()` (loop.go) |
| `BeforeToolCall` | `prepareAndExecuteToolCall()` (tools.go) |
| `AfterToolCall` | `finalizeToolCall()` (tools.go) |
| `StreamFn` | `invokeStreamFn()` (loop.go) |
| `ProviderStreamFn` | `invokeStreamFn()` (loop.go) |
| `Compaction` | `maybeCompactPreCall()` / `retryWithCompaction()` (stream.go) |

### 1.2 当前模块依赖（已核实）

```
agent-engine  → crux-ai, crux-kernel          (go.mod)
crux-kernel   → crux-ai                       (go.mod，真·底座)
```

`agent-engine` **已依赖 `crux-kernel`**，故 Hooks 放 `plugin` 无新增依赖。

### 1.3 `defaults` 现状

`agent-engine/defaults/` 是一批**平铺结构**（session/context/compaction/approval/memory/
autolearn/checkpoint/observe/workflow/token），各自实现 `plugin.XxxPlugin` 接口，
但**没有一个统一的「装配 + 生命周期」入口**，应用层需手工 new + 手工接线。

---

## 2. 目标分层

```
应用层 (crux-agent-chat / tui / chat-app)
  └─ ctx.New() → Ctx.Mount(各 defaults 插件) → Ctx.Hooks() → engine.New(Agent)
────────────────────────────────────────────
agent-engine
  ├─ engine/   领域核心：AgentLoop / Agent（有状态）
  │     扩展点 = plugin.Hooks（唯一主干）+ 事件轴（EventBus）
  ├─ ctx/      统一上下文：装配 defaults、管理生命周期、Scoped 多租户
  ├─ defaults/ 可组合插件集（每个实现 ctx.Plugin，资源自持、卸载即撤销）
  └─ integration/ 仅保留跨模块桥（删减 ApplyApproval/FromContainer）
────────────────────────────────────────────
crux-kernel
  ├─ plugin/hooks.go   新增：Hooks（唯一扩展主干）+ CompactionHooks + 配套类型
  ├─ plugin/adapt.go   新增：8 接口 → Hooks 的默认适配（MapXxx）
  ├─ plugin/types.go   8 接口（契约源，保持不变）
  ├─ container/   服务注册 + 插件生命周期（成为 ctx 的底座）
  ├─ events/      事件总线（成为 engine 事件轴的后端）
  └─ fiber/       生命周期状态机
────────────────────────────────────────────
crux-ai/core  类型底层（不变）
```

---

## 3. 分模块修改

### 模块 A：`crux-kernel/plugin` — 新增统一钩子契约

**新增 `crux-kernel/plugin/hooks.go`**：定义 `Hooks`，把 `engine.AgentLoopConfig`
的 12 个 func 字段收敛成一组命名钩子 + 配套类型（`BeforeToolCallCtx`/`AfterToolCallCtx`/
`ToolCallBlock`/`ToolCallOverride`/`CompactionHooks`/`StreamFn`/`ProviderStreamFn`）。

参考签名（对齐现有 engine 类型，避免重复定义）：

```go
package plugin

import (
    "context"
    "encoding/json"
    core "github.com/hycjack/crux-ai/core"
)

// Hooks 是 agent 循环的全部可组合扩展点（唯一扩展主干）。
type Hooks struct {
    ConvertToLlm        func([]core.Message) []core.Message
    TransformContext    func([]core.Message) []core.Message
    GetApiKey           func() string
    ShouldStopAfterTurn func(core.AssistantMessage, []core.ToolResultMessage) bool
    PrepareNextTurn     func(*PrepareTurnCtx)
    GetSteeringMessages func() []core.Message
    GetFollowUpMessages func() []core.Message
    BeforeToolCall      func(BeforeToolCallCtx) *ToolCallBlock
    AfterToolCall       func(AfterToolCallCtx) *ToolCallOverride
    StreamFn            StreamFn
    ProviderStreamFn    ProviderStreamFn
    Compaction          CompactionHooks
}
```

配套类型：`BeforeToolCallCtx` / `AfterToolCallCtx`（含 `AgentToolResult`）/ `ToolCallBlock` /
`ToolCallOverride` / `CompactionHooks`（含 `Compactor`/`TokenCounter`/`MaxTokens`/…）/
`StreamFn` / `ProviderStreamFn` / `AgentTool`（Hooks 需要的工具由 engine 自定义，不进 plugin）。

**新增 `crux-kernel/plugin/adapt.go`**：8 个能力接口各自提供「转成 Hooks 的适配函数」：

```go
// ApprovalPlugin → Hooks.BeforeToolCall
func MapApproval(ap ApprovalPlugin, ask func(ctx context.Context, toolName, toolID string, args []byte) (ApprovalResult, string, error)) func(BeforeToolCallCtx) *ToolCallBlock

// ContextPlugin → Hooks.TransformContext + Hooks.Compaction
func MapContext(cp ContextPlugin, comp CompactionHooks) (transform func([]core.Message) []core.Message, compHooks CompactionHooks)
```

> 这些适配把现在散落在 `integration/agentengine/bridge.go` 的 `ApplyApproval`/
> `approvalToBeforeToolCall` 逻辑收归契约层，实现「适配属于契约」的归位。

### 模块 B：`agent-engine/engine` — 去特权化：func 字段 → Hooks

1. **`engine/types.go`**：
   - 删除 `AgentLoopConfig` 的 12 个 func 字段，改为 `Hooks plugin.Hooks`。
   - `CompactionConfig` → 引用 `plugin.CompactionHooks`（或类型别名）。
   - `BeforeToolCallContext`/`AfterToolCallContext`/`ToolCallBlock`/`ToolCallOverride`/
     `StreamFn`/`ProviderStreamFn` → 改为 `plugin` 里的同名类型（别名或直接用）。
   - **保留向后兼容**：`Hooks.FromLegacyConfig(old AgentLoopConfig) Hooks`（deprecated
     一次性迁移助手）+ `AgentLoopConfig.applyLegacy()` 归并旧字段。
2. **`engine/loop.go` / `stream.go` / `tools.go`**：
   - 所有 `config.ConvertToLlm` → `config.Hooks.ConvertToLlm` …（机械替换）。
   - `PrepareNextTurn` 签名 `func(config *AgentLoopConfig, …)` → 传 `*PrepareTurnCtx`
     （内含 `*Hooks` 引用以便改写下一个 turn 的扩展点）。
3. **`engine/agent.go`**：
   - `AgentState` 的 func 字段 → `Hooks`。
   - 新增 `AttachHooks(h Hooks)` / 内部 `buildConfig()` 组装 `AgentLoopConfig.Hooks`。
   - `Agent` 升级为「消费 `plugin.Hooks` 的执行宿主」。

### 模块 C：`agent-engine/ctx`（新包）— 统一上下文 + 生命周期

**P3 已交付**（`ctx.go`/`plugin.go`/`events.go`）：
- `Ctx` 包装 `crux-kernel/container`（服务注册 + fiber 生命周期），提供 `Mount` 装配插件、`Dispose` 逆序卸载、`Scoped` 子上下文（多租户）。
- `Ctx.MergeHooks(sub plugin.Hooks)`：把各插件能力合并到**唯一扩展主干**，最终 `Hooks()` 交给 `engine.Agent.AttachHooks`。
- `ctx.Plugin` 接口：`Name()` + `Mount(x)`，插件自持资源、Dispose 时逆序清理。
- `BridgeEvents`：把 `engine.Agent` 事件桥接到 `Ctx` 事件总线（复用 `agentengine.WrapEvent`）。
- `defaults/plugins.go`：`Session/Compaction/ContextPipeline/Approval/Memory/AutoLearn/Observe/Checkpoint` 八个插件包装 + `BundleDefault` 一键装配。
- 测试：`ctx_test.go`（hooks 聚合、scoped 服务继承、Dispose 逆序）、`plugins_test.go`（压缩真实生效、审批真实拦截、服务注册/生命周期）。

**新增 `agent-engine/ctx/ctx.go`**：

```go
package ctx

// Ctx 是 agent 侧统一装配上下文（类 Cordis ctx，Go 显式风格）。
type Ctx struct {
    c      *container.Container   // 服务注册 + 插件生命周期
    events *events.EventBus        // 事件轴
    hooks  *plugin.Hooks           // 聚合全部扩展点
}

type Plugin interface {
    Name() string
    Mount(x *Ctx) (dispose func(), err error)
}

func New() *Ctx
func (x *Ctx) Scoped(name string) *Ctx           // 多租户：每 agent 一个 scoped ctx
func (x *Ctx) Mount(p Plugin) *fiber.PluginFiber  // 装配插件，生命周期由 Container 管理
func (x *Ctx) Hooks() *plugin.Hooks
func (x *Ctx) Register(svc any) error
func (x *Ctx) Get(svc any) error
func (x *Ctx) Start(ctx context.Context) error    // → Container.Start
func (x *Ctx) Dispose() error                     // → Container.Dispose（逆序卸载）
```

**新增 `agent-engine/ctx/events.go`**：把 `engine.Agent` 事件桥到 `EventBus`（带 AgentID
标签，供 scope 过滤），替代现在散落在应用层的 `forwardAgentEvent`/`BridgeEvents`。

**新增 `agent-engine/ctx/hooks.go`**：`Hooks` 聚合器的并发安全累加。

### 模块 D：`agent-engine/defaults` — 升级为可组合插件集

- 每个能力补 `Plugin.Mount`（`defaults/*_plugin.go`），资源自持、卸载即撤销。
  - `Session` → scoped ctx 订阅事件轴（MessageEnd/TurnEnd → Append）。
  - `Compaction`/`Context` → 挂 `Hooks.Compaction` + `Hooks.TransformContext`。
  - `Approval` → 挂 `Hooks.BeforeToolCall`（经 `plugin.MapApproval`）。
  - `Memory` → 挂 `Hooks.PrepareNextTurn` + 订阅事件。
  - `AutoLearn` → 订阅用户事件。
  - `Observe` → 事件轴订阅 + `Hooks` 外部钩子。
  - `Checkpoint` → 订阅 turn 边界事件。
- 新增 `defaults/bundle.go`：`BundleDefault(x *ctx.Ctx)` 一键装配常用插件。
- **收尾**：把 `agent-engine/harness/` 的 approval/context/session 并入 defaults，
  删除 `harness/`。

### 模块 E：scope / 多租户回真实负载路径

- `ctx.Ctx.Scoped(name)` 包装 `Container.Isolate`。
- 每 agent = 一个 scoped Ctx + 一组挂载其上的插件；agent 结束 = `scoped.Dispose()`。
- 事件按 scope 分发（AgentID 标签过滤）。

---

## 4. 迁移顺序（每步可编译、可回滚）

| 阶段 | 内容 | 交付物 | 编译目标 |
|---|---|---|---|
| P1 | 契约层打桩 ✅ 已完成 (23a9ed8) | `plugin/hooks.go` + `plugin/adapt.go` + `adapt_test.go` | 绿（新增不影响现有） |
| P2 | engine 内部收敛 ✅ 已完成 (81a1433) | `AgentLoopConfig` 新增 `Hooks` 字段；engine 调用点 `config.hooks()` 归一化；legacy 字段(deprecated) 双向填充；`Agent.hooks()`/`AttachHooks`；钩子类型别名收敛；`hooks_test.go` | 绿（零行为变化，legacy 全兼容） |
| P3 | defaults 插件化 ✅ 已完成 | 新增 `agent-engine/ctx` 包（Ctx 包装 Container：Mount/Scoped/Dispose/Hooks 聚合MergeHooks/事件桥接BridgeEvents）；`defaults/plugins.go`（8 个默认组件实现 `ctx.Plugin` + `BundleDefault` 一键装配）；`ctx_test.go`（hooks聚合/scoped继承/Dispose逆序）+ `plugins_test.go`（压缩生效/审批拦截/服务注册） | 绿（新增不动存量，legacy 全兼容） |
| P4 | 应用切 ctx | chat-app/chat/tui 改用 `ctx.New+Mount+Start`；删手工桥 | 绿（行为对照） |
| P5 | 收尾去重 | 并入 `harness/`，删 `integration/bridge.go` 适配，删旧字段 | 绿 |
| P6 | scope 落地 + 文档 | chat-app 每会话 Scoped；重写 DESIGN.md / docs/06；补本文档 | 绿 |

---

## 5. 删除/退役清单

- `engine.AgentLoopConfig` 12 个 func 字段 → `plugin.Hooks`
- `engine.CompactionConfig` → `plugin.CompactionHooks`
- `integration/agentengine/bridge.go` 的 `ApplyApproval`/`approvalToBeforeToolCall`/`FromContainer`
- `agent-engine/harness/`（approval/context/session）
- `AgentState` 散落 setter → `AttachHooks`
- 应用层手工 `forwardAgentEvent`/session 接线 → `ctx`

---

## 6. 风险与取舍

1. **`engine` 从「仅依赖 crux-ai/core」变为「依赖 crux-kernel/plugin」**：
   `agent-engine` 模块本就依赖 `crux-kernel`，故**模块级依赖不变**；仅 engine 包内
   引用增加。此为有意取舍：用「契约引用」换「单一扩展主干、消灭双轨」。
2. **dsh 的声明式装配（bundle/profile/patch yaml）不采用**：只吸收
   「插件 + ctx + 生命周期 + scope」四个可移植点；Go 里用 `BundleDefault`（代码组合）替代 yaml。
3. **P2 的「复制字段 + deprecated 双向填充」是保编译的关键**：不跳阶段。
4. **`PrepareNextTurn` 签名变化**：从 `func(config *AgentLoopConfig,...)` 改成
   `func(*PrepareTurnCtx)`，属破坏性变更，集中到 P5 一次性处理。

---

## 7. 验收标准

- `go build ./...`、`go vet ./...`、`go test ./agent-engine/... ./crux-kernel/...` 全绿。
- **每个能力只有一条路进入循环**（Hooks 或事件轴），`AgentLoopConfig` 无新增 func 字段。
- chat/tui/chat-app 行为与改造前一致（对照测试）。
- 新增一个能力（如加一个新的 defaults 插件）不再需要改 `engine/*.go` 任何一行。
- `docs/06` / `DESIGN.md` / `README.md` 与代码对齐。
