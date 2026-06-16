# 模块设计：Session 持久化

> 模块: crux-agent-runtime/session
> 版本: v0.1.0 | 更新: 2026-06-17
> 状态: ✅ 已完成

---

## 1. 职责

会话级对话历史的持久化存储，支持结构化条目和上下文重建。

**核心能力**：
- SessionStorage 接口（可替换后端）
- 三种存储后端（Memory、JSONL、SQLite）
- 结构化条目（消息、系统提示、模型切换、压缩摘要）
- 上下文重建（BuildContext）
- AgentSession 事件订阅

## 2. 架构

```
SessionStorage (接口)
  │
  ├── MemoryStorage    # 内存（测试）
  ├── JSONLStorage     # JSON Lines 文件
  └── SQLiteStorage    # SQLite 数据库
        │
        └── Session    # 会话管理器
              │
              └── AgentSession  # 事件订阅包装器
```

## 3. 核心类型

### SessionTreeEntry

```go
type SessionTreeEntry struct {
    Type      EntryType        // 条目类型
    Timestamp time.Time        // 创建时间
    MessageData json.RawMessage // 消息数据（延迟解析）
    SessionID string           // 会话 ID
    Provider  string           // 模型提供商
    ModelID   string           // 模型 ID
    ThinkingLevel string       // 思考级别
    CompactionSummary string   // 压缩摘要
    Metadata  map[string]any   // 自定义元数据
    Content   json.RawMessage  // 原始内容
}
```

### EntryType

```go
const (
    EntryUserMessage      EntryType = "user_message"
    EntryAssistantMessage EntryType = "assistant_message"
    EntryToolResult       EntryType = "tool_result"
    EntrySystemPrompt     EntryType = "system_prompt"
    EntryModelChange      EntryType = "model_change"
    EntryCompaction       EntryType = "compaction"
    EntrySessionInfo      EntryType = "session_info"
    EntryThinkingChange   EntryType = "thinking_change"
)
```

### SessionStorage

```go
type SessionStorage interface {
    ReadAll() ([]SessionTreeEntry, error)
    Append(entries []SessionTreeEntry) error
    Close() error
}
```

## 4. 存储后端对比

| 后端 | 并发安全 | 持久化 | 性能 | 使用场景 |
|------|----------|--------|------|----------|
| **MemoryStorage** | ✅ RWMutex | ❌ | 最快 | 单元测试 |
| **JSONLStorage** | ✅ Mutex | ✅ | 中等 | 开发环境 |
| **SQLiteStorage** | ✅ WAL | ✅ | 快 | 生产环境 |

### SQLite 特性

- WAL 日志模式（并发读写）
- 自动创建表和索引
- 事务支持

### SQLite Schema

```sql
CREATE TABLE session_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL,
    timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    message_data TEXT,
    session_id TEXT,
    provider TEXT,
    model_id TEXT,
    thinking_level TEXT,
    compaction_summary TEXT,
    metadata TEXT,
    content TEXT
);
CREATE INDEX idx_type ON session_entries(type);
CREATE INDEX idx_timestamp ON session_entries(timestamp);
```

## 5. JSON 反序列化

Message 是接口类型，无法直接 JSON 反序列化。解决方案：

```go
func (e *SessionTreeEntry) GetMessages() []core.Message {
    // 1. 探测 role 字段
    var probe struct { Role core.MessageRole }
    json.Unmarshal(e.MessageData, &probe)

    // 2. 根据 role 选择具体类型
    switch probe.Role {
    case core.MessageRoleUser:
        var msg core.UserMessage
        json.Unmarshal(e.MessageData, &msg)
        return []core.Message{msg}
    case core.MessageRoleAssistant:
        var msg core.AssistantMessage
        json.Unmarshal(e.MessageData, &msg)
        return []core.Message{msg}
    case core.MessageRoleTool:
        var msg core.ToolResultMessage
        json.Unmarshal(e.MessageData, &msg)
        return []core.Message{msg}
    }
}
```

## 6. BuildContext 流程

```
Session.BuildContext()
  │
  ├─ 扫找系统提示 (EntrySystemPrompt)
  ├─ 扫找模型配置 (EntryModelChange)
  ├─ 扫找思考级别 (EntryThinkingChange)
  ├─ 扫找压缩点 (EntryCompaction)
  │
  └─ 重建消息列表
       ├─ 压缩点之前: 只保留摘要消息
       └─ 压缩点之后: 保留所有消息
```

## 7. AgentSession 事件订阅

```go
agentSess, _ := NewAgentSession(storage)
defer agentSess.Close()

// 订阅事件
ch := agentSess.Subscribe(32)
defer agentSess.Unsubscribe(ch)

go func() {
    for event := range ch {
        switch e := event.(type) {
        case EventMessageUpdate:
            fmt.Print(e.Message)
        case EventRunEnd:
            fmt.Println("完成!")
        }
    }
}()
```

### 事件类型

```go
type AgentEvent interface { agentEventTag() }

type EventRunStart struct{}
type EventRunEnd struct { Messages []core.Message; Error error }
type EventMessageUpdate struct { Message core.Message }
type EventToolExecution struct { ToolName, Status string }
```

## 8. 辅助函数

```go
// 创建条目
NewUserMessageEntry(content string) SessionTreeEntry
NewAssistantMessageEntry(msg core.AssistantMessage) SessionTreeEntry
NewToolResultEntry(toolCallID, toolName string, content []core.ContentBlock, isError bool) SessionTreeEntry
NewSystemPromptEntry(prompt string) SessionTreeEntry
NewModelChangeEntry(provider, modelID string) SessionTreeEntry
NewCompactionEntry(summary string) SessionTreeEntry
```

## 9. 集成点

### 与 Agent Loop

```go
config.OnEvent = func(e agent.AgentEvent) {
    switch ev := e.(type) {
    case agent.EventMessageEnd:
        sess.Append(session.NewAssistantMessageEntry(ev.Message))
    case agent.EventToolExecEnd:
        sess.Append(session.NewToolResultEntry(ev.ToolCallID, ev.ToolName, ...))
    }
}
```

### 与 Context Manager

```go
ctxMgr.LoadFromSession(sess)
// ctxMgr 自动从 session 重建上下文
```

### 与 Memory

```go
// Session 管理对话历史
// Memory 管理长期事实
// 两者独立，互不依赖
```

## 10. 测试策略

| 测试类型 | 使用的后端 |
|----------|-----------|
| 单元测试 | MemoryStorage |
| 集成测试 | JSONLStorage |
| 端到端 | SQLiteStorage |

## 11. 后续计划

- [ ] 压缩条目合并
- [ ] 条目过期策略
- [ ] 导出/导入功能
- [ ] Redis 存储后端（分布式）
