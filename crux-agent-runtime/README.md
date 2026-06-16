# crux-agent-runtime

[![Go Reference](https://pkg.go.dev/badge/github.com/hycjack/crux-agent-runtime.svg)](https://pkg.go.dev/github.com/hycjack/crux-agent-runtime)

Go 语言的 Agent 运行时框架，提供完整的自主对话循环、会话管理、上下文压缩、持久化记忆和自动学习能力。

## ✨ 特性

- 🔄 **Agent 循环** — 双层事件驱动架构（outer loop + inner loop）
- 🛠️ **工具执行** — 并行/串行工具调用，支持中断和钩子
- 💬 **会话管理** — JSONL/SQLite 持久化，事件订阅
- 📦 **上下文管理** — Token 计数、滑动窗口、LLM 摘要压缩
- 🧠 **长期记忆** — KV 存储，跨会话持久化
- 📚 **自动学习** — 多源触发记忆提取（显式标记/自然语言/LLM）
- ⚡ **流式响应** — 实时事件流，支持中途注入消息

## 📦 包结构

```
crux-agent-runtime/
├── agent/           # Agent 核心循环
│   ├── agent.go           # Agent 状态管理
│   ├── agent-loop.go      # 双层事件循环
│   └── types.go           # 类型定义和工具接口
├── session/         # 会话持久化
│   ├── session.go         # Session + AgentSession
│   ├── storage.go         # JSONL + Memory 存储
│   ├── sqlite.go          # SQLite 存储
│   └── types.go           # 会话类型定义
├── context/         # 上下文窗口管理
│   ├── token.go           # Token 计数器
│   ├── compaction.go      # 压缩策略（滑动窗口/LLM摘要/链式）
│   └── manager.go         # 上下文管理器
├── memory/          # 长期记忆
│   └── memory.go          # KV 存储 + JSON 持久化
├── autolearn/       # 自动学习
│   └── autolearn.go       # 多源记忆提取
└── cmd/             # 命令行示例
    └── demo.go            # 演示程序
```

### 依赖关系

```
agent ──→ session ──→ core (crux-ai)
  │          │
  │          └──→ memory
  │
  └──→ context ──→ session
       │
       └──→ memory
            │
            └──→ autolearn ──→ memory
```

---

## 🚀 快速开始

### 安装

```bash
go get github.com/hycjack/crux-agent-runtime
```

### 基本 Agent 循环

```go
package main

import (
    "context"
    "fmt"

    "github.com/hycjack/crux-ai/core"
    "github.com/hycjack/crux-agent-runtime/agent"
)

func main() {
    // 定义工具
    tools := []agent.AgentTool{
        {
            Name:        "get_weather",
            Description: "获取天气信息",
            Parameters:  json.RawMessage(`{...}`),
            Execute: func(ctx context.Context, id string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
                return agent.AgentToolResult{
                    Content: []core.ContentBlock{
                        core.TextContent{Type: "text", Text: "晴天 25°C"},
                    },
                }, nil
            },
        },
    }

    // 配置 Agent
    config := agent.AgentLoopConfig{
        Model: core.Model{
            ID:       "gpt-4o",
            Provider: core.ProviderOpenAI,
            API:      core.APIOpenAICompletions,
        },
        SystemPrompt: "你是一个有帮助的助手。",
        Tools:        tools,
        StreamFn: func(ctx context.Context, m core.Model, c core.Context, o core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
            return llm.StreamSimpleWithContext(ctx, m, c, o)
        },
    }

    // 运行 Agent
    messages := []core.Message{
        core.UserMessage{Role: core.MessageRoleUser, Content: "北京天气怎么样？"},
    }

    stream := agent.AgentLoop(context.Background(), messages, config)
    result, _ := stream.Result()
    fmt.Printf("回复: %v\n", result)
}
```

---

## 💬 会话管理

### 基本用法

```go
import "github.com/hycjack/crux-agent-runtime/session"

// 创建持久化会话
storage, _ := session.NewSQLiteStorage("./sessions/user-123.db")
sess, _ := session.NewSession(storage)

// 添加会话历史
sess.Append(
    session.NewSystemPromptEntry("你是一个有帮助的助手。"),
    session.NewModelChangeEntry("openai", "gpt-4o"),
    session.NewUserMessageEntry("你好！"),
    session.NewAssistantMessageEntry(assistantMsg),
)

// 重建 LLM 上下文
ctx := sess.BuildContext()
// ctx.SystemPrompt = "你是一个有帮助的助手。"
// ctx.Messages = [user message, assistant message]
// ctx.Model = {Provider: "openai", ModelID: "gpt-4o"}

// 关闭会话
sess.Close()
```

### 存储后端

```go
// 1. 内存存储（测试用）
storage := session.NewMemoryStorage()

// 2. JSONL 文件存储
storage, _ := session.NewJSONLStorage("./session.jsonl")

// 3. SQLite 存储（推荐生产环境）
storage, _ := session.NewSQLiteStorage("./session.db")
```

### AgentSession（高级）

```go
agentSess, _ := session.NewAgentSession(storage)
defer agentSess.Close()

// 订阅事件
ch := agentSess.Subscribe(32)
defer agentSess.Unsubscribe(ch)

go func() {
    for event := range ch {
        switch e := event.(type) {
        case session.EventMessageUpdate:
            fmt.Print(e.Message)
        case session.EventRunEnd:
            fmt.Println("完成!")
        }
    }
}()

// 提交消息
agentSess.Session().Append(session.NewUserMessageEntry("你好"))
```

---

## 📦 上下文管理

### Token 计数

```go
import "github.com/hycjack/crux-agent-runtime/context"

// 使用默认计数器（~4 chars/token）
tokens := context.DefaultTokenCounter(systemPrompt, messages, tools)

// 检查是否需要压缩
config := context.DefaultContextWindowConfig()
config.MaxTokens = 128000
config.ReserveTokens = 4096

if context.NeedsCompaction(nil, systemPrompt, messages, tools, config) {
    // 需要压缩
}
```

### 压缩策略

```go
// 1. 滑动窗口（快速，无 LLM 调用）
compactor := context.NewSlideWindow(50)

// 2. LLM 摘要（保留信息密度）
compactor := context.NewLLMSummarize()
compactor.KeepLast = 10
compactor.MinTrigger = 30
compactor.Summarize = func(ctx context.Context, dropped []core.Message) (string, error) {
    return llm.CompleteSimple(ctx, model, dropped)
}

// 3. 链式压缩（按顺序尝试）
compactor := &context.ChainedCompactor{
    Compactors: []context.Compactor{
        context.NewLLMSummarize(),
        context.NewSlideWindow(50),
    },
}
```

### 上下文管理器

```go
config := context.DefaultContextWindowConfig()
config.MaxTokens = 128000

mgr := context.NewManager(config)
mgr.SetCompactor(context.NewSlideWindow(50))
mgr.LoadFromSession(session)

// 添加消息（自动触发压缩）
mgr.AddMessage(userMsg)

// 获取当前上下文
llmCtx := mgr.GetContext()

// 监控统计
stats := mgr.GetStats()
fmt.Printf("Tokens: %d/%d (%.1f%%)\n", 
    stats.TotalTokens, stats.AvailableTokens, stats.UsagePercent)

if mgr.IsNearLimit(0.8) {
    log.Println("Warning: context near limit!")
}
```

---

## 🧠 长期记忆

### 基本用法

```go
import "github.com/hycjack/crux-agent-runtime/memory"

// 创建记忆存储
mem, _ := memory.New("./agent-memory.json")

// 读写记忆
mem.Set("user.name", "小明")
mem.SetWithCategory("task.current", "开发项目", "task")

val, ok := mem.Get("user.name") // "小明", true
mem.Delete("old_key")

// 持久化
mem.Save()

// 格式化为 system prompt
prompt := mem.FormatForPrompt()
// # Long-term Memory
// - user.name: 小明
// - task.current: 开发项目
```

### 分类查询

```go
items := mem.ListByCategory("user")
for _, item := range items {
    fmt.Printf("%s: %s\n", item.Key, item.Entry.Value)
}

// 变化检测
hash := mem.Hash() // 用于判断是否需要重建 prompt
```

---

## 📚 自动学习

### 多源触发

```go
import "github.com/hycjack/crux-agent-runtime/autolearn"

mem, _ := memory.New("./memory.json")
learner := autolearn.New(mem, autolearn.DefaultSettings())

// 1. 处理用户输入（自动提取记忆）
count := learner.ProcessUserInput("我叫小明，来自杭州，请用中文回答")
// 自动保存:
//   user.name = 小明
//   user.location = 杭州
//   user.preferred_language = 中文

// 2. 处理工具结果
count = learner.ProcessToolResult("REMEMBER:task.status=完成")
// 自动保存: task.status = 完成
```

### 显式标记

```
请记住：user.name=小明
记住：user.name=小明
[remember:user.name=小明]
[memorize:user.name=小明]
```

### 自然语言提取

自动识别以下模式：
- ✅ "你叫小七" → `assistant.name=小七`
- ✅ "我叫张三" → `user.name=张三`
- ✅ "我来自杭州" → `user.location=杭州`
- ✅ "请用中文回答" → `user.preferred_language=中文`

### LLM 提取（可选）

```go
settings := autolearn.DefaultSettings()
settings.AutoLearn = true
settings.ExtractEveryN = 5 // 每 5 轮触发一次

extractor := &autolearn.LLMSimpleExtractor{
    SummarizeFunc: func(ctx context.Context, prompt string) (string, error) {
        return llm.CompleteSimple(ctx, model, []core.Message{
            {Role: core.MessageRoleUser, Content: prompt},
        })
    },
}

// 在对话循环中调用
learner.MaybeExtract(ctx, messages, extractor)
```

### Key 白名单

LLM 提取只接受以下前缀的 key：
- `user.*` — 用户信息
- `assistant.*` — AI 信息
- `task.*` / `project.*` — 任务/项目
- `fact.*` / `decision.*` — 事实/决策
- `preference.*` — 偏好设置

---

## 🧪 测试

```bash
# 运行所有测试
go test ./...

# 运行特定包
go test ./session/ -v
go test ./context/ -v
go test ./memory/ -v
go test ./autolearn/ -v

# 查看覆盖率
go test ./... -cover
```

### 测试统计

| 包 | 测试数 | 状态 |
|------|--------|------|
| agent | - | ⏳ 待添加 |
| session | 21 | ✅ 通过 |
| context | 15 | ✅ 通过 |
| memory | 13 | ✅ 通过 |
| autolearn | 20 | ✅ 通过 |

---

## 📚 文档

- [AGENTS.md](AGENTS.md) — 项目架构详细说明

## 🔗 依赖

- [github.com/hycjack/crux-ai](https://github.com/HycJack/crux-ai) — LLM 客户端库
- [github.com/mattn/go-sqlite3](https://github.com/mattn/go-sqlite3) — SQLite 驱动

## 📄 许可证

MIT License
