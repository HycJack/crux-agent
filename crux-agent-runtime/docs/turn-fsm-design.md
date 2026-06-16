# Turn FSM 设计文档

> 参考: [crux-harness/turn](file:///mnt/workspace/crux/crux/crux-harness/turn)
> 状态: 设计方案，待实现

---

## 1. 概述

### 问题

当前 crux-agent-runtime 的 `agent` 包是一个"一次性运行"的 Agent 循环：
- 没有持久化的中间状态
- 无法在工具执行前暂停等待人工审批
- 无法在上下文压缩后恢复
- 无法跨进程/重启后继续

### 解决方案

引入 **Turn FSM**（有限状态机），将单次 Agent 运行分解为 9 个显式状态，支持：
- 持久化每个状态转换
- 在 `awaiting_approval` 状态暂停等待外部事件
- 从任意非终态恢复执行
- 与 `session`、`memory`、`context`、`autolearn` 包集成

---

## 2. 状态机

### 状态图

```
received → provisioning → streaming → dispatching ──→ completed
                                   ↓              ↑
                              awaiting        executing
                              approval            ↓
                                   ↑        steering
                                (resolve)      / \
                                   └─────────+   +→ completed
```

### 状态定义

| 状态 | 说明 | 终态? |
|------|------|-------|
| `received` | 收到用户输入，初始化 Turn | ❌ |
| `provisioning` | 加载上下文、记忆、会话历史 | ❌ |
| `streaming` | 调用 LLM 获取流式响应 | ❌ |
| `dispatching` | 分发工具调用 | ❌ |
| `awaiting_approval` | 等待人工审批（阻塞） | ❌ |
| `executing` | 执行工具（沙箱模式，预留） | ❌ |
| `steering` | 检查是否继续循环 | ❌ |
| `agent_running` | AgentRunner 模式（一体化） | ❌ |
| `completed` | 正常完成 | ✅ |
| `failed` | 失败 | ✅ |

### 事件

| 事件 | 触发场景 |
|------|----------|
| `start` | 开始新 Turn |
| `provisioned` | 上下文加载完成 |
| `stream_start` | 开始流式调用 |
| `stream_done` | 流式调用完成 |
| `tool_dispatched` | 工具调用已分发 |
| `tool_executed` | 工具执行完成 |
| `approval_resolved` | 审批通过 |
| `approval_denied` | 审批拒绝 |
| `steer_continue` | 继续循环 |
| `steer_stop` | 停止循环 |
| `abort` | 用户取消 |
| `resume` | 恢复执行 |

---

## 3. 核心类型

### Turn（持久化状态）

```go
type Turn struct {
    ID        string            `json:"id"`
    SessionID string            `json:"session_id"`
    UserID    string            `json:"user_id,omitempty"`
    AgentID   string            `json:"agent_id,omitempty"`
    State     State             `json:"state"`
    Round     int               `json:"round"`
    Messages  []core.Message    `json:"messages"`
    Pending   []core.ToolCall   `json:"pending,omitempty"`
    Metadata  map[string]string `json:"metadata,omitempty"`
    Error     string            `json:"error,omitempty"`
    CreatedAt time.Time         `json:"created_at"`
    UpdatedAt time.Time         `json:"updated_at"`
}
```

### Store（持久化接口）

```go
type Store interface {
    Save(ctx context.Context, turn *Turn) error
    Load(ctx context.Context, id string) (*Turn, error)
    List(ctx context.Context, sessionID string) ([]*Turn, error)
}
```

### StateHandler（状态处理器）

```go
type StateHandler func(ctx context.Context, turn *Turn, event Event) (State, error)
```

### Machine（FSM 执行器）

```go
type Machine struct {
    store    Store
    trigger  Trigger
    logger   *slog.Logger
    handlers map[State]StateHandler
}
```

---

## 4. 与现有包集成

### provisioning 状态

```go
func provisioningHandler(cfg StatesConfig) StateHandler {
    return func(ctx context.Context, t *Turn, e Event) (State, error) {
        // 1. 从 session 加载历史消息
        if cfg.Session != nil {
            ctx2 := cfg.Session.BuildContext()
            t.Messages = append(ctx2.Messages, t.Messages...)
        }

        // 2. 注入长期记忆
        if cfg.Memory != nil {
            memoryPrompt := cfg.Memory.FormatForPrompt()
            if memoryPrompt != "" {
                t.Messages = insertAfterSystem(t.Messages, core.UserMessage{
                    Role:    core.MessageRoleUser,
                    Content: memoryPrompt,
                })
            }
        }

        // 3. 上下文压缩
        if cfg.ContextMgr != nil {
            cfg.ContextMgr.CompactIfNeeded(ctx)
            t.Messages = cfg.ContextMgr.GetMessages()
        }

        // 4. 自动学习
        if cfg.AutoLearner != nil {
            for _, m := range t.Messages {
                if um, ok := m.(core.UserMessage); ok {
                    cfg.AutoLearner.ProcessUserInput(fmt.Sprintf("%v", um.Content))
                }
            }
        }

        return postProvisioningState(cfg), nil
    }
}
```

### completed 状态

```go
func completedHandler(cfg StatesConfig) StateHandler {
    return func(ctx context.Context, t *Turn, e Event) (State, error) {
        // 1. 持久化到 session
        if cfg.Session != nil {
            for _, m := range t.Messages {
                switch msg := m.(type) {
                case core.UserMessage:
                    cfg.Session.Append(session.NewUserMessageEntry(...))
                case core.AssistantMessage:
                    cfg.Session.Append(session.NewAssistantMessageEntry(msg))
                case core.ToolResultMessage:
                    cfg.Session.Append(session.NewToolResultEntry(...))
                }
            }
        }

        // 2. 自动学习
        if cfg.AutoLearner != nil {
            for _, m := range t.Messages {
                // 提取记忆...
            }
        }

        return StateCompleted, nil
    }
}
```

---

## 5. 两种运行模式

### 模式 1: 显式状态（Legacy）

每个 LLM→tool 循环经历 4 个状态：
```
streaming → dispatching → executing → steering → streaming → ...
```

**优点**: 细粒度控制
**缺点**: 状态多，复杂

### 模式 2: AgentRunner（推荐）

整个 LLM→tool 循环在一个状态内完成：
```
agent_running → completed / awaiting_approval
```

**优点**: 简单，复用 agent-runtime 功能
**缺点**: 粒度粗

---

## 6. 实现计划

### Phase 1: 核心 FSM (1-2 天)
- [ ] `turn/types.go` — State, Event, Turn 类型
- [ ] `turn/store.go` — Store 接口 + MemoryStore
- [ ] `turn/machine.go` — Machine 执行器
- [ ] `turn/trigger.go` — Trigger 接口 + ChannelTrigger

### Phase 2: 状态处理器 (2-3 天)
- [ ] `turn/states.go` — 默认状态处理器
- [ ] 集成测试

### Phase 3: AgentRunner 集成 (1-2 天)
- [ ] `turn/agent_runner.go` — AgentRunner 桥接
- [ ] 审批流程集成

### Phase 4: 包集成 (1-2 天)
- [ ] session + memory + context + autolearn 集成
- [ ] 端到端测试

### Phase 5: SQLite Store (1 天)
- [ ] `turn/sqlite.go` — SQLite 持久化

**总计: 6-10 天**
