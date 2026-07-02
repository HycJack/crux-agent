# agent-engine 核心抽象方案

> 从 crux-agent-runtime 中提取核心引擎，作为独立、可嵌入的 Agent 运行时库
> 版本: v0.1.0 | 日期: 2026-07-02

---

## 一、设计目标

1. **轻量核心** — 只包含 Agent 循环引擎，无外部依赖（除 crux-ai）
2. **插件化** — Session/Context/Memory/AutoLearn 等能力通过接口注入
3. **零内包引用** — engine 核心不引用任何内部包
4. **可直接嵌入 TUI** — crux-agent-tui 可直接替换 internal/agent/
5. **可独立演进** — engine 和 plugins 各自独立版本

---

## 二、架构全景

```
┌─────────────────────────────────────────────────────────────────┐
│                      agent-engine                               │
│                                                                │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │  engine/  (核心引擎)                                    │   │
│  │  ├── types.go     → AgentEvent, AgentTool, 配置类型     │   │
│  │  ├── agent.go     → Agent 状态管理 (Run/Abort/Steer)    │   │
│  │  ├── loop.go      → AgentLoop 双层事件循环              │   │
│  │  ├── stream.go    → streamAssistantResponse + 流式处理   │   │
│  │  └── tools.go     → executeToolCalls (并行/串行)        │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │  plugin/  (插件接口)                                    │   │
│  │  └── types.go     → 所有插件接口定义                     │   │
│  │       ├── SessionPlugin    (会话持久化)                  │   │
│  │       ├── ContextPlugin    (上下文管理)                  │   │
│  │       ├── MemoryPlugin     (长期记忆)                    │   │
│  │       ├── AutoLearnPlugin  (自动学习)                    │   │
│  │       └── ToolPlugin       (工具插件)                    │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                │
└─────────────────────────────────────────────────────────────────┘
```

---

## 三、核心提取范围

### 3.1 从 `crux-agent-runtime/agent/` 提取（全部进 engine）

| 文件 | 行数 | 说明 | 变更 |
|------|------|------|------|
| types.go | ~285 | 全部类型定义 | **需解耦** CompactionConfig |
| agent.go | ~340 | Agent 状态管理 | **保留** 100% |
| agent-loop.go | ~680 | 双层事件循环 | **保留** 100%，拆分子文件 |

**合计: ~1,300 行代码进入 engine 核心**

### 3.2 从各插件包提取接口（进 plugin/types.go）

| 源包 | 提取的接口 | 说明 |
|------|-----------|------|
| session/types.go | SessionStorage、SessionTreeEntry | 只保留接口定义 |
| context/token.go | TokenCounter 类型 | 函数签名 |
| context/compaction.go | Compactor 接口 | 压缩策略接口 |
| memory/memory.go | Memory 的 Get/Set/FormatForPrompt | 只保留接口 |
| autolearn/autolearn.go | Extractor、Trigger | 提取器接口 |

**合计: ~100 行接口定义**

---

## 四、文件迁移计划

### 4.1 迁移后目录结构

```
agent-engine/
├── engine/                      # 核心引擎
│   ├── types.go                # 类型定义（迁移 + 解耦）
│   ├── agent.go                # Agent 状态管理（迁移）
│   ├── loop.go                 # AgentLoop / runLoop / runInnerLoop（迁移）
│   ├── stream.go               # streamAssistantResponse / consumeStreamEvents（从 loop.go 拆分）
│   └── tools.go                # executeToolCalls / 工具执行逻辑（从 loop.go 拆分）
│
├── plugin/                      # 插件接口
│   └── types.go                # 所有插件接口定义
│
├── (optional) defaults/         # 默认插件实现（可选包）
│   ├── session.go              # SessionPlugin 默认实现
│   ├── context.go              # ContextPlugin 默认实现
│   ├── memory.go               # MemoryPlugin 默认实现
│   └── autolearn.go            # AutoLearnPlugin 默认实现
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

#### agent-loop.go 的 import 变更

**迁移前：**
```go
import (
    contextpkg "crux-agent-runtime/context"
    "github.com/hycjack/crux-ai/ai"
    core "github.com/hycjack/crux-ai/core"
)
```

**迁移后：**
```go
import (
    core "github.com/hycjack/crux-ai/core"
    // ai.StreamSimpleWithContext → 通过 config.StreamFn 注入
    // contextpkg.* → 通过 config.Compaction 函数接口注入
)
```

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

type SessionPlugin interface {
    Append(entry any) error
    BuildContext() any
    Entries() []any
    Close() error
}

// ─── ContextPlugin 上下文管理 ───

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
```

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
├── (new) internal/plugins/   # 按需使用的插件
│   ├── session.go            # session 插件实现
│   ├── context.go            # context 插件实现
│   ├── memory.go             # memory 插件实现
│   └── autolearn.go          # autolearn 插件实现
│
└── cmd/agent-tui/main.go     # 组装 engine + plugins
```

### 6.2 main.go 组装示例

```go
package main

import (
    "agent-engine/engine"
    "crux-agent-tui/internal/plugins"
    "crux-agent-tui/internal/ui"
)

func main() {
    // 1. 创建插件
    sessPlugin := plugins.NewSessionPlugin("./sessions")
    ctxPlugin := plugins.NewContextPlugin(128000)
    memPlugin := plugins.NewMemoryPlugin("./memory.json")

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
            sessPlugin.Append(e.Message)
        case engine.EventMessageUpdate:
            ui.UpdateChat(e.Message)
        case engine.EventToolExecEnd:
            ui.ShowToolResult(e.ToolName, e.Result)
        }
    })

    // 4. 运行
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
├── defaults/session.go ──→ crux-ai + modernc.org/sqlite
├── defaults/context.go ──→ crux-ai
├── defaults/memory.go ──→ (纯 JSON)
└── defaults/autolearn.go ──→ memory + crux-ai
```

**关键变化**：
1. engine/ 不再引用任何内包
2. 所有插件通过接口解耦
3. 默认实现可作为独立可选包

---

## 八、代码量统计

| 组件 | 行数 | 比例 | 说明 |
|------|------|:----:|------|
| **engine/** | **~1,300** | **40%** | 核心引擎（必须保留） |
| **plugin/types.go** | **~100** | **3%** | 插件接口（新建） |
| session/ 实现 | ~600 | 18% | 不进核心，做 plugin |
| context/ 实现 | ~450 | 14% | 不进核心，做 plugin |
| memory/ 实现 | ~260 | 8% | 不进核心，做 plugin |
| autolearn/ 实现 | ~790 | 24% | 不进核心，做 plugin |
| tools/ | ~200 | 6% | 完全独立 |
| **总计** | **~3,700** | **100%** | |

---

## 九、实现路线图

| 阶段 | 内容 | 工作量 | 产出 |
|------|------|:------:|------|
| **Phase 1** | 创建 agent-engine 模块 | 1 天 | 可编译的 engine 核心 |
| 1a | 复制 agent/ 三文件至 engine/ | 0.5 天 | 代码骨架 |
| 1b | 解耦 CompactionConfig（函数化） | 0.5 天 | 零内包引用 |
| 1c | 拆分 stream.go + tools.go | 0.5 天 | 模块化结构 |
| **Phase 2** | 定义插件接口 | 0.5 天 | 清晰的插件契约 |
| 2a | 创建 plugin/types.go | 0.3 天 | 所有接口定义 |
| 2b | 创建 go.mod（仅依赖 crux-ai） | 0.2 天 | 独立模块 |
| **Phase 3** | TUI 集成 | 1 天 | 可运行的 TUI |
| 3a | 替换 internal/agent/ → engine/ | 0.3 天 | 核心替换 |
| 3b | 创建 internal/plugins/ | 0.5 天 | 插件适配层 |
| 3c | 修改 main.go 组装 | 0.2 天 | 集成完成 |
| **Phase 4** | 默认包 + 测试 | 1 天 | 完善的代码库 |
| 4a | defaults/ 包（可选） | 0.5 天 | 开箱即用 |
| 4b | engine 单元测试 | 0.5 天 | 80%+ 覆盖率 |
| **总计** | | **3.5 天** | |

---

## 十、关键决策记录

| # | 决策 | 理由 |
|:--:|------|------|
| 1 | CompactionConfig 改为纯函数接口 | 消除 engine 对 context 包的编译时依赖 |
| 2 | 不保留 EventStream 泛型实现 | 可复用 crux-ai 的 core.EventStream |
| 3 | 插件接口放在 plugin/ 包 | 与 engine 分离，插件可独立 mock |
| 4 | tools/ 不进 engine | 工具是业务层，每种 TUI 的工具不同 |
| 5 | 不保留 AgentState 的 CompactionConfig 字段引用 | 避免接口污染，统一通过函数签名 |

---

## 十一、附录：与 crux-agent-tui 现有代码的差异

### 当前 TUI 的 internal/agent/ 代码

```
crux-agent-tui/internal/agent/
├── loop.go     → 已有 Agent 循环逻辑
├── types.go    → 已有 Agent 类型定义
```

### 替换方案

| TUI 现有文件 | 替换为 | 说明 |
|-------------|--------|------|
| internal/agent/loop.go | engine/loop.go + engine/stream.go + engine/tools.go | 更完整的事件循环 |
| internal/agent/types.go | engine/types.go | 更丰富的类型定义 |
| — | engine/agent.go | 新增状态管理 |
| — | plugin/types.go | 新增插件接口 |

---

*文档结束*
