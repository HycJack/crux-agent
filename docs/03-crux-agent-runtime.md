# crux-agent-runtime 模块设计

## 一、模块概述

`crux-agent-runtime` 是 Crux 框架的中间层，提供一个可重用的 **Agent 循环**，包含事件流和工具执行框架。它包装了 `crux-ai`，提供事件驱动的循环机制。

## 二、核心组件

### 2.1 目录结构

```
crux-agent-runtime/
├── agent/
│   ├── agent.go           # Agent 状态管理
│   ├── agent-loop.go      # 核心循环实现
│   └── types.go           # 类型定义
└── cmd/
    └── demo.go            # 演示程序
```

### 2.2 模块职责

| 组件 | 职责 |
|------|------|
| `agent.go` | Agent 状态管理和公共 API |
| `agent-loop.go` | 核心循环逻辑和事件生成 |
| `types.go` | 事件类型、工具定义、配置结构 |

## 三、Agent 设计

### 3.1 AgentState（状态）

```go
type AgentState struct {
    Model         core.Model
    SystemPrompt  string
    Messages      []core.Message
    Tools         []AgentTool
    ToolExecution ToolExecutionMode
    
    // 可插拔钩子
    ConvertToLlm        func([]core.Message) []core.Message
    TransformContext    func([]core.Message) []core.Message
    GetApiKey           func() string
    ShouldStopAfterTurn func(core.AssistantMessage, []core.ToolResultMessage) bool
    PrepareNextTurn     func(*AgentLoopConfig, core.AssistantMessage, []core.ToolResultMessage, []core.Message)
    BeforeToolCall      func(BeforeToolCallContext) *ToolCallBlock
    AfterToolCall       func(AfterToolCallContext) *ToolCallOverride
    StreamFn            StreamFn
    SimpleStreamOptions core.SimpleStreamOptions
}
```

**设计亮点**：
- 状态与行为分离，通过钩子函数实现扩展
- 线程安全的状态访问

### 3.2 Agent（代理）

```go
type Agent struct {
    mu          sync.RWMutex
    state       AgentState
    subscribers []func(AgentEvent)
    steering    []core.Message
    followUp    []core.Message
    cancel      context.CancelFunc
    streamWg    sync.WaitGroup
}
```

### 3.3 核心方法

| 方法 | 功能 |
|------|------|
| `New(opts AgentOptions)` | 创建新 Agent |
| `State()` | 获取当前状态（副本） |
| `SetTools(tools []AgentTool)` | 更新工具列表 |
| `SetModel(model core.Model)` | 更新模型 |
| `SetSystemPrompt(prompt string)` | 更新系统提示词 |
| `Messages()` | 获取消息历史 |
| `Subscribe(fn func(AgentEvent))` | 订阅事件 |
| `Steering(msgs ...core.Message)` | 注入转向消息 |
| `FollowUp(msgs ...core.Message)` | 注入跟进消息 |
| `Abort()` | 取消当前运行 |
| `Run(ctx context.Context, prompts ...core.Message)` | 开始新运行 |
| `RunContinue(ctx context.Context)` | 继续当前运行 |

## 四、事件系统

### 4.1 AgentEvent 接口

```go
type AgentEvent interface {
    agentEventTag()
}
```

### 4.2 事件类型

| 事件类型 | 触发时机 | 包含数据 |
|----------|----------|----------|
| `EventAgentStart` | Agent 运行开始 | 无 |
| `EventAgentEnd` | Agent 运行结束 | 最终消息列表 |
| `EventTurnStart` | 对话轮次开始 | 无 |
| `EventTurnEnd` | 对话轮次结束 | 助手消息、工具结果 |
| `EventMessageStart` | 助手消息开始 | 初始消息 |
| `EventMessageUpdate` | 助手消息更新 | 当前消息、底层事件 |
| `EventMessageEnd` | 助手消息结束 | 最终消息 |
| `EventToolExecStart` | 工具执行开始 | 工具调用 ID、名称、参数 |
| `EventToolExecUpdate` | 工具执行更新 | 部分结果 |
| `EventToolExecEnd` | 工具执行结束 | 结果、是否错误 |

### 4.3 事件流示例

```
EventAgentStart
  → EventTurnStart
    → EventMessageStart
      → EventMessageUpdate (多次)
      → EventMessageEnd
    → EventToolExecStart (如果有工具调用)
      → EventToolExecUpdate (可选)
      → EventToolExecEnd
    → EventTurnEnd
  → [重复多轮]
  → stream.End(messages)
```

## 五、Agent 循环设计

### 5.1 双层循环架构

```
┌─────────────────────────────────────────────────────┐
│              Outer Loop (外层循环)                    │
│    ┌─────────────────────────────────────────────┐   │
│    │          Inner Loop (内层循环)               │   │
│    │  [Turn Start] → [Stream LLM] → [Tool Call]  │   │
│    │           ↓ (has tool calls?)                │   │
│    │     Yes → [Execute Tools] → [Turn End]      │   │
│    │           ↓                                  │   │
│    │     No → Exit Inner Loop                     │   │
│    └─────────────────────────────────────────────┘   │
│           ↓ (has follow-up?)                         │
│    Yes → Continue Outer Loop                         │
│    No → End                                          │
└─────────────────────────────────────────────────────┘
```

### 5.2 核心循环实现

```go
func runLoop(ctx context.Context, config AgentLoopConfig, messages []core.Message, stream *AgentEventStream) {
    for {
        // Inner loop: process tool calls and steering messages
        for {
            // 检查上下文取消
            if ctx.Err() != nil {
                stream.End(messages)
                return
            }
            
            // 注入转向消息
            if config.GetSteeringMessages != nil {
                steering := config.GetSteeringMessages()
                if len(steering) > 0 {
                    messages = append(messages, steering...)
                    hasSteering = true
                }
            }
            
            // 发送 TurnStart 事件
            stream.Push(EventTurnStart{})
            
            // 流式获取助手响应
            assistantMsg, err := streamAssistantResponse(ctx, config, messages, stream)
            if err != nil {
                // 处理错误
                stream.End(messages)
                return
            }
            
            messages = append(messages, assistantMsg)
            
            // 提取工具调用
            toolCalls := extractToolCalls(assistantMsg)
            
            // 执行工具调用
            if len(toolCalls) > 0 {
                toolResults := executeToolCalls(ctx, config, assistantMsg, toolCalls, messages, stream)
                messages = append(messages, toolResults...)
            }
            
            // 发送 TurnEnd 事件
            stream.Push(EventTurnEnd{Message: assistantMsg, ToolResults: toolResults})
            
            // 检查是否停止
            if config.ShouldStopAfterTurn != nil && config.ShouldStopAfterTurn(assistantMsg, toolResults) {
                stream.End(messages)
                return
            }
            
            // 若无工具调用且无转向消息，退出内层循环
            if len(toolCalls) == 0 && !hasSteering {
                break
            }
        }
        
        // 检查跟进消息（外层循环）
        if config.GetFollowUpMessages != nil {
            followUp := config.GetFollowUpMessages()
            if len(followUp) > 0 {
                messages = append(messages, followUp...)
                continue // 继续外层循环
            }
        }
        
        // 无跟进消息，结束
        stream.End(messages)
        return
    }
}
```

## 六、工具执行

### 6.1 AgentTool 定义

```go
type AgentTool struct {
    Name          string
    Description   string
    Parameters    json.RawMessage // JSON Schema
    Label         string
    Execute       ToolExecuteFunc
    ExecutionMode ToolExecutionMode // "" = inherit from config
}

type ToolExecuteFunc func(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (AgentToolResult, error)

type AgentToolResult struct {
    Content   []core.ContentBlock
    Details   json.RawMessage
    IsError   bool
    Terminate bool // 是否终止 Agent 运行
}
```

### 6.2 执行模式

```go
type ToolExecutionMode string

const (
    ToolExecParallel   ToolExecutionMode = "parallel"
    ToolExecSequential ToolExecutionMode = "sequential"
)
```

### 6.3 工具执行流程

```go
func executeToolCalls(ctx context.Context, config AgentLoopConfig, assistantMsg core.AssistantMessage, 
    toolCalls []core.ToolCall, messages []core.Message, stream *AgentEventStream) ([]core.ToolResultMessage, bool) {
    
    // 确定执行模式
    mode := config.ToolExecution
    if mode == "" {
        mode = ToolExecParallel
    }
    
    // 检查是否有工具要求串行执行
    for _, tc := range toolCalls {
        if tool := findTool(config.Tools, tc.Name); tool != nil && tool.ExecutionMode == ToolExecSequential {
            mode = ToolExecSequential
            break
        }
    }
    
    if mode == ToolExecSequential {
        return executeToolCallsSequential(ctx, config, assistantMsg, toolCalls, messages, stream)
    }
    return executeToolCallsParallel(ctx, config, assistantMsg, toolCalls, messages, stream)
}
```

### 6.4 并行执行

```go
func executeToolCallsParallel(ctx context.Context, config AgentLoopConfig, ...) ([]core.ToolResultMessage, bool) {
    results := make([]core.ToolResultMessage, len(toolCalls))
    var wg sync.WaitGroup
    ch := make(chan indexedResult, len(toolCalls))
    
    for i, tc := range toolCalls {
        wg.Add(1)
        go func(idx int, toolCall core.ToolCall) {
            defer wg.Done()
            if ctx.Err() != nil {
                return
            }
            result, resultMsg := executeSingleToolCall(ctx, config, assistantMsg, toolCall, messages, stream)
            ch <- indexedResult{index: idx, result: resultMsg, terminate: result.Terminate}
        }(i, tc)
    }
    
    go func() {
        wg.Wait()
        close(ch)
    }()
    
    shouldTerminate := false
    for r := range ch {
        results[r.index] = r.result
        if r.terminate {
            shouldTerminate = true
        }
    }
    return results, shouldTerminate
}
```

### 6.5 钩子机制

#### BeforeToolCall

```go
type BeforeToolCallContext struct {
    AssistantMessage core.AssistantMessage
    ToolCall         core.ToolCall
    Args             json.RawMessage
    Messages         []core.Message
}

type ToolCallBlock struct {
    Block  bool
    Reason string
}

// 在工具执行前调用，可阻塞执行
if config.BeforeToolCall != nil {
    block := config.BeforeToolCall(BeforeToolCallContext{...})
    if block != nil && block.Block {
        return AgentToolResult{
            Content: []core.ContentBlock{core.TextContent{Text: block.Reason}},
            IsError: true,
        }, nil
    }
}
```

#### AfterToolCall

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

// 在工具执行后调用，可覆盖结果
if config.AfterToolCall != nil {
    override := config.AfterToolCall(AfterToolCallContext{...})
    if override != nil {
        // 覆盖结果字段
    }
}
```

## 七、流式响应处理

### 7.1 消息流转换

```go
func streamAssistantResponse(ctx context.Context, config AgentLoopConfig, messages []core.Message, 
    stream *AgentEventStream) (core.AssistantMessage, error) {
    
    // 转换上下文
    if config.TransformContext != nil {
        messages = config.TransformContext(messages)
    }
    
    // 转换为 LLM 消息格式
    convertFn := config.ConvertToLlm
    if convertFn == nil {
        convertFn = defaultConvertToLlm
    }
    llmMessages := convertFn(messages)
    
    // 解析 API 密钥
    apiKey := config.GetApiKey()
    if apiKey == "" {
        apiKey = config.SimpleStreamOptions.APIKey
    }
    
    // 构建上下文
    llmCtx := toContextMessages(llmMessages, config.SystemPrompt, config.Tools)
    
    // 流式调用
    streamFn := config.StreamFn
    if streamFn == nil {
        streamFn = func(ctx context.Context, m core.Model, c core.Context, o core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
            return ai.StreamSimpleWithContext(ctx, m, c, o)
        }
    }
    
    llmStream, err := streamFn(ctx, config.Model, llmCtx, opts)
    if err != nil {
        return core.AssistantMessage{}, err
    }
    
    // 处理流式事件
    var partialMsg core.AssistantMessage
    stream.Push(EventMessageStart{Message: partialMsg})
    
    finalMsg, err := llmStream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
        switch e := evt.(type) {
        case core.EventStart:
            partialMsg.API = e.API
            partialMsg.Provider = e.Provider
            partialMsg.Model = e.Model
        case core.EventTextDelta:
            partialMsg.Content = appendOrUpdateText(partialMsg.Content, e.Delta)
        case core.EventThinkingDelta:
            partialMsg.Content = appendOrUpdateThinking(partialMsg.Content, e.Delta)
        case core.EventToolCallStart:
            partialMsg.Content = append(partialMsg.Content, core.ToolCall{...})
        case core.EventToolCallDelta:
            partialMsg.Content = updateToolCallArgs(partialMsg.Content, e.ID, e.ArgumentsDelta)
        case core.EventToolCallEnd:
            partialMsg.Content = finalizeToolCallArgs(partialMsg.Content, e.ID, e.Arguments)
        case core.EventDone:
            partialMsg = e.Message
        case core.EventError:
            return fmt.Errorf("%s", e.ErrorMessage)
        }
        
        stream.Push(EventMessageUpdate{Message: partialMsg, AssistantEvent: evt})
        return nil
    })
    
    stream.Push(EventMessageEnd{Message: finalMsg})
    return finalMsg, nil
}
```

### 7.2 内容块操作

| 函数 | 功能 |
|------|------|
| `appendOrUpdateText(blocks, delta)` | 追加或更新文本内容 |
| `appendOrUpdateThinking(blocks, delta)` | 追加或更新思考内容 |
| `updateToolCallArgs(blocks, id, delta)` | 更新工具调用参数 |
| `finalizeToolCallArgs(blocks, id, args)` | 最终确定工具调用参数 |
| `setTextSignature(blocks, sig)` | 设置文本签名 |
| `setThinkingSignature(blocks, sig)` | 设置思考签名 |

## 八、转向与跟进机制

### 8.1 Steering（转向）

在当前轮次中注入消息，可用于动态调整 Agent 行为：

```go
func (a *Agent) Steering(msgs ...core.Message) {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.steering = append(a.steering, msgs...)
}
```

在循环中：
```go
if config.GetSteeringMessages != nil {
    steering := config.GetSteeringMessages()
    if len(steering) > 0 {
        messages = append(messages, steering...)
        hasSteering = true
    }
}
```

### 8.2 FollowUp（跟进）

在 Agent 本应停止后注入消息，触发额外轮次：

```go
func (a *Agent) FollowUp(msgs ...core.Message) {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.followUp = append(a.followUp, msgs...)
}
```

在循环中：
```go
if config.GetFollowUpMessages != nil {
    followUp := config.GetFollowUpMessages()
    if len(followUp) > 0 {
        messages = append(messages, followUp...)
        hasFollowUp = true
    }
}

if !hasFollowUp {
    stream.End(messages)
    return
}
```

## 九、线程安全

### 9.1 状态保护

```go
// 读取状态（使用 RLock）
func (a *Agent) State() AgentState {
    a.mu.RLock()
    defer a.mu.RUnlock()
    return a.state
}

// 修改状态（使用 Lock）
func (a *Agent) SetModel(model core.Model) {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.state.Model = model
}
```

### 9.2 取消机制

```go
func (a *Agent) Abort() {
    a.mu.Lock()
    cancel := a.cancel
    a.mu.Unlock()
    if cancel != nil {
        cancel()
    }
}
```

### 9.3 事件订阅

```go
func (a *Agent) Subscribe(fn func(AgentEvent)) {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.subscribers = append(a.subscribers, fn)
}

func (a *Agent) processStream(ctx context.Context, stream *AgentEventStream) {
    a.mu.RLock()
    subs := make([]func(AgentEvent), len(a.subscribers))
    copy(subs, a.subscribers)
    a.mu.RUnlock()
    
    go func() {
        stream.ForEach(ctx, func(evt AgentEvent) error {
            for _, fn := range subs {
                fn(evt)
            }
            return nil
        })
    }()
}
```

## 十、配置选项

### 10.1 AgentLoopConfig

```go
type AgentLoopConfig struct {
    core.SimpleStreamOptions
    
    Model         core.Model
    SystemPrompt  string
    Tools         []AgentTool
    ToolExecution ToolExecutionMode
    
    // 消息转换钩子
    ConvertToLlm        func([]core.Message) []core.Message
    TransformContext    func([]core.Message) []core.Message
    
    // 动态配置
    GetApiKey           func() string
    
    // 循环控制
    ShouldStopAfterTurn func(core.AssistantMessage, []core.ToolResultMessage) bool
    PrepareNextTurn     func(*AgentLoopConfig, core.AssistantMessage, []core.ToolResultMessage, []core.Message)
    
    // 消息注入
    GetSteeringMessages  func() []core.Message
    GetFollowUpMessages  func() []core.Message
    
    // 工具执行钩子
    BeforeToolCall      func(BeforeToolCallContext) *ToolCallBlock
    AfterToolCall       func(AfterToolCallContext) *ToolCallOverride
    
    // 自定义流式函数
    StreamFn            StreamFn
}
```

### 10.2 默认配置

```go
// 默认消息转换：过滤为 LLM 兼容类型
func defaultConvertToLlm(msgs []core.Message) []core.Message {
    result := make([]core.Message, 0, len(msgs))
    for _, m := range msgs {
        switch m.(type) {
        case core.UserMessage, core.AssistantMessage, core.ToolResultMessage:
            result = append(result, m)
        }
    }
    return result
}
```

## 十一、错误处理

### 11.1 错误类型

| 错误场景 | 处理方式 |
|----------|----------|
| LLM 调用失败 | 返回错误消息，设置 StopReason=StopError |
| 工具未找到 | 返回错误结果，IsError=true |
| 工具执行错误 | 返回错误结果，IsError=true |
| 上下文取消 | 优雅终止，设置 StopReason=StopAborted |
| panic | 捕获并转换为错误事件 |

### 11.2 错误传播

```go
// 捕获 panic
go func() {
    defer func() {
        if r := recover(); r != nil {
            stream.Error(fmt.Errorf("agent: panic: %v", r))
        }
    }()
    
    // 执行循环
    runLoop(ctx, config, messages, stream)
}()

// 错误消息处理
assistantMsg, err := streamAssistantResponse(ctx, config, messages, stream)
if err != nil {
    errMsg := core.AssistantMessage{
        Role:         "assistant",
        StopReason:   core.StopError,
        ErrorMessage: err.Error(),
    }
    messages = append(messages, errMsg)
    stream.Push(EventTurnEnd{Message: errMsg})
    stream.End(messages)
    return
}
```

## 十二、扩展点

### 12.1 自定义流式函数

```go
type StreamFn func(context.Context, core.Model, core.Context, core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error)

// 使用示例
config.StreamFn = func(ctx context.Context, m core.Model, c core.Context, o core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
    // 自定义流式逻辑
    return customStream(ctx, m, c, o)
}
```

### 12.2 自定义消息转换

```go
config.ConvertToLlm = func(msgs []core.Message) []core.Message {
    // 自定义转换逻辑
    return transformedMessages
}
```

### 12.3 自定义停止条件

```go
config.ShouldStopAfterTurn = func(msg core.AssistantMessage, results []core.ToolResultMessage) bool {
    // 自定义停止逻辑
    return shouldStop
}
```