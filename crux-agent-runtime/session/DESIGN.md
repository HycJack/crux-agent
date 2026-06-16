# 模块设计：Session 持久化

> 模块: crux-agent-runtime/session
> 版本: v0.1.0 | 更新: 2026-06-17
> 状态: ✅ 已完成

---

## 1. 职责

会话级对话历史的持久化存储。三种后端：Memory、JSONL、SQLite。

**关键属性**：
- 跨重启保留对话历史
- 支持结构化条目（消息、系统提示、模型切换、压缩摘要）
- 重建 LLM 上下文（BuildContext）
- 事件订阅系统（AgentSession）

## 2. 设计原则

1. **接口驱动** — SessionStorage 接口，可替换后端
2. **树结构条目** — SessionTreeEntry 支持多种类型，不只是消息
3. **延迟解析** — MessageData 用 json.RawMessage，按需解析
4. **事件广播** — AgentSession 的 fanout 支持多订阅者

## 3. 核心类型

```go
// 会话树节点
type SessionTreeEntry struct {
    Type      EntryType        // user_message, assistant_message, tool_result...
    Timestamp time.Time
    MessageData json.RawMessage // 延迟解析
    SessionID string
    Provider  string
    ModelID   string
    // ...
}

// 持久化接口
type SessionStorage interface {
    ReadAll() ([]SessionTreeEntry, error)
    Append(entries []SessionTreeEntry) error
    Close() error
}
```

## 4. 存储后端对比

| 后端 | 并发安全 | 持久化 | 性能 | 使用场景 |
|------|----------|--------|------|----------|
| MemoryStorage | ✅ (RWMutex) | ❌ | 最快 | 测试 |
| JSONLStorage | ✅ (Mutex) | ✅ | 中等 | 开发环境 |
| SQLiteStorage | ✅ (SQLite WAL) | ✅ | 快 | 生产环境 |

## 5. BuildContext 流程

```
Session.Entries()
  │
  ├─ 扫找系统提示 (EntrySystemPrompt)
  ├─ 扫找模型配置 (EntryModelChange)
  ├─ 扫找压缩点 (EntryCompaction)
  │
  └─ 重建消息列表
       ├─ 压缩点之前: 只保留摘要消息
       └─ 压缩点之后: 保留所有消息
```

## 6. JSON 反序列化

Message 是接口类型，json.Unmarshal 无法直接解析。解决方案：

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
    // ...
    }
}
```

## 7. SQLite Schema

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

## 8. 集成点

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

### 与 Turn FSM（计划中）

```
turn FSM: provisioning
  → session.BuildContext() 加载历史
  → 注入到 turn.Messages

turn FSM: completed
  → session.Append() 持久化新消息
```

## 9. 后续计划

- [ ] 压缩条目合并（多个小条目合并为一个）
- [ ] 条目过期策略（自动删除超过 N 天的条目）
- [ ] 导出/导入功能
