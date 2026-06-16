# crux-agent-runtime 项目架构

## 项目概述

crux-agent-runtime 是一个 Go 语言的 Agent 运行时框架，参考了 [pi-ai-go](https://github.com/) 和 [crux-agent-runtime](file:///mnt/workspace/crux/crux/crux-agent-runtime) 的设计，提供完整的自主对话循环能力。

## 设计原则

1. **事件驱动** — 双层循环架构（outer loop + inner loop）
2. **可组合** — 通过钩子和接口扩展，而非继承
3. **持久化优先** — 会话和记忆默认持久化
4. **异步友好** — 事件流支持非阻塞消费

---

## 包结构详解

### 1. `agent/` — Agent 核心循环

**职责**：实现自主对话循环，处理工具调用和流式响应。

```
agent/
├── agent.go           # Agent 状态管理
├── agent-loop.go      # 双层事件循环实现
└── types.go           # 类型定义（AgentTool, AgentEvent 等）
```

#### 核心类型

```go
// AgentTool 定义一个可调用的工具
type AgentTool struct {
    Name          string
    Description   string
    Parameters    json.RawMessage // JSON Schema
    Execute       ToolExecuteFunc
    ExecutionMode ToolExecutionMode // "parallel" 或 "sequential"
}

// AgentLoopConfig 配置 Agent 循环
type AgentLoopConfig struct {
    Model         core.Model
    SystemPrompt  string
    Tools         []AgentTool
    StreamFn      StreamFn

    // 钩子函数
    ConvertToLlm          func([]core.Message) []core.Message
    TransformContext      func([]core.Message) []core.Message
    GetApiKey             func() string
    ShouldStopAfterTurn   func(core.AssistantMessage, []core.ToolResultMessage) bool
    PrepareNextTurn       func(*AgentLoopConfig, core.AssistantMessage, []core.ToolResultMessage, []core.Message)
    GetSteeringMessages   func() []core.Message
    GetFollowUpMessages   func() []core.Message
    BeforeToolCall        func(BeforeToolCallContext) *ToolCallBlock
    AfterToolCall         func(AfterToolCallContext) *ToolCallOverride
}
```

#### 双层循环

```
┌─────────────────────────────────────────┐
│ Outer Loop                              │
│  ├─ 检查 follow-up 消息                  │
│  └─ 如果有 follow-up，继续 inner loop    │
├─────────────────────────────────────────┤
│ Inner Loop                              │
│  ├─ 注入 steering 消息                   │
│  ├─ 流式调用 LLM                         │
│  ├─ 提取 tool calls                      │
│  ├─ 执行工具（并行/串行）                  │
│  ├─ 检查终止条件                          │
│  └─ 继续直到无 tool calls                │
└─────────────────────────────────────────┘
```

#### 事件类型

```go
type AgentEvent interface { agentEventTag() }

type EventAgentStart struct{}
type EventAgentEnd struct { Messages []core.Message }
type EventTurnStart struct{}
type EventTurnEnd struct { Message core.AssistantMessage; ToolResults []core.ToolResultMessage }
type EventMessageStart struct { Message core.AssistantMessage }
type EventMessageUpdate struct { Message core.AssistantMessage; AssistantEvent core.AssistantMessageEvent }
type EventMessageEnd struct { Message core.AssistantMessage }
type EventToolExecStart struct { ToolCallID, ToolName string; Args json.RawMessage }
type EventToolExecUpdate struct { ToolCallID, ToolName string; Args, PartialResult json.RawMessage }
type EventToolExecEnd struct { ToolCallID, ToolName string; Result json.RawMessage; IsError bool }
```

---

### 2. `session/` — 会话持久化

**职责**：管理对话历史的持久化存储和重建。

```
session/
├── types.go       # 会话类型定义
├── storage.go     # JSONL + Memory 存储
├── sqlite.go      # SQLite 存储
└── session.go     # Session + AgentSession
```

#### 核心类型

```go
// SessionTreeEntry 会话树节点
type SessionTreeEntry struct {
    Type      EntryType        // user_message, assistant_message, tool_result...
    Timestamp time.Time
    MessageData json.RawMessage // 消息内容（延迟解析）
    SessionID string           // 会话 ID
    Provider  string           // 模型提供商
    ModelID   string           // 模型 ID
    // ...
}

// SessionStorage 持久化接口
type SessionStorage interface {
    ReadAll() ([]SessionTreeEntry, error)
    Append(entries []SessionTreeEntry) error
    Close() error
}
```

#### 存储后端

| 后端 | 说明 | 使用场景 |
|------|------|----------|
| **MemoryStorage** | 内存存储 | 测试 |
| **JSONLStorage** | JSON Lines 文件 | 开发环境 |
| **SQLiteStorage** | SQLite 数据库 | 生产环境 |

#### 会话重建

```go
sess, _ := NewSession(storage)
sess.Append(
    NewSystemPromptEntry("系统提示"),
    NewUserMessageEntry("用户消息"),
    NewAssistantMessageEntry(assistantMsg),
)

// 重建 LLM 上下文
ctx := sess.BuildContext()
// ctx.SystemPrompt, ctx.Messages, ctx.Model
```

---

### 3. `context/` — 上下文窗口管理

**职责**：Token 计数、上下文压缩、窗口管理。

```
context/
├── token.go       # Token 计数器
├── compaction.go  # 压缩策略
└── manager.go     # 上下文管理器
```

#### Token 计数

```go
// 默认计数器（~4 chars/token）
func DefaultTokenCounter(systemPrompt string, messages []core.Message, tools []core.Tool) int
```

#### 压缩策略

| 策略 | 说明 | 优点 | 缺点 |
|------|------|------|------|
| **SlideWindow** | 保留最后 N 条消息 | 快速、无 LLM 调用 | 丢失旧信息 |
| **LLMSummarize** | LLM 生成摘要 | 保留信息密度 | 需要 LLM 调用 |
| **ChainedCompactor** | 链式组合 | 灵活 | 复杂 |
| **ContextWindowCompactor** | Token 感知 | 自动 | 依赖计数器精度 |

```go
// 滑动窗口
compactor := NewSlideWindow(50)

// LLM 摘要
compactor := NewLLMSummarize()
compactor.KeepLast = 10
compactor.Summarize = func(ctx context.Context, dropped []core.Message) (string, error) {
    return llm.CompleteSimple(ctx, model, dropped)
}

// 链式
compactor := &ChainedCompactor{
    Compactors: []Compactor{NewLLMSummarize(), NewSlideWindow(50)},
}
```

#### 上下文管理器

```go
config := DefaultContextWindowConfig()
config.MaxTokens = 128000

mgr := NewManager(config)
mgr.SetCompactor(NewSlideWindow(50))
mgr.LoadFromSession(session)

// 添加消息（自动压缩）
mgr.AddMessage(msg)

// 监控
stats := mgr.GetStats()
// stats.TotalTokens, stats.UsagePercent, stats.Compactions
```

---

### 4. `memory/` — 长期记忆

**职责**：跨会话的 KV 存储，持久化用户偏好和关键事实。

```
memory/
└── memory.go
```

#### 核心功能

```go
mem, _ := memory.New("./memory.json")

// 基本操作
mem.Set("user.name", "小明")
mem.SetWithCategory("task.current", "开发", "task")
val, ok := mem.Get("user.name")
mem.Delete("old_key")
mem.Has("key")

// 查询
keys := mem.Keys()
items := mem.ListByCategory("user")
size := mem.Size()

// 持久化
mem.Save()
mem.Load()

// Prompt 注入
prompt := mem.FormatForPrompt()
// # Long-term Memory
// - user.name: 小明
// - task.current: 开发

// 变化检测
hash := mem.Hash()
```

#### 存储格式（JSON）

```json
{
  "user.name": {
    "value": "小明",
    "createdAt": "2024-01-01T00:00:00Z",
    "updatedAt": "2024-01-01T00:00:00Z",
    "category": "user"
  }
}
```

---

### 5. `autolearn/` — 自动学习

**职责**：从对话中自动提取可记忆的事实。

```
autolearn/
└── autolearn.go
```

#### 触发源

| 触发源 | 示例 | 说明 |
|--------|------|------|
| **显式标记** | `[remember:key=value]` | 用户主动标记 |
| **工具结果** | `REMEMBER:key=value` | 工具输出标记 |
| **自然语言** | "我叫张三" | 正则提取 |
| **LLM 提取** | 异步分析对话 | 每 N 轮触发 |

#### 自然语言提取

```go
// 支持的模式
"你叫小七"          → assistant.name=小七
"我叫张三"          → user.name=张三
"我来自杭州"        → user.location=杭州
"请用中文回答"      → user.preferred_language=中文
```

#### LLM 提取

```go
extractor := &LLMSimpleExtractor{
    SummarizeFunc: func(ctx context.Context, prompt string) (string, error) {
        return llm.CompleteSimple(ctx, model, []core.Message{
            {Role: core.MessageRoleUser, Content: prompt},
        })
    },
}

// Key 白名单
var allowedKeyPrefixes = []string{
    "user.", "assistant.", "task.", "project.",
    "fact.", "decision.", "constraint.",
    "relation.", "family.", "pet.",
    "health.", "diet.", "date.", "asset.",
    "style.", "tool.", "goal.", "pain.",
}
```

---

## 数据流

```
用户输入
  │
  ├─→ autolearn.ProcessUserInput() → memory.Set()
  │
  └─→ agent.AgentLoop()
        │
        ├─→ context.Manager.AddMessage()
        │     └─→ Compactor.Compact() (if needed)
        │
        ├─→ llm.StreamSimpleWithContext()
        │     └─→ EventMessageUpdate (streaming)
        │
        ├─→ agent.executeToolCalls()
        │     ├─→ EventToolExecStart
        │     ├─→ tool.Execute()
        │     └─→ EventToolExecEnd
        │
        ├─→ autolearn.ProcessToolResult()
        │     └─→ memory.Set()
        │
        └─→ EventTurnEnd
              └─→ session.Append()
```

---

## 测试策略

### 单元测试

- **memory**: KV 操作、持久化、原子写入
- **autolearn**: 正则提取、LLM 提取、触发源
- **session**: 存储后端、会话重建、事件订阅
- **context**: Token 计数、压缩策略、管理器

### 集成测试

- Agent 循环 + 工具执行
- Session + Context + Memory 联动
- 自动学习 + 持久化

---

## 扩展点

### 1. 自定义存储后端

```go
type CustomStorage struct{}

func (s *CustomStorage) ReadAll() ([]session.SessionTreeEntry, error) { ... }
func (s *CustomStorage) Append(entries []session.SessionTreeEntry) error { ... }
func (s *CustomStorage) Close() error { ... }
```

### 2. 自定义压缩策略

```go
type CustomCompactor struct{}

func (c *CustomCompactor) Name() string { return "custom" }
func (c *CustomCompactor) Compact(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
    // 自定义压缩逻辑
}
```

### 3. 自定义记忆提取

```go
type CustomExtractor struct{}

func (e *CustomExtractor) Extract(ctx context.Context, messages []core.Message) ([]autolearn.Trigger, error) {
    // 自定义提取逻辑
}
```

### 4. 自定义 Token 计数器

```go
func CustomTokenCounter(systemPrompt string, messages []core.Message, tools []core.Tool) int {
    // 使用 tiktoken 等库精确计数
}
```

---

## 参考项目

- [pi-ai-go](https://github.com/) — 原始实现，提供了 Agent 循环、Session、Context 管理
- [crux-agent-runtime](file:///mnt/workspace/crux/crux/crux-agent-runtime) — 参考的 Session 和记忆设计
- [crux-ai](https://github.com/HycJack/crux-ai) — LLM 客户端库

---

## 版本历史

### v0.1.0 (当前)
- Agent 双层循环
- Session 持久化（JSONL + SQLite）
- Context 管理（Token 计数 + 压缩策略）
- Memory 长期记忆
- AutoLearn 自动学习
