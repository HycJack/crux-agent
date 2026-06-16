# 模块设计：Agent 循环

> 模块: crux-agent-runtime/agent
> 版本: v0.1.0 | 更新: 2026-06-17
> 状态: ⏳ 待完善（缺少测试，待集成 session/memory/context/autolearn）

---

## 1. 职责

自主 LLM 对话循环：流式调用 → 工具执行 → 继续对话。

**核心能力**：
- 双层循环（outer: follow-up, inner: tool calls）
- 并行/串行工具执行
- 事件驱动（AgentEvent 流）
- 可扩展钩子（BeforeToolCall, AfterToolCall, PrepareNextTurn）

## 2. 架构

```
AgentLoop(ctx, messages, config)
  │
  └─ outer loop (检查 follow-up)
       │
       └─ inner loop (最多 MaxRounds 轮)
            │
            ├─ streamAssistantResponse()
            │    ├─ transformContext()
            │    ├─ invokeStreamFn()
            │    └─ consumeStreamEvents()
            │
            ├─ executeToolCalls()
            │    ├─ beforeToolCall hook
            │    ├─ tool.Execute()
            │    └─ afterToolCall hook
            │
            └─ check termination
```

## 3. 核心类型

### AgentTool

```go
type AgentTool struct {
    Name          string
    Description   string
    Parameters    json.RawMessage
    Execute       ToolExecuteFunc
    ExecutionMode ToolExecutionMode  // "parallel" 或 "sequential"
}
```

### AgentLoopConfig

```go
type AgentLoopConfig struct {
    // 基础配置
    Model         core.Model
    SystemPrompt  string
    Tools         []AgentTool
    StreamFn      StreamFn
    MaxRounds     int

    // 钩子函数
    ConvertToLlm        func([]core.Message) []core.Message
    TransformContext    func([]core.Message) []core.Message
    GetApiKey           func() string
    ShouldStopAfterTurn func(...) bool
    PrepareNextTurn     func(...)
    GetSteeringMessages func() []core.Message
    GetFollowUpMessages func() []core.Message
    BeforeToolCall      func(...) *ToolCallBlock
    AfterToolCall       func(...) *ToolCallOverride
}
```

### StreamFn

```go
type StreamFn func(
    ctx context.Context,
    model core.Model,
    llmCtx core.Context,
    opts core.SimpleStreamOptions,
) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error)
```

## 4. 事件类型

```go
type AgentEvent interface { agentEventTag() }

// 生命周期
EventAgentStart     // Agent 开始
EventAgentEnd       // Agent 结束

// Turn 级别
EventTurnStart      // Turn 开始
EventTurnEnd        // Turn 结束

// 消息级别
EventMessageStart   // 消息开始
EventMessageUpdate  // 消息更新（流式 delta）
EventMessageEnd     // 消息结束

// 工具级别
EventToolExecStart  // 工具开始
EventToolExecUpdate // 工具更新
EventToolExecEnd    // 工具结束
```

## 5. 工具执行模式

```go
type ToolExecutionMode string

const (
    ToolExecParallel   ToolExecutionMode = "parallel"   // 并行
    ToolExecSequential ToolExecutionMode = "sequential"  // 串行
)
```

**选择逻辑**：
- 默认并行
- 任一工具指定 sequential → 全部串行
- 串行模式下，任一工具返回 Terminate=true → 停止

## 6. 钩子详解

### BeforeToolCall

```go
type BeforeToolCallContext struct {
    AssistantMessage core.AssistantMessage
    ToolCall         core.ToolCall
    Args             json.RawMessage
    Messages         []core.Message
}

type ToolCallBlock struct {
    Block  bool   // true = 阻止执行
    Reason string
}
```

**用途**：审批检查、权限检查、参数验证

### AfterToolCall

```go
type AfterToolCallContext struct {
    AssistantMessage core.AssistantMessage
    ToolCall         core.ToolCall
    Args             json.RawMessage
    Result           AgentToolResult
    IsError          bool
    Messages         []core.Message
}

type ToolCallOverride struct {
    Content   []core.ContentBlock
    Details   json.RawMessage
    IsError   *bool
    Terminate *bool
}
```

**用途**：结果脱敏、错误重写、强制终止

### PrepareNextTurn

```go
PrepareNextTurn func(config *AgentLoopConfig, assistantMsg core.AssistantMessage, 
    toolResults []core.ToolResultMessage, messages []core.Message)
```

**用途**：上下文压缩、动态调整 MaxRounds

## 7. 集成点（待实现）

### 与 Session

```go
config.OnEvent = func(e agent.AgentEvent) {
    if ev, ok := e.(agent.EventMessageEnd); ok {
        sess.Append(session.NewAssistantMessageEntry(ev.Message))
    }
}
```

### 与 Context Manager

```go
config.TransformContext = func(msgs []core.Message) []core.Message {
    ctxMgr.AddMessage(msgs...)
    return ctxMgr.GetMessages()
}
```

### 与 AutoLearn

```go
config.ConvertToLlm = func(msgs []core.Message) []core.Message {
    for _, m := range msgs {
        if um, ok := m.(core.UserMessage); ok {
            learner.ProcessUserInput(fmt.Sprintf("%v", um.Content))
        }
    }
    return msgs
}
```

### 与 Memory

```go
config.SystemPrompt = basePrompt + "\n\n" + mem.FormatForPrompt(ctx, "", 0)
```

## 8. 测试计划

### 单元测试

| 测试 | 说明 |
|------|------|
| TestAgentLoop_BasicFlow | 基本 LLM 调用流程 |
| TestAgentLoop_ToolExecution | 工具执行（并行/串行）|
| TestAgentLoop_BeforeToolCall | 钩子阻止执行 |
| TestAgentLoop_AfterToolCall | 钩子修改结果 |
| TestAgentLoop_MaxRounds | 达到最大轮次 |
| TestAgentLoop_Cancel | 取消操作 |
| TestAgentLoop_SteeringMessages | 中途注入消息 |
| TestAgentLoop_FollowUpMessages | 后续消息 |

### 集成测试

| 测试 | 说明 |
|------|------|
| TestAgentLoop_WithSession | 会话持久化 |
| TestAgentLoop_WithContextManager | 上下文管理 |
| TestAgentLoop_WithMemory | 记忆注入 |
| TestAgentLoop_WithAutoLearn | 自动学习 |

## 9. 待完善项

- [ ] 添加单元测试
- [ ] 集成 session
- [ ] 集成 memory
- [ ] 集成 context manager
- [ ] 集成 autolearn
- [ ] 实现 Agent 状态管理（IsRunning, Abort, Steer, FollowUp）

## 10. 后续计划

- [ ] 添加 metrics（token 使用、工具调用次数、循环轮次）
- [ ] 支持自定义事件处理器
- [ ] 支持 Agent 暂停/恢复
