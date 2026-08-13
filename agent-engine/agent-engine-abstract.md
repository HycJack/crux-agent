# agent-engine 核心抽象方案

> 从 crux-agent-runtime 中提取核心引擎，作为独立、可嵌入的 Agent 运行时库
> 参考: crux-agent-runtime (agent/), crux-agent-harness (session/context/approval/observe/checkpoint), crux-turn, crux-plugin, loop-engineering-plan.md
> 版本: v0.2.0 | 日期: 2026-07-02

---

## 一、设计目标

1. **轻量核心** — 只包含 Agent 循环引擎，无外部依赖（除 crux-ai）
2. **插件化** — Session/Context/Memory/AutoLearn/Turn 等能力通过接口注入
3. **零内包引用** — engine 核心不引用任何内部包
4. **可直接嵌入 TUI** — crux-agent-tui 可直接替换 internal/agent/
5. **可独立演进** — engine、plugins、turn-fsm 各自独立版本
6. **与现有模块兼容** — 参考 harness 的成熟实现，不重新发明轮子

---

## 二、架构全景

```
┌──────────────────────────────────────────────────────────────────────────┐
│                          agent-engine                                    │
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │  engine/  (核心引擎)                                             │   │
│  │  ├── types.go        → AgentEvent, AgentTool, 配置类型           │   │
│  │  ├── agent.go        → Agent 状态管理 (Run/Abort/Steer)          │   │
│  │  ├── loop.go         → AgentLoop 双层事件循环 (runLoop/runInner) │   │
│  │  ├── stream.go       → streamAssistantResponse + 流式处理         │   │
│  │  ├── tools.go        → executeToolCalls (并行/串行)              │   │
│  │  └── pipeline.go     → (新增) Pipeline 编排 + Hook 机制           │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │  plugin/  (插件接口)                                             │   │
│  │  └── types.go        → 所有插件接口定义                          │   │
│  │       ├── SessionPlugin     (会话持久化, 参考 harness/session)   │   │
│  │       ├── ContextPlugin     (上下文管理, 参考 harness/context)   │   │
│  │       ├── MemoryPlugin      (长期记忆, 参考 runtime/memory)      │   │
│  │       ├── AutoLearnPlugin   (自动学习, 参考 runtime/autolearn)   │   │
│  │       └── ToolPlugin        (工具插件, 参考 crux-plugin 协议)     │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │  defaults/ (可选默认实现, 参考 harness 已有代码)                  │   │
│  │  ├── session.go      → JSONL/SQLite 会话存储                      │   │
│  │  ├── context.go      → Pipeline + Compactor + TokenCounter        │   │
│  │  ├── memory.go       → JSON KV 存储                              │   │
│  │  ├── autolearn.go    → 4 种触发源提取器                          │   │
│  │  ├── approval.go     → (新增) 审批门, 参考 harness/approval       │   │
│  │  ├── checkpoint.go   → (新增) 快照/回滚, 参考 harness/checkpoint │   │
│  │  └── observe.go      → (新增) 结构化日志, 参考 harness/observe   │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────────────┐
│                     crux-turn (独立 Turn FSM)                           │
│  9 状态 FSM: received → provisioning → streaming → dispatching          │
│  → awaiting_approval / executing / steering → completed / failed        │
│  类型无关: 通过 Adapter[Msg, Call, Result] 接口解耦消息类型              │
│  @see crux-turn/turn.go, crux-turn/states.go                             │
└──────────────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────────────┐
│                     crux-plugin (独立插件系统)                           │
│  JSON-RPC 2.0 over stdio, 子进程隔离                                    │
│  支持 tool.list / hook.fire 等方法                                      │
│  @see crux-plugin/protocol.go, crux-plugin/manager.go                    │
└──────────────────────────────────────────────────────────────────────────┘

> **⚠️ 注意命名混淆：** 下图中的 `agent-engine/plugin/` 与独立模块 `crux-plugin` 是完全不同的概念。
> 前者是 Go 接口定义包（抽象契约），后者是子进程 IPC 框架（具体实现机制）。
> 详细对比见 [二-A节](#二-a-crux-plugin-与-plugin-接口包的概念辨析)。

---

## 二-A. crux-plugin 与 plugin/ 接口包的概念辨析

> 两个模块都叫 "plugin"，但处于完全不同的抽象层级，这里是重要澄清。

### 核心区别

| 维度 | `crux-plugin` (独立模块) | `agent-engine/plugin/` (接口包) |
|------|------------------------|-------------------------------|
| **全称** | crux-plugin 子进程插件系统 | agent-engine plugin 插件接口定义 |
| **本质** | 子进程 IPC 框架 | Go 接口定义包 |
| **层级** | 实现层 (如何运行插件) | 抽象层 (插件长什么样) |
| **通信** | stdin/stdout JSON-RPC 2.0 | 进程内函数调用 |
| **语言** | 不限 (插件可用任何语言编写) | 仅 Go |
| **隔离** | OS 进程级隔离 | 无隔离 |
| **依赖** | 仅 Go stdlib | 仅 `crux-ai/core` |
| **文件** | `protocol.go`, `process.go`, `manager.go`, `tooladapter.go` | `types.go` (仅接口) |
| **行数** | ~900 行 | ~120 行 |

### 两者的连接关系

```
agent-engine/engine/
    └── loop.go 调用 plugin.ToolPlugin.Execute()
                        │
                        │ (接口)
                        ▼
               ┌──────────────────┐
               │ plugin.ToolPlugin │ ← 接口定义 (agent-engine/plugin/types.go)
               └──────────────────┘
                    ▲          ▲
                    │          │
          ┌─────────┘          └─────────┐
          ▼                               ▼
  ┌────────────────┐           ┌──────────────────────┐
  │ 本地工具实现     │           │ crux-plugin 适配器     │
  │ (进程内函数)     │           │ (通过 JSON-RPC 调子进程)│
  └────────────────┘           └──────────────────────┘
```

`crux-plugin` 是 `plugin.ToolPlugin` 接口的**一种后端实现**——它把 Go 函数调用翻译成 JSON-RPC 请求发送给子进程。
除此之外，`plugin.ToolPlugin` 还可以有纯本地实现（如直接调 SDK）、HTTP 实现（调远程 API）等。

### 适配示例

```go
import (
    cp "github.com/hycjack/crux-plugin"      // 子进程插件系统
    "agent-engine/engine"                     // 引擎核心
    "agent-engine/plugin"                     // 插件接口（本包）
)

// crux-plugin 的 ToolAdapter → plugin.ToolPlugin 适配
func adaptPluginTools(adapters []cp.ToolAdapter) []engine.AgentTool {
    tools := make([]engine.AgentTool, len(adapters))
    for i, a := range adapters {
        a := a
        tools[i] = engine.AgentTool{
            Name:        a.Name,
            Description: a.Description,
            Parameters:  jsonRaw(a.Parameters),
            Execute: func(ctx context.Context, id string, args json.RawMessage,
                          onUpdate func(json.RawMessage)) (engine.AgentToolResult, error) {
                result, err := a.Execute(ctx, args)
                // ...
            },
        }
    }
    return tools
}
```

### 为什么都叫 "plugin"？

- **`crux-plugin`**：取名 "plugin" 是因为它提供了标准的**插件化扩展机制**（类似 VSCode 的插件系统）
- **`agent-engine/plugin/`**：取名 "plugin" 是因为它定义了 engine 的**能力扩展接口**

两者都合理，但组合使用时容易混淆。项目中用以下术语区分：

| 上下文 | 推荐称呼 | 指代 |
|--------|---------|------|
| 讨论跨进程扩展时 | **外部插件系统** / **子进程插件** | `crux-plugin` 模块 |
| 讨论 engine 扩展点时 | **插件接口** / **引擎插件契约** | `agent-engine/plugin/types.go` |
| 讨论具体工具时 | **插件工具** | 某个运行在子进程中的 Tool |
| 讨论适配时 | **插件适配层** | 连接两者的胶水代码 |

---

## 三、核心提取范围

### 3.1 从 `crux-agent-runtime/agent/` 提取（全部进 engine）

| 文件 | 行数 | 说明 | 变更 |
|------|------|------|------|
| types.go | ~285 | 全部类型定义 | **需解耦** CompactionConfig 引用 |
| agent.go | ~340 | Agent 状态管理 | **保留** 100% |
| agent-loop.go | ~680 | 双层事件循环 | **保留** 100%，拆分子文件 |

**合计: ~1,300 行代码进入 engine 核心**

### 3.2 从各插件包提取接口（进 plugin/types.go）

| 源包 | 提取的接口 | 说明 |
|------|-----------|------|
| runtime/session/types.go | SessionStorage, SessionTreeEntry | 只保留接口定义 |
| harness/session/types.go | SessionMetadata, HarnessError | 更完善的会话类型 |
| runtime/context/token.go | TokenCounter 类型 | 函数签名 |
| harness/context/compactor.go | Compactor 接口 (支持 LLM/Sliding/Hybrid) | 更完善的压缩策略 |
| runtime/memory/memory.go | Memory 的 Get/Set/FormatForPrompt | 只保留接口 |
| runtime/autolearn/autolearn.go | Extractor、Trigger | 提取器接口 |
| harness/approval/gate.go | Gate + Rule + Decision | 审批门模式 |
| harness/checkpoint/checkpoint.go | Snapshot + Store | 消息快照/回滚 |

**合计: ~150 行接口定义**

### 3.3 新增: Pipeline/Stage 抽象 (可选但推荐)

参考 `loop-engineering-plan.md` 的 Pipeline 设计：

| 组件 | 说明 | 优先级 |
|------|------|:------:|
| `Stage` 接口 | `Name() string` + `Run(ctx, *RunState) (*RunState, error)` | 高 |
| `RunState` | 跨 Stage 传递的上下文 (Messages, TextBuffer, ToolCalls, Round...) | 高 |
| `Pipeline` | 编排 Stage 顺序执行 + 循环控制 | 高 |
| `Hook` | BeforeStage / AfterStage 注入点 | 中 |
| `StageMiddleware` | 日志/重试/超时包装器 | 低 |

---

## 四、文件迁移计划

### 4.1 迁移后目录结构

```
agent-engine/
├── engine/                      # 核心引擎
│   ├── types.go                # 类型定义（迁移 + 解耦）
│   ├── agent.go                # Agent 状态管理（迁移）
│   ├── loop.go                 # AgentLoop / runLoop / runInnerLoop（迁移）
│   ├── stream.go               # streamAssistantResponse（从 loop.go 拆分）
│   ├── tools.go                # executeToolCalls（从 loop.go 拆分）
│   └── pipeline.go             # Pipeline + Stage + RunState（新增）
│
├── plugin/                      # 插件接口
│   └── types.go                # 所有插件接口定义
│
├── defaults/                    # 默认插件实现（参考 harness 现有代码）
│   ├── session.go              # JSONL/SQLite 会话
│   ├── context.go              # Pipeline + Compactor + MessageCounter
│   ├── memory.go               # JSON KV 存储
│   ├── autolearn.go            # 自动学习提取器
│   ├── approval.go             # 审批门 (参考 harness/approval/gate.go)
│   ├── checkpoint.go           # 快照/回滚 (参考 harness/checkpoint/checkpoint.go)
│   └── observe.go              # 结构化日志 (参考 harness/observe/observe.go)
│
├── go.mod                       # 仅依赖 github.com/hycjack/crux-ai
├── go.sum
└── README.md
```

### 4.2 具体的代码迁移

#### types.go 的解耦变更

**移除的 import：**
```go
// 删除这两行
ctxpkg "crux-agent-runtime/context"
// 不再引用内包
```

**CompactionConfig 改为函数接口（不再引用 context.Compactor / context.TokenCounter）：**

参考 harness 的 `context.Compactor` 接口和 `token.MessageCounter` 设计，将具体类型替换为函数签名：

```go
type CompactionConfig struct {
    // Compactor 改为函数签名，不再引用 ctxpkg.Compactor
    Compactor    func(ctx context.Context, msgs []core.Message) (newMsgs []core.Message, changed bool, err error)

    // TokenCounter 改为函数签名，不再引用 ctxpkg.TokenCounter
    TokenCounter func(systemPrompt string, messages []core.Message, tools []core.Tool) int

    MaxTokens       int
    ReserveTokens   int
    OverflowRetries int
    OnCompact       func(prevTokens, newTokens, prevMsgs, newMsgs int)
}
```

注意：harness 的 `context.Compactor` 接口是 `Compact(ctx, CompactionRequest, opts...) (string, error)`，
而 engine 需要更简洁的签名。两者是兼容的——harness 的 Compactor 可以在 adapter 中包装成函数签名。

#### agent-loop.go 的 import 变更

```go
import (
    core "github.com/hycjack/crux-ai/core"
    // ai.StreamSimpleWithContext → 通过 config.StreamFn 注入
    // contextpkg.* → 通过 config.Compaction 函数接口注入
)
```

### 4.3 Pipeline/Stage 设计（新增）

参考 `loop-engineering-plan.md` 的核心设计：

```go
// Stage 定义 Agent Loop 中的一个独立阶段
type Stage interface {
    Name() string
    Run(ctx context.Context, state *RunState) (*RunState, error)
}

// RunState 是跨 Stage 传递的上下文数据
type RunState struct {
    Messages     []core.Message
    TextBuffer   string
    ToolCalls    []core.ToolCall
    StopReason   core.StopReason
    Round        int
    MaxRounds    int
    Error        error
    Metadata     map[string]any
}

// Hook 在 Stage 执行前后注入自定义逻辑
type Hook interface {
    BeforeStage(ctx context.Context, stageName string, state *RunState)
    AfterStage(ctx context.Context, stageName string, state *RunState, err error)
}

// Pipeline 编排多个 Stage 按顺序执行
type Pipeline struct {
    stages    []Stage
    hooks     []Hook
    maxRounds int
}
```

**Stage 编排示例:**

```go
// ReAct 模式
pipeline := NewPipeline([]Stage{
    &ContextCompactionStage{MaxTokens: 100000},
    &LLMInvocationStage{Provider: provider},
    &ToolExecutionStage{Registry: tools},
    &OutputStage{},
}, WithMaxRounds(50))

// 当前 AgentLoop 的 runLoop + runInnerLoop 可以保留为默认实现，
// Pipeline 作为可选的、更灵活的替代方案
```

**重要原则：** Pipeline 是增量而非替代。保留 `AgentLoop()` / `AgentLoopContinue()` 作为默认实现，
Pipeline 作为可选的高级抽象，让高级用户自定义 Stage 编排。

---

## 五、插件接口定义

```go
// plugin/types.go — 所有插件接口

package plugin

import (
    "context"
    core "github.com/hycjack/crux-ai/core"
)

// ─── SessionPlugin 会话持久化 ───
// 参考: harness/session/session.go + session/types.go

type SessionPlugin interface {
    // ID 返回会话 ID
    ID() string
    // Append 追加条目到会话
    Append(entries ...SessionTreeEntry) error
    // Entries 返回所有会话条目
    Entries() []SessionTreeEntry
    // BuildContext 从会话条目重建上下文
    BuildContext() SessionContext
    // Close 关闭会话
    Close() error
}

// SessionTreeEntry 是会话树中的一个条目
type SessionTreeEntry struct {
    ID        string
    Type      string // message / compaction / model_change / thinking_change / session_info / label
    Timestamp time.Time
    Message   core.Message   // 对 type=message
    Metadata  map[string]any // 其他元数据
}

// SessionContext 是重建后的会话上下文
type SessionContext struct {
    Messages      []core.Message
    ThinkingLevel string
}

// ─── ContextPlugin 上下文管理 ───
// 参考: harness/context/pipeline.go + harness/context/context.go

type ContextPlugin interface {
    AddMessage(msg core.Message) error
    GetMessages() []core.Message
    IsNearLimit(threshold float64) bool
    GetStats() ContextStats
    Compact(ctx context.Context) error
}

type ContextStats struct {
    TotalTokens     int
    MessageCount    int
    Compactions     int
    MaxTokens       int
    AvailableTokens int
    UsagePercent    float64
}

// ─── MemoryPlugin 长期记忆 ───
// 参考: runtime/memory/memory.go

type MemoryPlugin interface {
    Get(key string) (string, bool)
    Set(key, value string)
    SetWithCategory(key, value, category string)
    Delete(key string)
    FormatForPrompt() string
    Hash() string
    Save() error
}

// ─── AutoLearnPlugin 自动学习 ───
// 参考: runtime/autolearn/autolearn.go

type AutoLearnPlugin interface {
    ProcessUserInput(text string) int
    ProcessToolResult(text string) int
    MaybeExtract(ctx context.Context, messages []core.Message, extractor Extractor) bool
}

type Extractor interface {
    Extract(ctx context.Context, messages []core.Message) ([]Trigger, error)
}

type Trigger struct {
    Source  string
    Key     string
    Value   string
    Context string
}

// ─── ToolPlugin 工具插件 ───
// 参考: crux-plugin/protocol.go, crux-plugin/tooladapter.go

type ToolPlugin interface {
    Name() string
    Description() string
    Parameters() []byte
    Execute(ctx context.Context, toolCallID string, params []byte, onUpdate func([]byte)) (ToolResult, error)
}

type ToolResult struct {
    Content   []core.ContentBlock
    Details   []byte
    IsError   bool
    Terminate bool
}

// ─── ApprovalPlugin 审批门 (新增) ───
// 参考: harness/approval/gate.go

type ApprovalResult int

const (
    ApprovalAllow  ApprovalResult = iota
    ApprovalBlock
    ApprovalAsk         // 需要外部回调确认
)

type ApprovalPlugin interface {
    Evaluate(ctx context.Context, toolName string, toolID string, args []byte) (ApprovalResult, string, error)
}

// ─── CheckpointPlugin 快照/回滚 (新增) ───
// 参考: harness/checkpoint/checkpoint.go

type CheckpointPlugin interface {
    Save(label string, messages []core.Message) (string, error)     // 返回快照 ID
    Undo() ([]core.Message, bool)
    Redo() ([]core.Message, bool)
    List() []CheckpointInfo
}
```

### 5.1 接口与现有实现的对应关系

| 插件接口 | runtime 实现 | harness 实现 | 其他参考 |
|----------|-------------|-------------|---------|
| SessionPlugin | session/session.go | session/session.go + jsonl.go | - |
| ContextPlugin | context/manager.go | context/pipeline.go | - |
| MemoryPlugin | memory/memory.go | - | - |
| AutoLearnPlugin | autolearn/autolearn.go | - | - |
| ToolPlugin | - | - | crux-plugin/tooladapter.go |
| ApprovalPlugin | - | approval/gate.go | - |
| CheckpointPlugin | - | checkpoint/checkpoint.go | - |

---

## 六、与 TUI 的集成方式

### 6.1 TUI 集成示意图

```
crux-agent-tui/
├── internal/
│   ├── ui/                   # TUI 渲染（保留）
│   ├── openai/               # LLM Provider（保留）
│   └── provider/             # 提供商抽象（保留）
│
├── (new) internal/agent/     # 替换为 agent-engine/engine/
│   ├── types.go              # → engine/types.go
│   ├── agent.go              # → engine/agent.go
│   ├── loop.go               # → engine/loop.go
│   ├── stream.go             # → engine/stream.go
│   └── tools.go              # → engine/tools.go
│
├── (new) internal/plugins/   # 按需使用的插件实现
│   ├── session.go            # SessionPlugin 实现 (参考 harness/session)
│   ├── context.go            # ContextPlugin 实现 (参考 harness/context)
│   ├── memory.go             # MemoryPlugin 实现 (参考 runtime/memory)
│   ├── autolearn.go          # AutoLearnPlugin 实现 (参考 runtime/autolearn)
│   ├── approval.go           # ApprovalPlugin 实现 (参考 harness/approval)
│   └── checkpoint.go         # CheckpointPlugin 实现 (参考 harness/checkpoint)
│
└── cmd/agent-tui/main.go     # 组装 engine + plugins
```

### 6.2 与 Turn FSM 的集成

`crux-turn` 是一个独立的 9 状态 FSM，可以在 engine 之上或之下工作：

```go
// 方式 A: Turn FSM 在上层，用 Adapter 包装 engine
// 适用于需要细粒度状态机 + 审批流的场景

func main() {
    // 1. 创建 engine
    agent := engine.New(engine.AgentOptions{...})

    // 2. 创建 Turn FSM, 用 Adapter 将 engine 适配为 StreamFn
    turnMachine := turn.New[core.AssistantMessage, core.ToolCall, core.ToolResultMessage](
        turn.NewMemoryStore(),
        &MyAdapter{},
        turn.WithStreamFn(func(ctx context.Context, msgs []turn.TurnMsg, tools []turn.ToolSchema[turn.TurnCall]) (turn.TurnMsg, error) {
            // 调用 engine 的流式接口
            ...
        }),
        turn.WithApproval(approvalSvc),
        turn.WithMaxRounds(50),
    )
}

// 方式 B: engine 内部集成 Turn FSM (作为 Stage)
// 适用于需要渐进式采用的场景

pipeline := NewPipeline([]Stage{
    &ContextCompactionStage{...},
    &TurnStage{...},           // Turn FSM 作为一个 Stage 嵌入
    &OutputStage{...},
})
```

### 6.3 与 crux-plugin（外部插件系统）的集成

> `crux-plugin` 是 `plugin.ToolPlugin` 接口的**一种后端实现**——通过子进程 JSON-RPC 执行工具。它不是 agent-engine 的一部分，而是可选的扩展机制。详见 [二-A节](#二-a-crux-plugin-与-plugin-接口包的概念辨析)。

`crux-plugin` 提供子进程隔离的插件系统，可以作为 ToolPlugin 的后端：

```go
import (
    cp "github.com/hycjack/crux-plugin"      // 子进程插件系统
    "agent-engine/engine"                     // 引擎核心
)

func main() {
    // 1. 启动外部插件管理器
    pluginMgr := cp.NewManager(logger)
    pluginMgr.Discover([]string{"~/.crux/plugins"})
    pluginMgr.StartAll(ctx)
    defer pluginMgr.StopAll()

    // 2. 获取插件工具 → 适配为 engine.AgentTool
    adapters, _ := pluginMgr.RegisterPluginTools(ctx)
    for _, a := range adapters {
        a := a
        engineTools = append(engineTools, engine.AgentTool{
            Name:        a.Name,        // "<pluginID>.<toolName>"
            Description: a.Description,
            Parameters:  json.RawMessage(a.Parameters),
            Execute: func(ctx context.Context, id string, args json.RawMessage,
                          onUpdate func(json.RawMessage)) (engine.AgentToolResult, error) {
                result, err := a.Execute(ctx, args)
                if err != nil {
                    return engine.AgentToolResult{IsError: true}, err
                }
                return engine.AgentToolResult{
                    Content: []core.ContentBlock{core.TextContent{Text: result}},
                }, nil
            },
        })
    }
}
```

### 6.4 main.go 组装示例

```go
package main

import (
    "agent-engine/engine"
    "agent-engine/plugin" // 接口定义
    "crux-agent-tui/internal/plugins"
    "crux-agent-tui/internal/ui"
)

func main() {
    // 1. 创建插件
    sessPlugin := plugins.NewSessionPlugin("./sessions")          // 参考 harness/session
    ctxPlugin := plugins.NewContextPlugin(128000)                 // 参考 harness/context/pipeline
    memPlugin := plugins.NewMemoryPlugin("./memory.json")         // 参考 runtime/memory
    approveGate := plugins.NewApprovalGate()                     // 参考 harness/approval
    checkpoint := plugins.NewCheckpointStore()                    // 参考 harness/checkpoint

    // 2. 创建 Agent
    agent := engine.New(engine.AgentOptions{
        InitialState: &engine.AgentState{
            Model:        model,
            SystemPrompt: "..." + memPlugin.FormatForPrompt(),
            Tools:        tools.All(),
        },
    })

    // 3. 订阅事件 → 通知 UI + 插件
    agent.Subscribe(func(evt engine.AgentEvent) {
        switch e := evt.(type) {
        case engine.EventMessageEnd:
            sessPlugin.Append(plugin.SessionTreeEntry{
                Type: "message", Message: e.Message,
            })
        case engine.EventMessageUpdate:
            ui.UpdateChat(e.Message)
        case engine.EventToolExecEnd:
            ui.ShowToolResult(e.ToolName, e.Result)
        }
    })

    // 4. 配置上下文管理 (参考 harness/context/pipeline.Compact)
    agent.SetCompaction(engine.CompactionConfig{
        Compactor:    func(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
            compacted, result, err := ctxPlugin.CompactWithStrategy(ctx, msgs)
            return compacted, result != nil, err
        },
        TokenCounter: func(systemPrompt string, messages []core.Message, tools []core.Tool) int {
            return ctxPlugin.EstimateTokens(systemPrompt, messages, tools)
        },
        MaxTokens:     100000,
        ReserveTokens: 4096,
    })

    // 5. 运行
    result, err := agent.Run(ctx, userMessages)
}
```

---

## 七、依赖关系图

### 7.1 迁移前（当前）

```
crux-agent-runtime
│
├── agent ──→ context (内包引用) ──→ session (内包引用)
│     │                                      │
│     └── tools ──→ agent                    │
│                                            │
├── memory (独立)                             │
├── autolearn ──→ memory                      │
│                                             │
└── session ──→ crux-ai (外部)                │
      │                                       │
      └── context ──→ session (循环引用风险)   │
```

### 7.2 迁移后

```
agent-engine (新模块)
│
├── engine/ ──→ crux-ai (唯一依赖)
│     │
│     └── plugin/types.go (无依赖)
│
├── defaults/session.go ──→ crux-ai + modernc.org/sqlite (来自 runtime)
├── defaults/context.go ──→ crux-ai + tikToken (来自 harness)
├── defaults/memory.go ──→ (纯 JSON, 来自 runtime)
├── defaults/autolearn.go ──→ memory + crux-ai (来自 runtime)
├── defaults/approval.go ──→ (纯逻辑, 来自 harness)
├── defaults/checkpoint.go ──→ (纯逻辑, 来自 harness)
└── defaults/observe.go ──→ (纯逻辑, 来自 harness)

独立模块 (不依赖 agent-engine):
├── crux-turn ──→ 独立 FSM, 通过 Adapter 适配 agent-engine 或直接使用
└── crux-plugin ──→ 独立插件系统, 通过 ToolPlugin 接口适配
```

**关键变化**：
1. engine/ 不再引用任何内包
2. 所有插件通过接口解耦
3. 默认实现复用 harness/runtime 的成熟代码
4. Turn FSM 和 Plugin 系统保持独立，通过接口集成

### 7.3 新的模块架构

```
┌────────────────────────────────────────────────────────────┐
│                    crux-agent-tui                          │
│  ├── internal/ui/  ├── internal/agent/  ├── internal/plugins/ │
│  (TUI 渲染)           (engine 的副本)      (插件实现)        │
└────────┬───────────────────────┬───────────────────────────┘
         │                       │
         ▼                       ▼
┌─────────────────┐   ┌──────────────────────┐
│  agent-engine   │   │  crux-turn           │
│  ├── engine/    │   │  (独立 Turn FSM)     │
│  ├── plugin/    │   └──────────────────────┘
│  └── defaults/  │
└─────────────────┘
         │
         ▼
┌─────────────────┐
│  crux-ai        │
│  (LLM 类型)      │
└─────────────────┘
```

---

## 八、代码量统计

| 组件 | 行数 | 比例 | 来源 |
|------|------|:----:|------|
| **engine/** | **~1,300** | **28%** | 从 runtime/agent/ 迁移 |
| engine/pipeline.go (新增) | ~200 | 4% | 新增 |
| **plugin/types.go** | **~120** | **3%** | 新建 |
| defaults/session.go | ~600 | 13% | 参考 harness/session + runtime/session |
| defaults/context.go | ~450 | 10% | 参考 harness/context/pipeline |
| defaults/memory.go | ~260 | 6% | 参考 runtime/memory |
| defaults/autolearn.go | ~790 | 17% | 参考 runtime/autolearn |
| defaults/approval.go | ~150 | 3% | 参考 harness/approval/gate.go |
| defaults/checkpoint.go | ~120 | 3% | 参考 harness/checkpoint/checkpoint.go |
| defaults/observe.go | ~150 | 3% | 参考 harness/observe/observe.go |
| **总计 (agent-engine)** | **~4,100** | **-** | |
| crux-turn | ~1,500 | - | 独立模块，不包含在 engine 中 |
| crux-plugin | ~900 | - | 独立模块，不包含在 engine 中 |

---

## 九、实现路线图

| 阶段 | 内容 | 工作量 | 产出 |
|------|------|:------:|------|
| **Phase 1** | 创建 agent-engine 模块 (engine 核心) | 1.5 天 | 可编译的 engine 核心 |
| 1a | 复制 agent/ 三文件至 engine/ | 0.5 天 | 代码骨架 |
| 1b | 解耦 CompactionConfig（函数化） | 0.5 天 | 零内包引用 |
| 1c | 拆分 stream.go + tools.go | 0.5 天 | 模块化结构 |
| **Phase 2** | 定义插件接口 + 新增 Pipeline | 1 天 | 清晰的插件契约 |
| 2a | 创建 plugin/types.go (所有接口) | 0.3 天 | 插件接口定义 |
| 2b | 创建 engine/pipeline.go | 0.5 天 | Pipeline/Stage/RunState |
| 2c | 创建 go.mod（仅依赖 crux-ai） | 0.2 天 | 独立模块 |
| **Phase 3** | TUI 集成 | 1 天 | 可运行的 TUI |
| 3a | 替换 internal/agent/ → engine/ | 0.3 天 | 核心替换 |
| 3b | 创建 internal/plugins/ | 0.5 天 | 插件适配层 |
| 3c | 修改 main.go 组装 | 0.2 天 | 集成完成 |
| **Phase 4** | 默认包 + 测试 | 1.5 天 | 完善的代码库 |
| 4a | defaults/ 包（session/context/memory/autolearn） | 0.5 天 | 开箱即用 |
| 4b | defaults/ 包（approval/checkpoint/observe） | 0.5 天 | 额外插件 |
| 4c | engine 单元测试 | 0.5 天 | 80%+ 覆盖率 |
| **Phase 5** | cross-module 集成测试 | 1 天 | 端到端验证 |
| 5a | engine + crux-turn 联调 | 0.3 天 | Turn FSM 集成 |
| 5b | engine + crux-plugin 联调 | 0.3 天 | 插件系统集成 |
| 5c | engine + defaults 全流程测试 | 0.4 天 | 完整链路过关 |
| **总计** | | **6 天** | |

---

## 十、关键决策记录

| # | 决策 | 理由 |
|:--:|------|------|
| 1 | CompactionConfig 改为纯函数接口 | 消除 engine 对 context 包的编译时依赖 |
| 2 | Pipeline/Stage 是增量而非替代 | 保留 AgentLoop() 作为默认，Pipeline 作为高级选项 |
| 3 | 插件接口放在 plugin/ 包 | 与 engine 分离，插件可独立 mock |
| 4 | tools/ 不进 engine | 工具是业务层，每种 TUI 的工具不同 |
| 5 | Turn FSM 保持独立模块 | crux-turn 已有完整实现，通过接口集成而非复制 |
| 6 | crux-plugin 保持独立 | 子进程隔离模式与 engine 的进程内模型正交 |
| 7 | defaults/ 引用 harness 实现的思路 | harness/session, harness/context 等已成熟，不应重写 |
| 8 | 不保留 EventStream 泛型实现 | 可复用 crux-ai 的 core.EventStream |

---

## 十一、附录：与现有代码的差异

### 11.1 当前 TUI 的 internal/agent/ 代码

```
crux-agent-tui/internal/agent/
├── loop.go     → 已有 Agent 循环逻辑
├── types.go    → 已有 Agent 类型定义
```

### 11.2 替换方案

| TUI 现有文件 | 替换为 | 说明 |
|-------------|--------|------|
| internal/agent/loop.go | engine/loop.go + engine/stream.go + engine/tools.go | 更完整的事件循环 |
| internal/agent/types.go | engine/types.go | 更丰富的类型定义 |
| — | engine/agent.go | 新增状态管理 |
| — | engine/pipeline.go | 新增 Pipeline 编排 |
| — | plugin/types.go | 新增插件接口 |

### 11.3 runtime 现有代码的保留与迁移

| runtime 包 | 迁移方式 | 说明 |
|-----------|---------|------|
| agent/ (三文件) | 迁移到 engine/ | 核心引擎 |
| context/ | 摘接口到 plugin, 实现进 defaults | 上下文管理 |
| session/ | 摘接口到 plugin, 实现进 defaults | 会话管理 |
| memory/ | 摘接口到 plugin, 实现进 defaults | 长期记忆 |
| autolearn/ | 摘接口到 plugin, 实现进 defaults | 自动学习 |
| tools/ | **不迁移** | 业务层 |

### 11.4 harness 现有代码的定位

| harness 包 | 关系 | 说明 |
|-----------|------|------|
| session/ | defaults/ 参考其实现 | JSONL 存储、会话树 |
| context/ | defaults/ 参考其实现 | Pipeline、Compactor、TokenCounter |
| approval/ | defaults/ 参考其实现 | Gate、Rule、Decision |
| checkpoint/ | defaults/ 参考其实现 | 快照、Undo/Redo |
| observe/ | defaults/ 参考其实现 | 结构化日志、TurnTimer |
| token/ | defaults/context 使用 | Token 计数 |
| prompt/ | 不进入 engine | Prompt 模板 |

### 11.5 独立模块的关系

| 模块 | 定位 | 依赖 agent-engine? | 说明 |
|------|------|:------------------:|------|
| crux-turn | 9-state Turn FSM，可选上层状态机 | 否 | 通过 Adapter 解耦，可作为 engine 的上层或嵌入 Stage |
| crux-plugin | 子进程 JSON-RPC 2.0 插件系统 | 否 | 通过 plugin.ToolPlugin 接口适配，是 engine 的一种工具后端 |

### 11.6 crux-plugin 与 agent-engine/plugin 完整关系一览

| 上下文 | 指代 | 包路径 | 行数 | 本质 |
|--------|------|--------|:----:|------|
| 引擎插件接口 | engine 的能力扩展契约 | `agent-engine/plugin/types.go` | ~120 | 接口定义 |
| 外部插件系统 | 子进程 IPC 框架 | `github.com/hycjack/crux-plugin` | ~900 | 子进程管理器 |
| 插件工具适配层 | 连接两者的胶水代码 | 调用方实现 (如 `internal/plugins/`) | ~50 | 适配器 |

**关键原则：**
1. `agent-engine/plugin/` 不引用 `crux-plugin`（没有这个依赖）
2. `crux-plugin` 也不引用 `agent-engine/plugin/`（它是纯 stdlib，无外部依赖）
3. 调用方（如 TUI）负责在两者之间适配

---

*文档结束*
