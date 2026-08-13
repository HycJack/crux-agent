# crux-agent-tui → agent-engine 集成方案

> 将 TUI 项目的内置 Agent 循环替换为 agent-engine 库
> 日期: 2026-07-03 | 状态: 规划中

---

## 一、背景

### 1.1 当前状态

TUI 项目 (`crux-agent-tui`) 当前在 `internal/agent/` 中维护着自己的一套 Agent 运行时：

- `internal/agent/types.go` — 事件类型、AgentTool、AgentState、Agent
- `internal/agent/loop.go` — Agent 循环（LLM 调用 → 工具执行 → LLM 调用）

同时有自己的 LLM Provider 抽象层：
- `internal/provider/types.go` — Message、StreamEvent、LLMProvider 接口
- `internal/openai/openai.go` — OpenAI 兼容协议实现

`agent-engine` 已经作为独立库开发完成，提供了更完整的事件循环、插件接口、流式处理、上下文压缩等能力。

### 1.2 为什么要集成

| 原因 | 说明 |
|------|------|
| **消除重复逻辑** | TUI 的 agent 循环与 agent-engine 功能重叠 |
| **获得更完整的事件体系** | agent-engine 有 12 种事件类型 vs TUI 的 4 种 |
| **获得上下文压缩** | agent-engine 内置 pre-call compaction + overflow retry |
| **获得 Pipeline 抽象** | Stage-based 的循环编排 |
| **统一代码基** | 所有 Agent 运行时逻辑集中在 agent-engine |

### 1.3 集成原则

1. **最小修改**：不改动 TUI 的业务逻辑和 UI 渲染代码
2. **适配层模式**：使用 adapter/portal 层连接两种事件体系
3. **渐进式集成**：优先替换核心循环，插件接口逐步采用
4. **零侵入**：agent-engine 的修改必须提交到 agent-engine 仓库

---

## 二、当前架构 vs 目标架构

### 2.1 当前架构

```
crux-agent-tui/
├── internal/
│   ├── agent/                  # 自包含 Agent 运行时
│   │   ├── types.go            # Agent、AgentState、AgentEvent
│   │   └── loop.go             # runLoop、streamResponse、executeTool
│   ├── provider/               # LLM Provider 抽象
│   │   └── types.go            # LLMProvider 接口、StreamEvent、Message
│   ├── openai/                 # OpenAI 协议实现
│   │   └── openai.go           # HTTP SSE 流式请求
│   └── ui/
│       ├── app.go              # BubbleTea App，创建 Agent、运行
│       ├── tools.go            # Tool 定义（bash, read_file 等）
│       └── config.go           # 环境变量加载
└── cmd/agent-tui/main.go       # 入口
```

### 2.2 目标架构

```
crux-agent-tui/
├── internal/
│   ├── agent/                  → 保留但精简：转为 adapter 层
│   │   ├── adapter.go          (新增) 适配 agent-engine 事件 → TUI 事件
│   │   ├── stream_adapter.go   (新增) 适配 crux-ai StreamFn → LLMProvider
│   │   ├── types.go            精简：只保留 TUI 特有的事件类型
│   │   └── loop.go             删除（被 agent-engine 替代）
│   │
│   ├── provider/               保留：LLMProvider 接口仍用于工具调用
│   ├── openai/                 保留但不再用于 Agent 循环
│   └── ui/
│       ├── app.go              修改：使用 engine.New() 替代 agent.New()
│       ├── tools.go            修改：适配 engine.AgentTool
│       └── config.go           不变
│
├── docs/
│   └── migration-to-agent-engine.md  ← 本文档
│
└── go.mod                      修改：添加 agent-engine 依赖

agent-engine/  ← 独立模块，通过 replace directive 引用
├── engine/    → Agent, AgentLoop, streamAssistantResponse
├── plugin/    → 插件接口
└── defaults/  → 默认实现
```

### 2.3 依赖关系

```
TUI (crux-agent-tui)
    │
    ├── agent-engine/engine     # Agent 运行时
    │       │
    │       └── crux-ai/core    # Message, Model, EventStream 等核心类型
    │
    ├── internal/provider       # LLMProvider 接口（保留，用于工具执行回调）
    ├── internal/openai         # OpenAI HTTP 实现
    │
    └── bubbletea               # TUI 框架
```

---

## 三、关键差异分析

### 3.1 事件体系对比

| TUI 事件 (当前) | agent-engine 事件 | 映射方式 |
|-----------------|-------------------|---------|
| `EventMessageUpdate{Type, Delta}` | `EventMessageUpdate{AssistantEvent}` | 提取 `Delta` 字段 |
| `EventToolExecStart{ToolName, Args, ToolID}` | `EventToolExecStart{ToolCallID, ToolName, Args}` | 直接映射 |
| `EventToolExecEnd{ToolName, Result, IsError, ToolID}` | `EventToolExecEnd{ToolCallID, ToolName, Result, IsError}` | 直接映射 |
| `EventTurnEnd{ErrorMessage}` | `EventTurnEnd{Message, ToolResults}` | 提取 ErrorMessage |
| — | `EventAgentStart` | 忽略 |
| — | `EventAgentEnd` | 忽略 |
| — | `EventTurnStart` | 忽略 |
| — | `EventMessageStart` | 忽略 |
| — | `EventMessageEnd` | 处理最终消息 |
| — | `EventToolExecUpdate` | 可选的流式更新 |

### 3.2 Agent 初始化对比

| 字段 | TUI (当前) | agent-engine | 注意 |
|------|-----------|-------------|------|
| `Model` | `string` | `core.Model` | 需要创建 `core.Model{ID: ...}` |
| `BaseURL` | `string` | `SimpleStreamOptions.BaseURL` | 迁移到 options |
| `APIKey` | `string` | `SimpleStreamOptions.APIKey` | 迁移到 options |
| `SystemPrompt` | `string` | `AgentState.SystemPrompt` | 不变 |
| `Messages` | `[]provider.Message` | `[]core.Message` | 需要类型转换 |
| `Tools` | `[]agent.AgentTool` | `[]engine.AgentTool` | 直接对应 |
| `MaxTokens` | `int` | `SimpleStreamOptions.MaxTokens` | 迁移到 options |
| `Headers` | `map[string]string` | `SimpleStreamOptions.Headers` | 迁移到 options |

### 3.3 AgentTool 对比

| 字段 | TUI (当前) | agent-engine | 注意 |
|------|-----------|-------------|------|
| `Name` | `string` | `string` | 不变 |
| `Description` | `string` | `string` | 不变 |
| `Parameters` | `json.RawMessage` | `json.RawMessage` | 不变 |
| `Execute` | `ToolExecuteFunc` | `ToolExecuteFunc` | 返回值不同 |
| `Label` | — | `string` | 新字段 |
| `ExecutionMode` | — | `ToolExecutionMode` | 新字段 |

### 3.4 StreamFn 适配

agent-engine 通过 `StreamFn` 接受自定义流式函数。TUI 当前的 `openai.Provider.Stream()` 方法需要包装为 `StreamFn` 签名：

```go
// StreamFn 签名 (agent-engine):
type StreamFn func(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (
    *core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error)
```

适配策略：创建一个 `StreamFn` 实现，将 `core.Context` 转换为 `provider.LLMContext`，调用 `openai.Provider.Stream()`，然后将 `provider.EventStream` 的事件转换为 `core.AssistantMessageEvent`。

---

## 四、集成步骤

### Phase 1: 基础设施准备

| # | 任务 | 文件 | 说明 |
|---|------|------|------|
| 1a | 添加 go.mod 依赖 | `go.mod` | 添加 `require` + `replace` agent-engine |
| 1b | 添加 adapter 层 | `internal/agent/adapter.go` | 类型转换函数 |
| 1c | 添加 StreamFn adapter | `internal/agent/stream_adapter.go` | 包装 openai provider |

### Phase 2: TUI 代码修改

| # | 任务 | 文件 | 说明 |
|---|------|------|------|
| 2a | 修改 `agent.go` → 精简 | `internal/agent/types.go` | 删除重复类型，保留 adapter |
| 2b | 删除 `loop.go` | `internal/agent/loop.go` | 被 agent-engine 替代 |
| 2c | 修改 `app.go` | `internal/ui/app.go` | 使用 `engine.New()` |
| 2d | 修改 `tools.go` | `internal/ui/tools.go` | 适配 `engine.AgentTool` |

### Phase 3: 编译验证与测试

| # | 任务 | 说明 |
|---|------|------|
| 3a | `go build ./...` | 确认编译通过 |
| 3b | 手动测试 | 确认基本功能正常运行 |

---

## 五、Adapter 层设计

### 5.1 类型转换

```go
// internal/agent/adapter.go

// toCoreModel converts a provider model string to core.Model.
func ToCoreModel(id string) core.Model {
    return core.Model{ID: id}
}

// toCoreMessage converts a provider.Message to core.Message.
func ToCoreMessage(msg provider.Message) core.Message { ... }

// toCoreMessages converts a slice.
func ToCoreMessages(msgs []provider.Message) []core.Message { ... }

// FromCoreMessage converts a core.Message back to provider.Message.
func FromCoreMessage(msg core.Message) provider.Message { ... }

// FromCoreMessages converts a slice.
func FromCoreMessages(msgs []core.Message) []provider.Message { ... }
```

### 5.2 Event Adapter

```go
// internal/agent/adapter.go

// AdaptAgentEvent converts an engine.AgentEvent to events the TUI understands.
// Returns the adapted TUI event (or nil to skip).
func AdaptAgentEvent(evt engine.AgentEvent) any {
    switch e := evt.(type) {
    case engine.EventMessageUpdate:
        // Extract text delta from AssistantEvent
        switch ae := e.AssistantEvent.(type) {
        case core.EventTextDelta:
            return EventMessageUpdate{Type: "text", Delta: ae.Delta}
        case core.EventThinkingDelta:
            return EventMessageUpdate{Type: "thinking", Delta: ae.Delta}
        }
    case engine.EventToolExecStart:
        return EventToolExecStart{
            ToolName: e.ToolName,
            Args:     truncate(string(e.Args), 300),
            ToolID:   e.ToolCallID,
        }
    case engine.EventToolExecEnd:
        return EventToolExecEnd{
            ToolName: e.ToolName,
            Result:   formatToolResult(e),
            IsError:  e.IsError,
            ToolID:   e.ToolCallID,
        }
    case engine.EventTurnEnd:
        if e.Message.ErrorMessage != "" || (len(e.ToolResults) == 0)
            return EventTurnEnd{ErrorMessage: e.Message.ErrorMessage}
        }
    }
    return nil // skip event
}
```

### 5.3 StreamFn Adapter

```go
// internal/agent/stream_adapter.go

// NewStreamFn creates a StreamFn that wraps a provider.LLMProvider.
func NewStreamFn(prov provider.LLMProvider) engine.StreamFn {
    return func(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (
        *core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
        // 1. Convert core.Context → provider.LLMContext
        // 2. Call prov.Stream(ctx, llmCtx, streamOpts)
        // 3. Convert provider.EventStream → core.EventStream
    }
}
```

---

## 六、修改文件清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `go.mod` | **修改** | 添加 `agent-engine` 依赖 |
| `internal/agent/types.go` | **修改** | 保留 adapter 事件；删除 Agent、AgentState、AgentTool 等（由 engine 提供） |
| `internal/agent/loop.go` | **删除** | 被 agent-engine 替代 |
| `internal/agent/adapter.go` | **新建** | 类型转换 + 事件适配 |
| `internal/agent/stream_adapter.go` | **新建** | StreamFn 适配 |
| `internal/ui/app.go` | **修改** | `newAgent()` → 使用 `engine.New()`；`runAgent()` 修改调用 |
| `internal/ui/tools.go` | **修改** | 适配 `engine.AgentTool`（返回值类型） |

---

## 七、回滚方案

如果集成后发现重大问题，可以：

1. **保留 `internal/agent/` 的旧代码**不变（只是不编译）
2. 恢复 `go.mod` 的依赖和 replace 配置
3. 恢复 `app.go` 和 `tools.go` 的旧逻辑

旧代码完全可以并存，切换成本低。
