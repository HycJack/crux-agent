# agent-engine 设计文档

> 轻量、可嵌入的 Agent 运行时库
> 版本: v0.1.0 | 最后更新: 2026-07-02

---

## 目录

1. [概述](#1-概述)
2. [架构](#2-架构)
3. [核心引擎 (engine/)](#3-核心引擎-engine)
4. [插件接口 (plugin/)](#4-插件接口-plugin)
5. [事件体系](#5-事件体系)
6. [Pipeline/Stage 抽象](#6-pipelinestage-抽象)
7. [集成指南](#7-集成指南)
8. [与外部模块的关系](#8-与外部模块的关系)
9. [设计决策](#9-设计决策)

---

## 1. 概述

### 1.1 是什么

`agent-engine` 是从 `crux-agent-runtime` 中提取出来的核心 Agent 运行时引擎，作为独立、可嵌入的 Go 库。

### 1.2 设计目标

| 目标 | 说明 |
|------|------|
| **轻量核心** | 只包含 Agent 循环引擎，唯一外部依赖是 `crux-ai` |
| **插件化** | Session/Context/Memory/AutoLearn/Tool 等能力通过接口注入 |
| **零内包引用** | engine 核心不引用任何内部包 |
| **可嵌入** | 可直接嵌入 TUI、HTTP 服务、CLI 等任何 Go 程序 |
| **可独立演进** | engine、plugin 接口、默认实现各自独立版本 |

### 1.3 依赖

```
agent-engine ──→ crux-ai/core (唯一编译依赖)
                    │
                    └── 提供: Message, Model, EventStream, Context 等类型
```

`agent-engine` **不依赖**：
- `crux-agent-runtime`（被提取的源项目）
- `crux-agent-harness`（参考其实现理念，但无编译依赖）
- `crux-turn`（独立模块，通过 Adapter 解耦）
- `crux-plugin`（独立模块，通过 ToolPlugin 接口适配）

---

## 2. 架构

### 2.1 模块结构

```
agent-engine/
│
├── engine/                      # 核心引擎 (唯一编译产物)
│   ├── types.go                # 类型定义: AgentEvent, AgentTool, CompactionConfig, AgentLoopConfig
│   ├── agent.go                # Agent 状态管理: New, Run, RunContinue, Abort, Subscribe
│   ├── loop.go                 # AgentLoop 双层事件循环: runLoop (outer) + runInnerLoop
│   ├── stream.go               # 流式处理: streamAssistantResponse, compaction, overflow retry
│   ├── tools.go                # 工具执行: executeToolCalls (parallel/sequential)
│   └── pipeline.go             # Pipeline/Stage/Hook 抽象 (可选高级抽象)
│
├── plugin/                      # 插件接口契约 (仅类型定义，无运行时逻辑)
│   └── types.go                # 7 个插件接口: Session, Context, Memory, AutoLearn, Tool, Approval, Checkpoint
│
├── defaults/                    # (规划中) 默认实现，参考 harness/runtime 现有代码
│
├── go.mod                       # module github.com/hycjack/agent-engine
└── go.sum
```

### 2.2 包依赖关系

```
外部使用者 (TUI / HTTP 服务 / CLI)
    │
    ├── agent-engine/engine      # 直接使用 AgentLoop / Agent 类型
    │       │
    │       └── crux-ai/core     # 唯一编译依赖
    │
    ├── agent-engine/plugin      # 按需实现或注入插件接口
    │
    └── agent-engine/defaults    # (可选) 引用默认实现
```

**关键约束**：
- `engine/` → `crux-ai/core`（单向，唯一依赖）
- `plugin/` → `crux-ai/core`（仅因使用 core.Message 等类型）
- `engine/` 不引用 `plugin/`（通过函数签名注入）
- `plugin/` 不引用 `engine/`（完全解耦）

---

## 3. 核心引擎 (engine/)

### 3.1 Agent 双层事件循环

```
AgentLoop(ctx, messages, config)
    │
    ┌▼──────────────────────────────────────┐
    │  outer loop (runLoop)                 │
    │  每次迭代:                             │
    │    1. runInnerLoop                    │
    │    2. 检查 follow-up messages         │
    │    3. 有 follow-up → 继续 outer loop  │
    │    4. 无 follow-up → 结束             │
    └───────────────────────────────────────┘
            │
    ┌───────▼───────────────────────────────┐
    │  inner loop (runInnerLoop)            │
    │  每次迭代 (每轮 turn):                 │
    │    1. injectSteeringMessages          │
    │    2. streamAssistantResponse         │
    │       ├─ transformContext             │
    │       ├─ maybeCompactPreCall          │
    │       ├─ convertToLLM                 │
    │       ├─ invokeStreamFn               │
    │       └─ consumeStreamEvents          │
    │    3. handleStreamError / overflow    │
    │    4. extractToolCalls                │
    │    5. executeToolCalls (并行/串行)    │
    │    6. PrepareNextTurn / ShouldStop    │
    │    7. 有 toolCalls → 继续 inner loop │
    │    8. 无 toolCalls → 返回 outer loop  │
    └───────────────────────────────────────┘
```

### 3.2 AgentLoopConfig

`AgentLoopConfig` 是所有行为的配置中心。它使用**函数注入**而非接口注入，保持 engine 零外部依赖：

```go
type AgentLoopConfig struct {
    // 模型与提示
    Model         core.Model
    SystemPrompt  string

    // 工具
    Tools         []AgentTool
    ToolExecution ToolExecutionMode

    // 消息转换 (可注入上下文管理)
    ConvertToLlm     func([]core.Message) []core.Message
    TransformContext func([]core.Message) []core.Message

    // API Key 动态解析
    GetApiKey func() string

    // 生命周期回调
    ShouldStopAfterTurn func(AssistantMessage, []ToolResultMessage) bool
    PrepareNextTurn     func(*AgentLoopConfig, ...)

    // 消息注入 (steering / follow-up)
    GetSteeringMessages func() []core.Message
    GetFollowUpMessages func() []core.Message

    // 工具生命周期钩子
    BeforeToolCall func(BeforeToolCallContext) *ToolCallBlock
    AfterToolCall  func(AfterToolCallContext) *ToolCallOverride

    // 自定义流式函数 (默认: crux-ai StreamSimpleWithContext)
    StreamFn StreamFn

    // 上下文压缩 (函数签名，无外部依赖)
    Compaction CompactionConfig
}
```

### 3.3 CompactionConfig — 解耦设计

这是从 `crux-agent-runtime` 迁移时最关键的解耦变更：

**之前 (runtime)**：`Compactor` 和 `TokenCounter` 是具体接口类型，引用了 `context` 包
**之后 (agent-engine)**：改为纯函数签名

```go
type CompactionConfig struct {
    // func(ctx, messages) → (newMessages, changed, error)
    Compactor func(ctx context.Context, msgs []core.Message) (newMsgs []core.Message, changed bool, err error)

    // func(systemPrompt, messages, tools) → token count
    TokenCounter func(systemPrompt string, messages []core.Message, tools []core.Tool) int

    MaxTokens       int    // default: 100000
    ReserveTokens   int    // default: 4096
    OverflowRetries int    // default: 1
    OnCompact       func(prevTokens, newTokens, prevMsgs, newMsgs int)
}
```

使用者只需将 harness 的 `Compactor` 或自实现的压缩器包装成函数签名即可注入。

### 3.4 Agent 状态管理

`Agent` 结构体提供有状态的使用方式：

```go
agent := engine.New(engine.AgentOptions{
    InitialState: &engine.AgentState{
        Model:        model,
        SystemPrompt: "You are a helpful assistant",
        Tools:        tools,
    },
    Compaction: compactionConfig,
})

// 运行时修改配置
agent.SetTools(newTools)
agent.SetModel(newModel)

// 消息管理
agent.SetMessages(history)
agent.Reset()

// 消息注入
agent.Steering(steeringMsg)    // 在当前轮次注入
agent.FollowUp(followUpMsg)    // 在当前轮次结束后注入

// 事件订阅
agent.Subscribe(func(evt engine.AgentEvent) {
    // 实时通知 UI
})

// 运行
result, err := agent.Run(ctx, userMsg)
result, err = agent.RunContinue(ctx)

// 取消
agent.Abort()
```

### 3.5 无状态 API

对于不需要状态的场景，可以直接使用 `AgentLoop`：

```go
stream := engine.AgentLoop(ctx, messages, config)

// 消费事件
go func() {
    stream.ForEach(ctx, func(evt engine.AgentEvent) error {
        // 实时处理
        return nil
    })
}()

// 等待结果
result, err := stream.Result()
```

---

## 4. 插件接口 (plugin/)

### 4.1 接口一览

| 接口 | 方法数 | 说明 | 参考实现 |
|------|:------:|------|---------|
| `SessionPlugin` | 5 | 会话持久化、上下文重建 | harness/session, runtime/session |
| `ContextPlugin` | 5 | Token 管理、上下文压缩 | harness/context/pipeline |
| `MemoryPlugin` | 7 | 长期记忆 KV 存储 | runtime/memory |
| `AutoLearnPlugin` | 3 | 自动从对话中提取记忆 | runtime/autolearn |
| `ToolPlugin` | 4 | 工具定义与执行 | crux-plugin, runtime/tools |
| `ApprovalPlugin` | 1 | 工具审批门 | harness/approval/gate |
| `CheckpointPlugin` | 4 | 消息快照/回滚 | harness/checkpoint |

### 4.2 设计原则

1. **接口在消费方定义**：所有接口定义在 `plugin/` 包中，engine 通过 `AgentLoopConfig` 的函数注入使用
2. **最小接口**：每个接口只包含必要方法，避免肥大接口
3. **无 engine 依赖**：`plugin/` 包只依赖 `crux-ai/core`，不依赖 `engine/`
4. **可 mock**：所有接口都是 Go interface，测试时轻松 mock

### 4.3 关于"plugin"的命名澄清

> `plugin/` 是**抽象接口包**，`crux-plugin` 是**子进程 IPC 框架**——两者完全不同。

| 对比维度 | agent-engine/plugin/ | crux-plugin (独立模块) |
|----------|---------------------|----------------------|
| 本质 | Go 接口定义 | 子进程 JSON-RPC 2.0 框架 |
| 层级 | 抽象层 (契约) | 实现层 (通信机制) |
| 位置 | agent-engine 内部包 | 独立模块 |
| 依赖 | crux-ai/core | 仅 Go stdlib |

**关系**：`crux-plugin` 可以通过适配实现 `plugin.ToolPlugin` 接口，成为 engine 的一种工具后端。

---

## 5. 事件体系

### 5.1 事件类型

engine 使用 `core.EventStream[AgentEvent, []core.Message]` 作为事件流载体，通过 8 种事件类型实现端到端可见性：

```
AgentLoop 生命周期:
  EventAgentStart → [turns...] → EventAgentEnd

单个 Turn 生命周期:
  EventTurnStart → [stream events] → [tool events] → EventTurnEnd

流式响应生命周期:
  EventMessageStart → [EventMessageUpdate × N] → EventMessageEnd

工具执行生命周期:
  EventToolExecStart → [EventToolExecUpdate × N] → EventToolExecEnd
```

### 5.2 事件订阅

```go
agent.Subscribe(func(evt engine.AgentEvent) {
    switch e := evt.(type) {
    case engine.EventAgentStart:
        // Agent 开始运行
    case engine.EventAgentEnd:
        // Agent 运行结束，e.Messages 是最终消息列表
    case engine.EventTurnStart:
        // 新一轮 LLM 调用开始
    case engine.EventTurnEnd:
        // 本轮结束，包含 AssistantMessage 和 ToolResults
    case engine.EventMessageStart:
        // LLM 开始输出
    case engine.EventMessageUpdate:
        // LLM 输出增量 (用于 UI 实时渲染)
    case engine.EventMessageEnd:
        // LLM 输出结束
    case engine.EventToolExecStart:
        // 工具开始执行
    case engine.EventToolExecUpdate:
        // 工具执行中间结果
    case engine.EventToolExecEnd:
        // 工具执行结束
    }
})
```

---

## 6. Pipeline/Stage 抽象

### 6.1 设计动机

传统的 `runLoop` + `runInnerLoop` 虽然功能完整，但存在以下限制：

1. 所有步骤硬编码在 loop.go 中，无法单独替换某个环节
2. 增加"日志记录"、"重试"、"安全审查"等环节需修改核心代码
3. 整个 Loop 作为一个整体测试，无法单独测试某个阶段

Pipeline 提供**增量替代方案**——保留 AgentLoop 为默认实现，Pipeline 作为高级抽象。

### 6.2 核心接口

```go
// Stage 是 Agent Loop 中一个可独立执行的阶段
type Stage interface {
    Name() string
    Run(ctx context.Context, state *RunState) (*RunState, error)
}

// RunState 是跨 Stage 传递的上下文
type RunState struct {
    Messages     []core.Message
    SystemPrompt string
    Tools        []AgentTool
    TextBuffer   string
    ToolCalls    []core.ToolCall
    StopReason   core.StopReason
    Round        int
    MaxRounds    int
    Error        error
    Metadata     map[string]any
}

// Hook 提供 Stage 执行前后的生命周期回调
type Hook interface {
    BeforeStage(ctx context.Context, stageName string, state *RunState)
    AfterStage(ctx context.Context, stageName string, state *RunState, err error)
}

// Pipeline 编排多个 Stage 按顺序执行，支持多轮循环
type Pipeline struct { ... }
```

### 6.3 使用方式

```go
// ReAct 模式
pipeline := engine.NewPipeline([]engine.Stage{
    &ContextCompactionStage{MaxTokens: 100000},
    &LLMInvocationStage{Config: config},
    &ToolExecutionStage{Registry: tools},
    &OutputStage{},
}, engine.WithMaxRounds(50), engine.WithHooks(loggingHook))

finalState, err := pipeline.Run(ctx, &engine.RunState{
    Messages:  []core.Message{userMsg},
    MaxRounds: 30,
})
```

### 6.4 与 AgentLoop 的关系

```
                    ┌─────────────────────┐
                    │   使用者选择:        │
                    │                     │
                    │ 1. Agent.AgentLoop()│ ← 默认实现，开箱即用
                    │    (传统双层循环)    │
                    │                     │
                    │ 2. Pipeline.Run()   │ ← 高级抽象，可自定义 Stage
                    │    (Stage 编排)     │
                    │                     │
                    │ 3. 混合使用          │ ← Pipeline 中嵌入 TurnStage
                    │    (Pipeline +      │    调用 AgentLoop 作为子步骤
                    │     AgentLoop)      │
                    └─────────────────────┘
```

---

## 7. 集成指南

### 7.1 最小集成 (只需 LLM 调用)

```go
import (
    "github.com/hycjack/agent-engine/engine"
    core "github.com/hycjack/crux-ai/core"
    _ "github.com/hycjack/crux-ai/providers"
    "github.com/hycjack/crux-ai/ai"
)

model, _ := ai.GetModel("openai", "gpt-4o")

stream := engine.AgentLoop(ctx, []core.Message{userMsg}, engine.AgentLoopConfig{
    Model:        model,
    SystemPrompt: "You are a helpful assistant",
    Tools:        []engine.AgentTool{...},
})

result, _ := stream.Result()
```

### 7.2 有状态集成 (Agent 对象)

```go
agent := engine.New(engine.AgentOptions{
    InitialState: &engine.AgentState{
        Model:        model,
        SystemPrompt: "You are a helpful assistant",
    },
})

// 订阅事件用于 UI 渲染
agent.Subscribe(func(evt engine.AgentEvent) {
    if update, ok := evt.(engine.EventMessageUpdate); ok {
        ui.Render(update.Message)
    }
})

result, _ := agent.Run(ctx, userMsg)

// 继续对话
result, _ = agent.RunContinue(ctx, followUpMsg)
```

### 7.3 横切关注点注入 (compaction + hooks)

```go
agent := engine.New(engine.AgentOptions{
    InitialState: &engine.AgentState{...},
    Compaction: engine.CompactionConfig{
        Compactor: func(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
            return myCompactor.Compact(ctx, msgs)
        },
        TokenCounter: func(sp string, msgs []core.Message, tools []core.Tool) int {
            return myCounter.Estimate(sp, msgs, tools)
        },
        MaxTokens: 100000,
        OnCompact: func(prev, cur, pmsgs, cmsgs int) {
            metrics.RecordCompaction(prev, cur)
        },
    },
})
```

---

## 8. 与外部模块的关系

### 8.1 依赖关系图

```
                    crux-agent-tui
                    (使用者)
                    │       │
        ┌───────────┘       └───────────┐
        ▼                               ▼
agent-engine                    crux-turn (独立 FSM)
│       │                       │
│       └── crux-plugin         │ (通过 Adapter 解耦)
│           (可选工具后端)        │
│                               │
└─────────── crux-ai/core ──────┘
                │
        ┌───────┴───────┐
        ▼               ▼
  crux-agent-    crux-agent-
  runtime         harness
  (源项目)         (实现参考)
```

### 8.2 引用关系

| 外部模块 | 与 agent-engine 的关系 | 编译依赖? |
|----------|----------------------|:---------:|
| crux-ai | 唯一编译依赖：提供 Message, Model, EventStream 等核心类型 | **是** |
| crux-agent-runtime | 被提取的源项目，agent-loop 和 types 的原始来源 | 否 |
| crux-agent-harness | 实现参考：session/context/approval/checkpoint 的成熟代码 | 否 |
| crux-turn | 独立 9-state Turn FSM，通过 Adapter 接口集成 | 否 |
| crux-plugin | 子进程 JSON-RPC 2.0 插件系统，通过 ToolPlugin 接口适配 | 否 |

---

## 9. 设计决策

### 9.1 决策记录

| # | 决策 | 理由 |
|:--:|------|------|
| 1 | CompactionConfig 用函数签名而非接口 | 消除 engine 对 context 包的编译时依赖 |
| 2 | Pipeline 是增量而非替代 | 保留 AgentLoop() 为默认实现，降低用户心智负担 |
| 3 | 事件用 interface + type switch | 比泛型 + 类型参数更灵活，保留向后兼容 |
| 4 | Agent 有状态封装 vs 纯函数 | 有状态方式更适合 TUI/HTTP 等长时间运行场景 |
| 5 | functions over interfaces 在 config 中 | Go 的函数类型可直接赋值，比单方法接口更简洁 |
| 6 | 不保留 EventStream 泛型实现 | 直接复用 crux-ai 的 core.EventStream |
| 7 | plugin 包独立于 engine | 接口与实现分离，插件可独立 mock 和演进 |

### 9.2 与 crux-agent-runtime 的关键差异

| 方面 | runtime (原版) | agent-engine (新版) |
|------|---------------|-------------------|
| CompactionConfig 类型 | 引用 `ctxpkg.Compactor` 接口 | 纯函数签名 |
| 依赖 | 依赖 context/session 等内包 | 仅 crux-ai |
| 文件组织 | types.go + agent.go + agent-loop.go | 6 个文件：单独 stream/tools/pipeline |
| Pipeline 抽象 | 无 | 新增 Stage/RunState/Hook |
| 插件接口 | 散落在各包 | 统一在 plugin/types.go |

### 9.3 已知限制

1. **Pipeline 内置 Stage 为简化版**：`ContextCompactionStage` 和 `LLMInvocationStage` 当前是 placeholder，需要参考 loop.go 中的完整实现填充
2. **defaults/ 尚未实现**：默认插件实现（session/context/memory 等）需要从 harness 和 runtime 迁移
3. **无内置 metrics**：依赖使用者通过 Event 订阅或 OnCompact 回调自行采集
4. **线程安全**：Agent 使用 `sync.RWMutex` 保护状态，高频事件场景需关注锁竞争

---

*文档结束*
