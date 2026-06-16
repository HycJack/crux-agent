# 模块设计：Agent 循环

> 模块: crux-agent-runtime/agent
> 版本: v0.1.0 | 更新: 2026-06-17
> 状态: ⏳ 待完善（缺少测试）

---

## 1. 职责

自主 LLM 对话循环：流式调用 → 工具执行 → 继续对话。

**关键属性**：
- 双层循环（outer: follow-up, inner: tool calls）
- 并行/串行工具执行
- 事件驱动（AgentEvent 流）
- 可扩展钩子（BeforeToolCall, AfterToolCall, PrepareNextTurn）

## 2. 设计原则

1. **事件驱动** — 每个步骤发出事件，调用者订阅观察
2. **可组合** — 通过钩子扩展，而非继承
3. **并发安全** — 每个调用独立 channel，工具可并行执行
4. **可中断** — 通过 context 取消或 Abort() 停止

## 3. 核心类型

```go
// AgentTool 定义可调用的工具
type AgentTool struct {
    Name          string
    Description   string
    Parameters    json.RawMessage
    Execute       ToolExecuteFunc
    ExecutionMode ToolExecutionMode
}

// AgentLoopConfig 配置 Agent 循环
type AgentLoopConfig struct {
    Model         core.Model
    SystemPrompt  string
    Tools         []AgentTool
    StreamFn      StreamFn
    MaxRounds     int

    // 钩子
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

## 4. 双层循环

```
AgentLoop(ctx, messages, config)
  │
  └─ outer loop (无限)
       │
       ├─ 检查 ctx 取消
       │
       ├─ inner loop (最多 MaxRounds 轮)
       │    │
       │    ├─ 注入 steering 消息
       │    ├─ EventTurnStart
       │    │
       │    ├─ streamAssistantResponse()
       │    │    ├─ transformContext()
       │    │    ├─ convertToLlm()
       │    │    ├─ invokeStreamFn()
       │    │    └─ consumeStreamEvents() → EventMessageUpdate
       │    │
       │    ├─ 检查 StopReason (error/aborted → 退出)
       │    ├─ extractToolCalls()
       │    │
       │    ├─ executeToolCalls()
       │    │    ├─ 选择模式 (parallel/sequential)
       │    │    ├─ beforeToolCall 钩子
       │    │    ├─ tool.Execute()
       │    │    ├─ afterToolCall 钩子
       │    │    └─ EventToolExecStart/End
       │    │
       │    ├─ EventTurnEnd
       │    ├─ PrepareNextTurn 钩子 (→ context.CompactIfNeeded)
       │    ├─ ShouldStopAfterTurn 钩子
       │    │
       │    └─ 无 tool calls + 无 steering → break inner loop
       │
       ├─ 注入 follow-up 消息
       └─ 无 follow-up → 结束
```

## 5. 事件类型

```go
type AgentEvent interface { agentEventTag() }

// 生命周期
EventAgentStart     // Agent 开始运行
EventAgentEnd       // Agent 结束

// Turn 级别
EventTurnStart      // Turn 开始
EventTurnEnd        // Turn 结束（含 ToolResults）

// 消息级别
EventMessageStart   // 助手消息开始
EventMessageUpdate  // 助手消息更新（流式 delta）
EventMessageEnd     // 助手消息结束

// 工具级别
EventToolExecStart  // 工具执行开始
EventToolExecUpdate // 工具执行更新（部分结果）
EventToolExecEnd    // 工具执行结束
```

## 6. 工具执行模式

```go
type ToolExecutionMode string

const (
    ToolExecParallel   ToolExecutionMode = "parallel"   // 并行执行
    ToolExecSequential ToolExecutionMode = "sequential"  // 串行执行
)
```

**选择逻辑**：
- 默认并行
- 如果任一工具指定 sequential，全部串行
- 串行模式下，任一工具返回 Terminate=true，停止执行

## 7. 钩子详解

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
    Reason string // 阻止原因
}
```

**用途**：
- 审批检查（needs_approval → 阻止）
- 权限检查
- 参数验证

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
    Content   []core.ContentBlock // 覆盖结果内容
    Details   json.RawMessage
    IsError   *bool
    Terminate *bool
}
```

**用途**：
- 结果脱敏
- 错误重写
- 强制终止

### PrepareNextTurn

```go
PrepareNextTurn func(config *AgentLoopConfig, assistantMsg core.AssistantMessage, 
    toolResults []core.ToolResultMessage, messages []core.Message)
```

**用途**：
- 上下文压缩（context.CompactIfNeeded）
- 动态调整 MaxRounds
- 注入额外消息

## 8. StreamFn 签名

```go
type StreamFn func(
    ctx context.Context,
    model core.Model,
    llmCtx core.Context,
    opts core.SimpleStreamOptions,
) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error)
```

**调用方注入**：

```go
config.StreamFn = func(ctx, model, llmCtx, opts) {
    return llm.StreamSimpleWithContext(ctx, model, llmCtx, opts)
}
```

## 9. 集成点

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
config.SystemPrompt = basePrompt + "\n\n" + mem.FormatForPrompt()
```

## 10. 后续计划

- [ ] 添加单元测试（核心循环、工具执行、钩子）
- [ ] 实现 Agent 状态管理（IsRunning, Abort, Steer, FollowUp）
- [ ] 集成 session + memory + context + autolearn
- [ ] 添加 metrics（token 使用、工具调用次数、循环轮次）
