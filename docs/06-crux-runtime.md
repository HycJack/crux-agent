# 模块设计：crux-kernel — 统一容器层

> 模块: crux-kernel（已实现）
> 版本: v0.1.0（设计草案） | 更新: 2026-08-14
> 状态: ✅ 已实现

---

## 0. TL;DR

**问题**：crux-agent 现有 13 个模块各自独立，缺少统一的"运行时容器"来编排插件生命周期、服务依赖和多租户隔离。`AgentLoopConfig` 的 15+ 字段需要调用方手动组装，且插件（Compactor / Memory / Tool 等）没有统一的加载/卸载/重载机制。

**方案**：`crux-kernel` 模块提供三层抽象：
1. **Container** — 服务注册/查询的统一容器（替代 `AgentLoopConfig` 平铺字段）
2. **PluginFiber** — 插件生命周期状态机（pending → active → disposed）
3. **EventBus** — 支持多派发模式的事件总线（parallel / serial / bail / waterfall）

**原则**：借鉴 [Cordis](https://github.com/cordiverse/cordis) 的 Fiber/Context/Reflect 思想，但用 Go 的显式风格表达，**不照搬** TS 的 Proxy/装饰器/模块合并。

**非目标**：不做配置驱动加载、不做 HMR、不做动态 Proxy 拦截。这些是 Cordis 作为"通用应用框架"的需求，crux-agent 作为"Agent 运行时"应保持显式组装。

---

## 1. 背景与动机

### 1.1 现状问题

当前 crux-agent 的模块组装方式存在三个痛点：

**痛点 1：组装心智成本高**

[agent-engine/engine/types.go](../agent-engine/engine/types.go) 的 `AgentLoopConfig` 有 15+ 字段，调用方需要手动填充每一个：

```go
// 现状：调用方需要理解所有字段
config := agent.AgentLoopConfig{
    Model:               model,
    SystemPrompt:        prompt,
    Tools:               tools,
    StreamFn:            streamFn,
    MaxRounds:           25,
    ConvertToLlm:        learner.Process,
    TransformContext:    ctxMgr.Transform,
    GetApiKey:           func() string { return apiKey },
    BeforeToolCall:      approvalGate.Check,
    AfterToolCall:       auditHook.After,
    PrepareNextTurn:     ctxMgr.PrepareNext,
    GetSteeringMessages: steerMgr.Get,
    GetFollowUpMessages: followMgr.Get,
    ShouldStopAfterTurn: stopper.ShouldStop,
    OnEvent:             eventLogger.Log,
}
```

对比 Cordis 的声明式注入：

```typescript
// Cordis 风格（TS 专属，Go 做不到）
ctx.logger       // 自动注入
ctx.timer        // 自动注入
ctx.http.config  // 自动拦截
```

**痛点 2：插件无生命周期管理**

当前插件（Compactor、Memory、Session、ToolPlugin）是"一次性配置"，运行时无法安全替换：

```go
// 现状：运行中切换 Compactor 不安全
config.TransformContext = newCompactor.Transform  // 旧 compactor 资源泄漏？
```

crux-plugin v2-5 计划做 "hot-reload"，但缺少基础设施。

**痛点 3：多租户/多会话无隔离机制**

crux-plugin v2-12 计划做 "multi-user isolation"，但当前所有插件共享同一份配置。一个进程服务多个用户时，Session/Memory 会互相污染。

### 1.2 Cordis 的启示

Cordis 的核心三件套（Context + Fiber + Reflect）解决了上述问题：

| Cordis 抽象 | 解决的问题 | crux-agent 对应物 | 完成度 |
|------------|----------|-------------------|--------|
| **Fiber**（生命周期状态机） | 插件加载/卸载/重载 | crux-turn 的 Turn FSM（仅单轮对话） | 60% |
| **Context**（运行时容器） | 服务聚合 + 依赖注入 | `AgentLoopConfig` 的字段平铺 | 30% |
| **Reflect**（服务发现） | 动态注册 + 依赖通知 | plugin/types.go 的 7 个接口 | 70% |
| **Events**（事件系统） | parallel/bail/waterfall | 11 种 AgentEvent（仅广播） | 80% |

**关键发现**：crux-agent 的"零件"齐全，但缺少"组装车间"。

### 1.3 Go 语言的限制

Cordis 的核心能力依赖 TypeScript 的两个特性，**Go 都没有**：

| TS 特性 | Cordis 用法 | Go 替代方案 |
|---------|------------|-----------|
| Proxy 动态拦截 | `ctx.logger` 自动注入 | interface 显式注入 |
| `declare module` 合并 | 跨包扩展 Context 类型 | 大 struct 聚合 / interface 组合 |

**推论**：crux-kernel 会比 Cordis 更像 Spring/Dagger，而非 Cordis 本身。设计上选择"显式 struct + interface"，而非"动态 Proxy + 装饰器"。

---

## 2. 设计目标

### 2.1 必须达成

- **G1**: 把 `AgentLoopConfig` 的 15+ 字段收编为 1 个 Container 注入
- **G2**: 插件有统一的生命周期状态机（pending → active → disposed）
- **G3**: 事件系统支持 parallel / serial / bail / waterfall 四种派发模式
- **G4**: 支持派生隔离容器（Isolate），为多租户打基础
- **G5**: 零破坏性 — 现有 `AgentLoopConfig` API 保留，Container 作为可选入口

### 2.2 明确不做

- **N1**: 不做配置驱动加载（YAML → 自动构建）— 保持代码组装
- **N2**: 不做 HMR 热重载 — 作为 crux-plugin v2 的独立特性
- **N3**: 不做动态 Proxy 拦截 — Go 没有这个能力
- **N4**: 不做装饰器注入（`@Inject`）— Go 没有装饰器
- **N5**: 不做运行时服务发现（运行中动态注册新服务）— 编译期固定

---

## 3. 架构定位

### 3.1 模块依赖关系

```
┌─────────────────────────────────────────────────────────────┐
│  应用层：crux-agent-tui · crux-agent-chat · chat-app       │
└─────────────────────────────────────────────────────────────┘
          ↓                    ↓
┌──────────────────┐   ┌──────────────────┐
│  crux-kernel     │   │  agent-engine    │
│  (已实现)        │←──│  (已实现)        │
│                  │   │                  │
│  · Container     │   │  · AgentLoop     │
│  · PluginFiber   │   │  · Pipeline      │
│  · EventBus      │   │  · Tools         │
└──────────────────┘   └──────────────────┘
          ↓                    ↓
┌─────────────────────────────────────────────────────────────┐
│  基础层：crux-ai · crux-plugin · crux-turn · crux-memory    │
└─────────────────────────────────────────────────────────────┘
```

**关键决策**：
- `crux-kernel` 位于 runtime 层，**不依赖** `agent-engine`
- `agent-engine` **可选依赖** `crux-kernel`（向后兼容）
- `crux-kernel` 只依赖 stdlib + `crux-ai/core`（类型）

### 3.2 与现有模块的关系

| 现有模块 | 与 crux-kernel 的关系 |
|---------|----------------------|
| `crux-ai` | crux-kernel 依赖其 `core` 包的类型（Model / Context / Message） |
| `agent-engine` | **可选**：用 Container 替代 AgentLoopConfig，旧的 Config API 保留 |
| `crux-agent-harness` | 各 concern（Session/Memory/Approval/...）注册为 Container 服务 |
| `crux-plugin` | ToolPlugin 包装为 PluginFiber，获得生命周期管理 |
| `crux-turn` | Turn FSM 独立运行，不纳入 Container（职责不同） |
| `crux-memory` | 4 层记忆注册为 Container 服务 |

---

## 4. 核心抽象

### 4.1 Container — 服务容器

```go
// crux-runtime/container/container.go
package container

// Container 是 Agent 运行时的统一容器。
// 替代 AgentLoopConfig 的平铺字段，提供有状态的服务聚合。
type Container struct {
    mu       sync.RWMutex
    services map[reflect.Type]any    // 按类型注册的服务
    plugins  map[string]*PluginFiber // 按名称注册的插件
    events   *EventBus              // 事件总线
    isolates map[string]*Container   // 派生的隔离容器
    parent   *Container              // 父容器（isolate 场景）
    state    State                   // 容器自身状态
    disposers []func() error        // 清理函数
}

// State 容器状态
type State int

const (
    StateStarting State = iota
    StateActive
    StateDisposing
    StateDisposed
)

// --- 服务注册 ---

// Register 注册服务实例。按值的 Go 类型索引。
// 重复注册同类型会覆盖旧的（旧实例的 Dispose 会被调用）。
func (c *Container) Register(svc any) error

// Get 按类型获取服务。svc 必须是指针或接口。
// 未注册时返回 ErrServiceNotFound。
func (c *Container) Get(svc any) error

// MustGet 同 Get，未注册时 panic（仅用于启动期）。
func (c *Container) MustGet(svc any) any

// --- 插件管理 ---

// Plugin 加载插件并管理其生命周期。
// fn 返回 disposer（清理函数），插件状态 pending → active。
func (c *Container) Plugin(name string, fn PluginFunc) *PluginFiber

// PluginFunc 插件加载函数
type PluginFunc func(c *Container) (disposer func() error, err error)

// --- 隔离 ---

// Isolate 派生隔离容器。新容器继承父容器的服务（只读），
// 但可以注册自己的同名服务覆盖父容器。
func (c *Container) Isolate(name string) *Container

// --- 生命周期 ---

// Start 启动容器：调用所有插件的加载函数，状态 → active。
func (c *Container) Start(ctx context.Context) error

// Dispose 卸载容器：逆序调用所有 disposer，状态 → disposed。
func (c *Container) Dispose() error

// State 返回容器当前状态。
func (c *Container) State() State
```

**设计要点**：

1. **按类型注册**：Go 没有 `declare module`，用 `reflect.Type` 做类型索引，`Get(&logger)` 自动推导。
2. **Plugin 与 Service 分离**：Service 是无状态依赖（Model / Logger），Plugin 是有状态可卸载的（Session / Memory）。
3. **Isolate 继承父容器**：子容器可以 `Get` 父容器的服务，但 `Register` 只影响自己。
4. **Dispose 逆序调用**：后注册的先卸载，保证依赖关系正确。

### 4.2 PluginFiber — 插件生命周期状态机

```go
// crux-runtime/fiber/fiber.go
package fiber

// State 插件状态
type State string

const (
    StatePending  State = "pending"   // 已注册，未加载
    StateLoading  State = "loading"   // 加载中
    StateActive   State = "active"    // 已激活
    StateFailed   State = "failed"    // 加载失败
    StateDisposed State = "disposed"  // 已卸载
)

// PluginFiber 管理单个插件的生命周期。
// 借鉴 Cordis Fiber 的状态机，但简化为 5 状态（去掉 UNLOADING）。
type PluginFiber struct {
    Name     string
    State    State
    Config   any           // 插件配置（用于 Reload 比较）
    Epoch    string        // 配置版本号（Reload 时变更）

    disposer func() error  // 清理函数（PluginFunc 返回）
    err      error         // 失败原因（StateFailed 时）
    mu       sync.Mutex
}

// Reload 重新加载插件：先 Dispose 旧的，再用新 config 加载。
// 如果新 config 的 Epoch 与旧的一致，跳过（无变化）。
func (f *PluginFiber) Reload(container *container.Container, config any) error

// Dispose 卸载插件，调用 disposer，状态 → disposed。
func (f *PluginFiber) Dispose() error

// Err 返回失败原因（StateFailed 时）。
func (f *PluginFiber) Err() error
```

**状态转换图**：

```
                   Register()
                       │
                       ▼
                  ┌─────────┐
         ┌───────│ Pending │
         │       └────┬────┘
         │            │ Start()
         │            ▼
         │       ┌─────────┐
         │       │ Loading │
         │       └────┬────┘
         │            │
         │     ┌──────┴──────┐
         │     │             │
         │     ▼             ▼
         │ ┌─────────┐  ┌─────────┐
         │ │ Active  │  │ Failed  │
         │ └────┬────┘  └─────────┘
         │      │
         │      │ Reload()
         │      ▼
         │ ┌─────────┐
         └→│ Active  │ (新 epoch)
           └────┬────┘
                │ Dispose()
                ▼
           ┌──────────┐
           │ Disposed │
           └──────────┘
```

**设计要点**：

1. **5 状态而非 6**：Cordis 的 `UNLOADING` 在 Go 中用同步 Mutex 即可表达，不需要独立状态。
2. **Epoch 比较**：Reload 时若 Epoch 一致则跳过，避免无意义重载。
3. **Config any**：插件自定义配置类型，Fiber 不关心具体内容，只比较 Epoch。
4. **失败可恢复**：`StateFailed` 后可以重新 `Reload`，不需要重建 Fiber。

### 4.3 EventBus — 事件总线

```go
// crux-runtime/events/bus.go
package events

// DispatchMode 派发模式
type DispatchMode int

const (
    // DispatchBroadcast 广播：所有 handler 并发执行，不等待，不收集结果。
    // 用途：日志、监控、UI 刷新。
    DispatchBroadcast DispatchMode = iota

    // DispatchParallel 并行：所有 handler 并发执行，等待全部完成。
    // 任一 handler 返回 error 会被聚合到返回值。
    // 用途：EventTurnEnd（异步日志 + 持久化 + 学习并行）。
    DispatchParallel

    // DispatchSerial 串行：handler 按顺序执行，前一个完成后才执行下一个。
    // 用途：需要严格顺序的处理链。
    DispatchSerial

    // DispatchBail 短路：handler 串行执行，任一返回非 nil 立即停止。
    // 用途：BeforeToolCall 审批门（任一拒绝即停）。
    DispatchBail

    // DispatchWaterfall 瀑布流：handler 串行执行，前一个的返回值作为后一个的输入。
    // 用途：AfterToolCall 结果变换链（脱敏 → 格式化 → 校验）。
    DispatchWaterfall
)

// Handler 事件处理器
type Handler func(ctx context.Context, event Event) (any, error)

// EventBus 事件总线
type EventBus struct {
    handlers map[string][]handlerEntry
    mu       sync.RWMutex
}

type handlerEntry struct {
    mode    DispatchMode
    handler Handler
    name    string  // handler 标识（用于 Unsubscribe）
}

// On 注册监听器。
// eventType: 事件类型名（如 "tool_exec_end"、"before_tool_call"）。
// mode: 该 handler 所属组采用何种派发模式（同 eventType 下可有多个组）。
func (b *EventBus) On(eventType string, mode DispatchMode, handler Handler) string

// Once 注册一次性监听器（触发一次后自动移除）。
func (b *EventBus) Once(eventType string, mode DispatchMode, handler Handler) string

// Off 取消监听。
func (b *EventBus) Off(handlerID string)

// Emit 派发事件。
// 返回值取决于派发模式：
//   - Broadcast: 总是返回 nil
//   - Parallel:  返回聚合的 errors
//   - Serial:    返回最后一个 handler 的结果
//   - Bail:       返回第一个非 nil 结果（短路）
//   - Waterfall:  返回最后一个 handler 的结果（链式传递）
func (b *EventBus) Emit(ctx context.Context, eventType string, event Event) (any, error)

// Event 基础事件接口
type Event interface {
    Type() string
    Timestamp() time.Time
}
```

**与现有 AgentEvent 的关系**：

现有 11 种 `AgentEvent`（AgentStart/End、TurnStart/End、MessageStart/Update/End、ToolExecStart/Update/End、Retry、QueueUpdate）**不变**，它们实现 `Event` 接口即可接入 EventBus：

```go
// 现有 AgentEvent 自动满足 Event 接口
func (e EventToolExecEnd) Type() string { return "tool_exec_end" }
func (e EventToolExecEnd) Timestamp() time.Time { return e.Timestamp }
```

**派发模式应用场景**：

| 事件 | 推荐模式 | 理由 |
|------|---------|------|
| `EventAgentStart` | Broadcast | 通知所有观察者，无需等待 |
| `BeforeToolCall` | Bail | 审批门：任一拒绝即停 |
| `AfterToolCall` | Waterfall | 结果变换链：脱敏 → 格式化 |
| `EventTurnEnd` | Parallel | 日志 + 持久化 + 学习并行 |
| `EventMessageUpdate` | Broadcast | 流式 delta，只广播 |

---

## 5. 使用示例

### 5.1 基础用法：替代 AgentLoopConfig

```go
package main

import (
    "context"
    "github.com/crux-agent/crux-kernel/container"
    "github.com/crux-agent/crux-kernel/events"
    "github.com/crux-agent/crux-kernel/fiber"
)

func main() {
    ctx := context.Background()

    // 1. 创建容器
    c := container.New()

    // 2. 注册服务（替代 AgentLoopConfig 的字段）
    c.Register(model)                    // core.Model
    c.Register(ctxMgr)                   // *ContextManager
    c.Register(memory)                   // *Memory
    c.Register(learner)                  // *AutoLearn
    c.Register(approvalGate)             // *ApprovalGate

    // 3. 注册插件（带生命周期的）
    c.Plugin("session", func(c *container.Container) (func() error, error) {
        sess := session.New(config)
        c.Register(sess)
        return sess.Close, nil
    })

    c.Plugin("tool-runner", func(c *container.Container) (func() error, error) {
        runner := tools.NewRunner()
        c.Register(runner)
        return runner.Stop, nil
    })

    // 4. 注册事件
    c.Events().On("before_tool_call", events.DispatchBail, func(ctx context.Context, evt events.Event) (any, error) {
        gate := c.MustGet(&ApprovalGate{}).(*ApprovalGate)
        return gate.Check(ctx, evt)
    })

    // 5. 启动容器（加载所有插件）
    if err := c.Start(ctx); err != nil {
        panic(err)
    }
    defer c.Dispose()

    // 6. 运行 Agent（从容器取服务）
    agentLoop := agent.NewWithContainer(c)
    stream := agentLoop.Run(ctx, messages)
    // ...
}
```

### 5.2 多租户隔离

```go
// 一个进程服务多个用户
root := container.New()
root.Register(sharedModel)       // 共享 LLM 客户端
root.Register(sharedCompactor)  // 共享压缩器

// 用户 1 的隔离容器
user1 := root.Isolate("user-1")
user1.Register(session1)         // 各自的 Session
user1.Register(memory1)          // 各自的 Memory
user1.Plugin("user1-session", func(c *container.Container) (func() error, error) {
    return session1.Close, nil
})

// 用户 2 的隔离容器
user2 := root.Isolate("user-2")
user2.Register(session2)
user2.Register(memory2)

// user1.Get(&Model{}) → 拿到 root 的 sharedModel（继承）
// user1.Get(&Session{}) → 拿到 session1（自己的）
// user2.Get(&Session{}) → 拿到 session2（自己的，互不干扰）
```

### 5.3 插件热重载

```go
// 运行中替换 Compactor
fiber := c.GetPlugin("compactor")
oldConfig := fiber.Config.(*CompactorConfig)

newConfig := &CompactorConfig{
    Strategy:  "llm",
    MaxTokens: 8000,  // 调整 token 上限
}
newConfig.Epoch = "v2"  // 版本号变更

// 安全重载：先 dispose 旧 compactor，再加载新的
if err := fiber.Reload(c, newConfig); err != nil {
    log.Printf("重载失败，保留旧 compactor: %v", err)
}
```

### 5.4 事件系统组合用法

```go
bus := c.Events()

// BeforeToolCall: 审批门（bail 短路）
bus.On("before_tool_call", events.DispatchBail, approvalGate.Check)
bus.On("before_tool_call", events.DispatchBail, rateLimiter.Check)  // 第二道门

// AfterToolCall: 结果变换链（waterfall）
bus.On("after_tool_call", events.DispatchWaterfall, desensitizer.Process)  // 脱敏
bus.On("after_tool_call", events.DispatchWaterfall, formatter.Format)     // 格式化
bus.On("after_tool_call", events.DispatchWaterfall, validator.Validate)   // 校验

// EventTurnEnd: 并行副作用
bus.On("turn_end", events.DispatchParallel, persistence.Save)
bus.On("turn_end", events.DispatchParallel, learner.Learn)
bus.On("turn_end", events.DispatchParallel, metrics.Record)

// EventMessageUpdate: 纯广播
bus.On("message_update", events.DispatchBroadcast, ui.Render)
```

---

## 6. 与现有模块的集成

### 6.1 agent-engine 集成（可选）

[agent-engine/engine/agent.go](../agent-engine/engine/agent.go) 新增 `WithContainer` 选项，旧的 `AgentLoopConfig` API 保留：

```go
// agent-engine/engine/agent.go

// Option Agent 构造选项
type Option func(*Agent)

// WithContainer 从容器读取服务，替代手动 config。
// 优先级：显式 Config > Container > 默认值。
func WithContainer(c *container.Container) Option {
    return func(a *Agent) {
        // 从容器读取服务
        var model core.Model
        if err := c.Get(&model); err == nil {
            a.config.Model = model
        }

        var ctxMgr *ContextManager
        if err := c.Get(&ctxMgr); err == nil {
            a.config.TransformContext = ctxMgr.Transform
        }

        var approval *ApprovalGate
        if err := c.Get(&approval); err == nil {
            a.config.BeforeToolCall = approval.Check
        }

        // 把 Agent 的事件桥接到 EventBus
        a.config.OnEvent = func(evt AgentEvent) {
            c.Events().Emit(a.ctx, evt.Type(), evt)
        }
    }
}

// NewWithContainer 从容器构造 Agent
func NewWithContainer(c *container.Container, opts ...Option) *Agent {
    return New(append(opts, WithContainer(c))...)
}
```

**迁移策略**：
- 现有代码继续用 `New(config)` 不受影响
- 新代码可以用 `NewWithContainer(c)` 简化组装
- 两种方式可混用（Option 模式）

### 6.2 harness 各 concern 的注册

```go
// crux-agent-harness/session/session.go
// 新增 Container 注册辅助函数

func Register(c *container.Container, config Config) error {
    return c.Plugin("session", func(c *container.Container) (func() error, error) {
        sess := New(config)
        c.Register(sess)
        return sess.Close, nil
    }).Err()
}

// crux-agent-harness/memory/memory.go
func Register(c *container.Container, config Config) error {
    return c.Plugin("memory", func(c *container.Container) (func() error, error) {
        mem := New(config)
        c.Register(mem)
        return mem.Close, nil
    }).Err()
}
```

### 6.3 crux-plugin 子进程插件

```go
// crux-plugin/manager.go
// 把子进程插件包装为 PluginFiber

func (m *Manager) RegisterPlugin(c *container.Container, name string, path string) error {
    return c.Plugin(name, func(c *container.Container) (func() error, error) {
        proc, err := m.startProcess(path)
        if err != nil {
            return nil, err
        }
        plugin := newClient(proc)
        c.Register(plugin)
        return func() error {
            return proc.Kill()
        }, nil
    }).Err()
}

// 热重载：重启子进程
func (m *Manager) ReloadPlugin(c *container.Container, name string) error {
    fiber := c.GetPlugin(name)
    if fiber == nil {
        return ErrPluginNotFound
    }
    return fiber.Reload(c, fiber.Config)
}
```

---

## 7. 迁移路径

### Phase 1：crux-kernel 核心实现（v0.1）✅ 已完成

**范围**：新建 `crux-kernel` 模块，实现 Container + PluginFiber + EventBus。

**交付物**：
- `crux-kernel/container/` — Container + Isolate
- `crux-kernel/fiber/` — PluginFiber 状态机
- `crux-kernel/events/` — EventBus + 5 种派发模式
- 单元测试覆盖 ≥ 80%

**验收标准**：
- Container 能注册/获取 10+ 种服务类型
- PluginFiber 状态转换正确（pending → active → disposed）
- EventBus 5 种派发模式行为符合预期
- Isolate 派生容器能继承父容器服务

**不涉及**：agent-engine / harness / plugin 的改动（保持零破坏）

### Phase 2：agent-engine 可选集成（v0.2）✅ 已完成

**范围**：agent-engine 新增 `WithContainer` 选项，旧 API 保留。

**交付物**：
- `agent.WithContainer(c *container.Container) Option`
- `agent.NewWithContainer(c, opts...) *Agent`
- 示例代码：用 Container 替代 AgentLoopConfig

**验收标准**：
- 现有 `agent.New(config)` 用法完全不变
- `agent.NewWithContainer(c)` 能跑通完整 Agent 循环
- 两种方式产出的事件流一致

**风险**：低。Option 模式向后兼容。

### Phase 3：harness 注册辅助（v0.3）✅ 已完成

**范围**：harness 各 concern 提供 `Register(c, config)` 辅助函数。

**交付物**：
- session.Register(c, config)
- memory.Register(c, config)
- context.Register(c, config)
- approval.Register(c, config)
- checkpoint.Register(c, config)

**验收标准**：
- 现有直接构造方式不受影响
- Register 辅助函数能正确注册到 Container

### Phase 4：crux-plugin 热重载（v0.4）✅ 已完成

**范围**：crux-plugin 的子进程插件包装为 PluginFiber，支持热重载。

**交付物**：
- `Manager.RegisterPlugin(c, name, path)`
- `Manager.ReloadPlugin(c, name)`

**验收标准**：
- 子进程崩溃后能自动重启
- 配置变更后能热重载子进程

### Phase 5：Isolate 多租户（v0.5，可选）

**范围**：基于 Isolate 实现多租户隔离。

**交付物**：
- 多用户场景的示例代码
- Isolate 的服务继承/覆盖语义测试

**触发条件**：crux-plugin v2-12 "multi-user isolation" 开始做时

---

## 8. 关键设计决策记录

### ADR-01：为什么按类型注册，而非按字符串名

**决策**：`c.Register(svc)` 按值的 Go 类型索引，而非 `c.Register("logger", svc)`。

**理由**：
- Go 的 `reflect.Type` 天然唯一，避免字符串命名冲突
- 编译期类型检查（`c.Get(&Logger{})` 拿到的必定是 `*Logger`）
- 参考 fx/wire 等 Go DI 框架的实践

**代价**：同类型只能注册一个实例。如果需要多个 `*Logger`，用 `LoggerRole` 包装：

```go
type LoggerRole struct { Name string; *Logger }
c.Register(LoggerRole{"audit", auditLogger})
c.Register(LoggerRole{"app", appLogger})
```

### ADR-02：为什么 Plugin 和 Service 分离

**决策**：Service 是无状态依赖（Register/Get），Plugin 是有状态可卸载的（Plugin/PluginFiber）。

**理由**：
- `core.Model`、`Logger` 这类无状态服务不需要生命周期管理
- `Session`、`Memory`、子进程插件需要 Start/Stop/Reload
- 混在一起会让简单场景复杂化

**对比 Cordis**：Cordis 的 Fiber 同时管理两者，但 TS 有 Proxy 帮助区分。Go 必须显式区分。

### ADR-03：为什么不做动态服务发现

**决策**：服务在 `Start()` 前注册，运行时不能动态新增。

**理由**：
- Go 的 interface 是编译期契约，运行时新增类型无意义
- crux-agent 的场景是"启动时组装"，不是"运行时插件市场"
- 动态注册需要处理"新服务如何通知已有依赖"，复杂度爆炸

**对比 Cordis**：Cordis 的 `ctx.reflect.provide()` 支持运行时动态注册 + notify。crux-agent 不需要。

### ADR-04：为什么 EventBus 保留现有 AgentEvent 类型

**决策**：不重新定义事件类型，现有 11 种 `AgentEvent` 实现 `events.Event` 接口即可。

**理由**：
- 现有事件类型是稳定的 API 契约，TUI/Chat 都依赖
- EventBus 只增加"派发模式"能力，不改事件结构
- 避免破坏性变更

**代价**：现有 `AgentEvent` 需要补两个方法（`Type() string` + `Timestamp() time.Time`）。可以用 embedding 实现：

```go
type EventBase struct {
    Timestamp_ time.Time
}
func (e EventBase) Timestamp() time.Time { return e.Timestamp_ }

type EventToolExecEnd struct {
    EventBase
    // 现有字段...
}
```

### ADR-05：为什么 5 状态而非 Cordis 的 6 状态

**决策**：PluginFiber 用 5 状态（pending / loading / active / failed / disposed），去掉 Cordis 的 `UNLOADING`。

**理由**：
- Cordis 的 `UNLOADING` 用于异步卸载场景（dispose 可能是 Promise）
- Go 的卸载是同步的（`func() error`），用 Mutex 保护即可
- 减少状态 = 减少边界条件

**代价**：如果未来需要异步卸载（如子进程 graceful shutdown），需要加 `StateUnloading`。但 YAGNI，先不加。

---

## 9. 与 Cordis 的对比

| 维度 | Cordis | crux-kernel |
|------|--------|--------------|
| **语言** | TypeScript | Go |
| **核心抽象** | Context + Fiber + Reflect（三件套） | Container + PluginFiber + EventBus |
| **服务注入** | `ctx.logger` 自动注入（Proxy） | `c.Get(&logger)` 显式查询（reflect） |
| **依赖声明** | `@Inject()` 装饰器 | 构造函数参数 |
| **类型扩展** | `declare module` 跨包合并 | 大 struct 聚合 |
| **生命周期** | 6 状态（含 UNLOADING） | 5 状态（同步卸载） |
| **动态注册** | 运行时 `ctx.reflect.provide()` | 仅启动期 `c.Register()` |
| **事件模式** | 5 种（emit/parallel/serial/bail/waterfall） | 5 种（broadcast/parallel/serial/bail/waterfall） |
| **隔离** | isolate/island 命名空间 | Isolate 派生容器 |
| **配置驱动** | loader + include（YAML） | 不做 |
| **HMR** | hmr 包（完整依赖图分析） | 不做（留给 crux-plugin v2） |
| **Proxy 使用** | 重度（Context/Service/traceable 三层） | 零 |

**核心差异**：Cordis 是"动态声明式"（运行时可变），crux-kernel 是"静态显式"（编译期固定）。

---

## 10. 测试计划

### 10.1 单元测试

| 测试 | 模块 | 说明 |
|------|------|------|
| TestContainer_RegisterGet | container | 注册/获取服务 |
| TestContainer_DuplicateRegister | container | 重复注册覆盖 + dispose 旧实例 |
| TestContainer_GetNotFound | container | 未注册服务返回 ErrServiceNotFound |
| TestContainer_Isolate | container | 子容器继承父容器服务 |
| TestContainer_IsolateOverride | container | 子容器覆盖父容器服务 |
| TestContainer_DisposeOrder | container | disposer 逆序调用 |
| TestPluginFiber_StateMachine | fiber | pending → active → disposed |
| TestPluginFiber_Reload | fiber | Reload 时先 dispose 再加载 |
| TestPluginFiber_ReloadSameEpoch | fiber | Epoch 一致时跳过 |
| TestPluginFiber_FailedRecover | fiber | Failed 后可重新 Reload |
| TestEventBus_Broadcast | events | 广播：不等待 |
| TestEventBus_Parallel | events | 并行：等待全部 |
| TestEventBus_Serial | events | 串行：顺序执行 |
| TestEventBus_Bail | events | 短路：首个非 nil 即停 |
| TestEventBus_Waterfall | events | 瀑布流：链式传递 |
| TestEventBus_Once | events | 一次性监听器 |

### 10.2 集成测试

| 测试 | 说明 |
|------|------|
| TestIntegration_AgentWithContainer | 用 Container 驱动完整 Agent 循环 |
| TestIntegration_MultiTenant | 多用户隔离场景 |
| TestIntegration_PluginReload | 运行中重载插件 |
| TestIntegration_EventChaining | 事件链组合（bail + waterfall + parallel） |

### 10.3 性能测试

| 基准 | 说明 |
|------|------|
| BenchmarkContainer_Get | 服务查询性能（map 查找 + reflect） |
| BenchmarkEventBus_Broadcast | 广播性能 |
| BenchmarkEventBus_Bail | 短路性能 |

---

## 11. 后续计划

### 短期（v0.1 - v0.3）✅ 已完成

- [x] 实现 Container + PluginFiber + EventBus
- [x] agent-engine 集成 WithContainer 选项
- [x] harness 各 concern 提供 Register 辅助
- [x] 单元测试覆盖 ≥ 80%

### 中期（v0.4 - v0.5）✅ 已完成

- [x] crux-plugin 子进程插件接入 PluginFiber
- [x] 子进程崩溃自动重启
- [ ] Isolate 多租户场景验证

### 长期（v1.0+）

- [ ] 可选的 YAML 配置加载器（作为独立包，不进核心）
- [ ] 插件依赖图分析（借鉴 Cordis HMR 的依赖图算法）
- [ ] 跨进程 Container（基于 crux-plugin 的 RPC）

---

## 12. 参考资料

- [Cordis 源码](https://github.com/cordiverse/cordis) — Fiber/Context/Reflect 三件套的原始实现
- [Cordis 论文](https://github.com/cordiverse/paper) — 时空可组合性理论
- [Go fx](https://pkg.go.dev/go.uber.org/fx) — Go 的 DI 框架，Container 的类型注册参考
- [ADR 架构决策记录](https://adr.github.io/) — 本文档第 8 节的格式参考

---

## 附录 A：完整 API 速查

```go
// Container
func New() *Container
func (c *Container) Register(svc any) error
func (c *Container) Get(svc any) error
func (c *Container) MustGet(svc any) any
func (c *Container) Plugin(name string, fn PluginFunc) *PluginFiber
func (c *Container) Isolate(name string) *Container
func (c *Container) Start(ctx context.Context) error
func (c *Container) Dispose() error
func (c *Container) State() State
func (c *Container) Events() *EventBus
func (c *Container) GetPlugin(name string) *PluginFiber

// PluginFiber
func (f *PluginFiber) Reload(c *Container, config any) error
func (f *PluginFiber) Dispose() error
func (f *PluginFiber) Err() error

// EventBus
func (b *EventBus) On(eventType string, mode DispatchMode, handler Handler) string
func (b *EventBus) Once(eventType string, mode DispatchMode, handler Handler) string
func (b *EventBus) Off(handlerID string)
func (b *EventBus) Emit(ctx context.Context, eventType string, event Event) (any, error)
```
