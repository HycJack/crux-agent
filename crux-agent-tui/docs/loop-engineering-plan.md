# Loop Engineering 方案 —— Crux Agent TUI

> 版本: v1.0
> 日期: 2026-07-02
> 目标: 对 `internal/agent/loop.go` 中的 Agent 主循环进行抽象化、可插拔化重构，提升可扩展性、可测试性和可维护性。

---

## 目录

1. [背景与现状分析](#1-背景与现状分析)
2. [设计目标](#2-设计目标)
3. [整体架构](#3-整体架构)
4. [核心接口定义](#4-核心接口定义)
5. [Pipeline 引擎](#5-pipeline-引擎)
6. [内置 Stage 实现](#6-内置-stage-实现)
7. [扩展机制](#7-扩展机制)
8. [建议的实施路径](#8-建议的实施路径)
9. [附录：对比现有代码](#9-附录对比现有代码)

---

## 1. 背景与现状分析

### 1.1 当前实现

`loop.go` 中的 `runLoop` 是 Agent 的核心，采用 **ReAct 模式**（推理 → 行动 → 观察），其流程为：

```
runLoop (每轮循环)
  ├── maybeCompact()        → 上下文压缩
  ├── buildLLMContext()     → 构建 LLM 请求
  ├── streamResponse()      → 调用 LLM，获取文本+工具调用
  ├── 检查 stopReason       → 是否终止
  ├── 检查 toolCalls        → 是否还有工具需要执行
  └── 执行工具 → 结果追加到 Messages → 回到循环顶部
```

### 1.2 存在的问题

| 问题 | 说明 |
|------|------|
| 硬编码流程 | 所有步骤写死在一个函数中，无法单独替换某个环节 |
| 难以扩展 | 增加"日志记录"、"重试"、"验证"等环节需修改核心代码 |
| 难以测试 | 整个 Loop 作为一个整体测试，无法单独测试某个阶段 |
| 耦合紧密 | Agent 结构体同时承担状态管理、事件发布、LLM 调用、工具执行 |
| 难以复用 | 无法在其他项目或场景中复用这个 Loop 逻辑 |

---

## 2. 设计目标

### 2.1 核心原则

| 原则 | 说明 |
|------|------|
| 可插拔 | 每个环节（Stage）可独立替换、组合 |
| 可组合 | 不同的 Stage 通过 Pipeline 串联成完整流程 |
| 可测试 | 每个 Stage 可独立测试，Pipeline 可 mock |
| 可观测 | 每个 Stage 的执行可被 Hook/中间件拦截 |
| 自包含 | 每个 Stage 职责单一，只做一件事 |

### 2.2 目标 vs 现状

| 能力 | 当前 (V0) | 目标 (V1) |
|------|-----------|-----------|
| 流程控制 | 硬编码在 runLoop | Pipeline + Stage 编排 |
| 扩展方式 | 修改 loop.go | 添加新 Stage / 注册中间件 |
| 测试粒度 | 集成测试 | 单元测试每个 Stage |
| 观察能力 | 仅通过 PublishEvent 向 UI 推送 | 通用 Hook / 中间件机制 |
| 错误处理 | 全局错误返回 | 每个 Stage 可独立处理错误 |

---

## 3. 整体架构

### 3.1 分层设计

```
+------------------------------------------------------------+
|                     Agent Harness                           |
|  +------------------------------------------------------+  |
|  |                    Pipeline                           |  |
|  |  +----------+  +----------+  +----------+  +-----+  |  |
|  |  | Stage 1  |--> Stage 2  |--> Stage 3  |--> ...|  |  |
|  |  | (Compact)|  |  (LLM)   |  | (Tool)   |  |     |  |  |
|  |  +----------+  +----------+  +----------+  +-----+  |  |
|  +------------------------------------------------------+  |
|                            |                               |
|  +------------------------------------------------------+  |
|  |                    Hook Chain                        |  |
|  |  [BeforeStage] -> [AfterStage] -> [OnError] -> Done |  |
|  +------------------------------------------------------+  |
|                            |                               |
|  +------------------------------------------------------+  |
|  |                Logger / Metrics                      |  |
|  +------------------------------------------------------+  |
+------------------------------------------------------------+
```

### 3.2 核心组件

| 组件 | 职责 | 对应现有代码 |
|------|------|-------------|
| Stage | 定义 Agent Loop 中一个可独立执行的阶段 | runLoop 中的每个步骤 |
| Pipeline | 编排多个 Stage，按顺序执行 | runLoop 整个函数 |
| RunState | 跨 Stage 传递的上下文数据 | Agent 的字段 + 局部变量 |
| Hook | 在 Stage 执行前后注入自定义逻辑 | (无) |
| AgentHarness | 组合 Pipeline + Hook 的顶层运行时 | Agent 结构体 |

---

## 4. 核心接口定义

### 4.1 Stage 接口

每个 Stage 代表 Agent Loop 中的一个独立步骤：

```go
// Stage 定义 Agent Loop 中一个可独立执行的阶段
type Stage interface {
    Name() string
    Run(ctx context.Context, state *RunState) (*RunState, error)
}
```

### 4.2 RunState

RunState 替代原来散落在 Agent 结构体和局部变量中的状态：

```go
// RunState 是跨 Stage 传递的上下文数据
type RunState struct {
    Messages     []provider.Message
    TextBuffer   string
    ToolCalls    []provider.ToolCallContent
    StopReason   provider.StopReason
    Round        int
    MaxRounds    int
    Error        error
    Metadata     map[string]any
}
```

### 4.3 Hook 接口

Hook 提供无侵入的观察能力：

```go
// Hook 在 Stage 执行前后注入自定义逻辑
type Hook interface {
    BeforeStage(ctx context.Context, stageName string, state *RunState)
    AfterStage(ctx context.Context, stageName string, state *RunState, err error)
}
```

常见 Hook 用途：

| Hook | 用途 |
|------|------|
| LoggingHook | 记录每个 Stage 的入参、出参、耗时 |
| MetricsHook | 采集每个 Stage 的执行次数、成功率 |
| DebugHook | 在开发时打印详细的执行追踪 |
| TracingHook | 集成分布式追踪 (OpenTelemetry) |

---

## 5. Pipeline 引擎

Pipeline 是 Loop Engineering 的核心，负责编排 Stage 的执行顺序并控制循环逻辑。

### 5.1 Pipeline 结构

```go
// Pipeline 编排多个 Stage 按顺序执行
type Pipeline struct {
    stages    []Stage
    hooks     []Hook
    maxRounds int
}

func NewPipeline(stages []Stage, opts ...PipelineOption) *Pipeline {
    p := &Pipeline{stages: stages, maxRounds: 50}
    for _, opt := range opts {
        opt(p)
    }
    return p
}

func (p *Pipeline) Run(ctx context.Context, initialState *RunState) (*RunState, error) {
    state := initialState
    if state.MaxRounds <= 0 {
        state.MaxRounds = p.maxRounds
    }
    for state.Round < state.MaxRounds {
        for _, stage := range p.stages {
            if err := ctx.Err(); err != nil {
                return state, err
            }
            for _, h := range p.hooks {
                h.BeforeStage(ctx, stage.Name(), state)
            }
            var err error
            state, err = stage.Run(ctx, state)
            for _, h := range p.hooks {
                h.AfterStage(ctx, stage.Name(), state, err)
            }
            if err != nil {
                state.Error = err
                return state, err
            }
        }
        if p.shouldStop(state) {
            break
        }
        state.Round++
    }
    if state.Round >= state.MaxRounds {
        state.Error = fmt.Errorf("agent: exceeded max rounds (%d)", state.MaxRounds)
    }
    return state, state.Error
}

func (p *Pipeline) shouldStop(state *RunState) bool {
    switch state.StopReason {
    case provider.StopStop, provider.StopError, provider.StopAborted:
        return true
    }
    return len(state.ToolCalls) == 0
}
```

### 5.2 PipelineOption

```go
type PipelineOption func(*Pipeline)

func WithHooks(hooks ...Hook) PipelineOption {
    return func(p *Pipeline) { p.hooks = append(p.hooks, hooks...) }
}

func WithMaxRounds(n int) PipelineOption {
    return func(p *Pipeline) { p.maxRounds = n }
}
```

### 5.3 使用示例

```go
pipeline := NewPipeline(
    []Stage{
        &ContextCompactionStage{MaxTokens: 4000},
        &LLMInvocationStage{Provider: openaiProvider},
        &ToolExecutionStage{ToolRegistry: toolRegistry},
    },
    WithMaxRounds(30),
    WithHooks(NewLoggingHook(logger), NewMetricsHook()),
)

finalState, err := pipeline.Run(ctx, &RunState{
    Messages:  []provider.Message{userMsg},
    MaxRounds: 30,
})
```

---

## 6. 内置 Stage 实现

### 6.1 ContextCompactionStage

对应现有的 `maybeCompact()`，抽象为独立的上下文压缩策略：

```go
// CompactionStrategy 定义上下文压缩策略
type CompactionStrategy interface {
    Compact(messages []provider.Message) []provider.Message
}

// SlidingWindowStrategy 滑动窗口策略（当前实现的抽象）
type SlidingWindowStrategy struct {
    MaxTokens     int
    TokenBudget   int
    PreserveFirst int
    KeepLast      int
}

func (s *SlidingWindowStrategy) Compact(msgs []provider.Message) []provider.Message {
    estimated := 0
    for _, msg := range msgs {
        estimated += len(msg.Content) / 4
        estimated += s.TokenBudget
    }
    if estimated <= s.MaxTokens || len(msgs) <= 10 {
        return msgs
    }
    keep := s.KeepLast
    if keep <= 0 {
        keep = 8
    }
    preserved := make([]provider.Message, 0, keep+2)
    if s.PreserveFirst > 0 && len(msgs) > 2 {
        preserved = append(preserved, msgs[0])
    }
    start := len(msgs) - keep
    if start < 2 {
        start = 2
    }
    preserved = append(preserved, msgs[start:]...)
    return preserved
}

// SummaryStrategy 摘要压缩策略（Future Work）
type SummaryStrategy struct {
    LLMProvider provider.LLMProvider
}

// ContextCompactionStage
type ContextCompactionStage struct {
    Strategy CompactionStrategy
}

func (s *ContextCompactionStage) Name() string { return "compact" }

func (s *ContextCompactionStage) Run(ctx context.Context, state *RunState) (*RunState, error) {
    if s.Strategy != nil {
        state.Messages = s.Strategy.Compact(state.Messages)
    }
    return state, nil
}
```

### 6.2 LLMInvocationStage

对应现有的 `buildLLMContext()` + `streamResponse()`：

```go
type LLMInvocationStage struct {
    Provider  provider.LLMProvider
    APIKey    string
    BaseURL   string
    Model     string
    MaxTokens int
    Headers   map[string]string
    OnDelta   func(string)
}

func (s *LLMInvocationStage) Name() string { return "llm" }

func (s *LLMInvocationStage) Run(ctx context.Context, state *RunState) (*RunState, error) {
    if s.MaxTokens <= 0 {
        s.MaxTokens = 4096
    }
    llmCtx := provider.LLMContext{
        SystemPrompt: state.SystemPrompt,
        Messages:     state.Messages,
        Tools:        state.Tools,
    }
    opts := provider.StreamOptions{
        APIKey: s.APIKey, BaseURL: s.BaseURL,
        Model: s.Model, MaxTokens: s.MaxTokens,
        Headers: s.Headers,
    }
    stream, err := s.Provider.Stream(ctx, llmCtx, opts)
    if err != nil {
        return state, fmt.Errorf("llm call: %w", err)
    }

    toolCallBuf := make(map[string]*strings.Builder)
    var textBuf strings.Builder

    _, err = stream.ForEach(ctx, func(evt provider.StreamEvent) error {
        switch e := evt.(type) {
        case provider.EventTextDelta:
            textBuf.WriteString(e.Delta)
            if s.OnDelta != nil {
                s.OnDelta(e.Delta)
            }
        case provider.EventToolCallStart:
            toolCallBuf[e.ID] = &strings.Builder{}
        case provider.EventToolCallDelta:
            if buf, ok := toolCallBuf[e.ID]; ok {
                buf.WriteString(e.Delta)
            }
        case provider.EventDone:
            state.StopReason = e.StopReason
        }
        return nil
    })
    if err != nil {
        return state, err
    }

    state.TextBuffer = textBuf.String()
    state.ToolCalls = nil
    for id, buf := range toolCallBuf {
        state.ToolCalls = append(state.ToolCalls, provider.ToolCallContent{
            Type: "toolCall", ID: id,
        })
    }
    return state, nil
}
```

### 6.3 ToolExecutionStage

对应现有的 `executeTool()`，支持顺序和并行两种模式：

```go
type ToolExecutor interface {
    Name() string
    Execute(ctx context.Context, params json.RawMessage) (content string, isError bool)
}

type ToolExecutionStage struct {
    Registry    map[string]ToolExecutor
    Parallel    bool
    MaxParallel int
}

func (s *ToolExecutionStage) Name() string { return "tool" }

func (s *ToolExecutionStage) Run(ctx context.Context, state *RunState) (*RunState, error) {
    if len(state.ToolCalls) == 0 {
        return state, nil
    }
    if s.Parallel && len(state.ToolCalls) > 1 {
        return s.runParallel(ctx, state)
    }
    return s.runSequential(ctx, state)
}

func (s *ToolExecutionStage) runSequential(ctx context.Context, state *RunState) (*RunState, error) {
    for _, tc := range state.ToolCalls {
        executor, ok := s.Registry[tc.Name]
        if !ok {
            state.Messages = append(state.Messages, provider.Message{
                Role: provider.RoleTool, ToolCallID: tc.ID,
                ToolName: tc.Name, Content: "Tool not found", IsError: true,
            })
            continue
        }
        content, isError := executor.Execute(ctx, tc.Arguments)
        state.Messages = append(state.Messages, provider.Message{
            Role: provider.RoleTool, ToolCallID: tc.ID,
            ToolName: tc.Name, Content: content, IsError: isError,
        })
    }
    state.ToolCalls = nil
    return state, nil
}

func (s *ToolExecutionStage) runParallel(ctx context.Context, state *RunState) (*RunState, error) {
    var mu sync.Mutex
    var wg sync.WaitGroup
    sem := make(chan struct{}, s.MaxParallel)
    if cap(sem) == 0 {
        sem = make(chan struct{}, 5)
    }
    for _, tc := range state.ToolCalls {
        tc := tc
        wg.Add(1)
        go func() {
            defer wg.Done()
            sem <- struct{}{}
            defer func() { <-sem }()
            executor, ok := s.Registry[tc.Name]
            if !ok {
                mu.Lock()
                state.Messages = append(state.Messages, provider.Message{
                    Role: provider.RoleTool, ToolCallID: tc.ID,
                    ToolName: tc.Name, Content: "Tool not found", IsError: true,
                })
                mu.Unlock()
                return
            }
            content, isError := executor.Execute(ctx, tc.Arguments)
            mu.Lock()
            state.Messages = append(state.Messages, provider.Message{
                Role: provider.RoleTool, ToolCallID: tc.ID,
                ToolName: tc.Name, Content: content, IsError: isError,
            })
            mu.Unlock()
        }()
    }
    wg.Wait()
    state.ToolCalls = nil
    return state, nil
}
```

### 6.4 OutputStage

新增 Stage，负责将 LLM 输出写入消息历史：

```go
type OutputStage struct {
    OnOutput func(provider.Message)
}

func (s *OutputStage) Name() string { return "output" }

func (s *OutputStage) Run(ctx context.Context, state *RunState) (*RunState, error) {
    if state.TextBuffer != "" {
        msg := provider.Message{
            Role:       provider.RoleAssistant,
            Content:    state.TextBuffer,
            StopReason: state.StopReason,
        }
        state.Messages = append(state.Messages, msg)
        if s.OnOutput != nil {
            s.OnOutput(msg)
        }
    }
    return state, nil
}
```

### 6.5 Stage 组合示例

```go
// ReAct 模式（标准）
reactPipeline := NewPipeline([]Stage{
    &ContextCompactionStage{Strategy: &SlidingWindowStrategy{MaxTokens: 4000}},
    &LLMInvocationStage{Provider: provider},
    &ToolExecutionStage{Registry: tools},
})

// 带反思的 ReAct 模式
reactReflectionPipeline := NewPipeline([]Stage{
    &ContextCompactionStage{Strategy: &SlidingWindowStrategy{MaxTokens: 4000}},
    &LLMInvocationStage{Provider: provider},
    &ToolExecutionStage{Registry: tools},
    &ReflectionStage{Provider: provider},
})

// Plan-Execute 模式
planExecutePipeline := NewPipeline([]Stage{
    &PlanningStage{Provider: provider},
    &ContextCompactionStage{Strategy: &SlidingWindowStrategy{MaxTokens: 4000}},
    &LLMInvocationStage{Provider: provider},
    &ToolExecutionStage{Registry: tools},
    &ProgressCheckStage{},
})


---

## 7. 扩展机制

### 7.1 StageMiddleware

中间件模式允许在不修改 Stage 本身的情况下增强其行为：

```go
type StageMiddleware func(Stage) Stage
```

### 7.2 内置中间件

```go
// LoggingStage 日志中间件
type LoggingStage struct {
    inner  Stage
    logger *slog.Logger
}

func (s *LoggingStage) Name() string { return s.inner.Name() }

func (s *LoggingStage) Run(ctx context.Context, state *RunState) (*RunState, error) {
    start := time.Now()
    s.logger.Info("stage start", "stage", s.inner.Name(), "round", state.Round)
    state, err := s.inner.Run(ctx, state)
    elapsed := time.Since(start)
    if err != nil {
        s.logger.Error("stage error", "stage", s.inner.Name(), "err", err, "elapsed", elapsed)
    } else {
        s.logger.Info("stage done", "stage", s.inner.Name(), "elapsed", elapsed)
    }
    return state, err
}

// RetryStage 重试中间件
type RetryStage struct {
    inner      Stage
    maxRetries int
}

func (s *RetryStage) Name() string { return s.inner.Name() }

func (s *RetryStage) Run(ctx context.Context, state *RunState) (*RunState, error) {
    var lastErr error
    for i := 0; i <= s.maxRetries; i++ {
        if i > 0 {
            time.Sleep(time.Duration(1<<uint(i-1)) * 100 * time.Millisecond)
        }
        var newState *RunState
        newState, lastErr = s.inner.Run(ctx, state)
        if lastErr == nil {
            return newState, nil
        }
        state = newState
    }
    return state, fmt.Errorf("retry failed after %d attempts: %w", s.maxRetries, lastErr)
}

// TimeoutStage 超时中间件
type TimeoutStage struct {
    inner   Stage
    timeout time.Duration
}

func (s *TimeoutStage) Name() string { return s.inner.Name() }

func (s *TimeoutStage) Run(ctx context.Context, state *RunState) (*RunState, error) {
    ctx, cancel := context.WithTimeout(ctx, s.timeout)
    defer cancel()
    return s.inner.Run(ctx, state)
}
```

### 7.3 中间件应用方式

```go
// 方式一：对特定 Stage 应用中间件
stages := []Stage{
    &ContextCompactionStage{...},
    ApplyMiddlewares(
        &LLMInvocationStage{...},
        LoggingMiddleware(logger),
        RetryMiddleware(3),
        TimeoutMiddleware(30 * time.Second),
    ),
    &ToolExecutionStage{...},
}

// 方式二：全局中间件（作用于所有 Stage）
pipeline := NewPipeline(
    stages,
    WithGlobalMiddleware(LoggingMiddleware(logger), MetricsMiddleware(metrics)),
)
```

### 7.4 自定义 Stage 示例

```go
type SafetyCheckStage struct {
    inner Stage
}

func (s *SafetyCheckStage) Name() string { return "safety_check" }

func (s *SafetyCheckStage) Run(ctx context.Context, state *RunState) (*RunState, error) {
    for _, tc := range state.ToolCalls {
        if tc.Name == "bash" {
            var args struct{ Command string }
            json.Unmarshal(tc.Arguments, &args)
            if isDangerous(args.Command) {
                return state, fmt.Errorf("safety check failed")
            }
        }
    }
    return state, nil
}
```

---

## 8. 建议的实施路径

### 8.1 分阶段实施

| 阶段 | 内容 | 涉及文件 | 预计工作量 |
|------|------|---------|-----------|
| Phase 1 | 定义接口 + 提取 RunState | types.go 新增接口 | 0.5 天 |
| Phase 2 | 将 runLoop 拆为独立的 Stage | loop.go 拆为多个 Stage | 1 天 |
| Phase 3 | 实现 Pipeline 引擎 + Hook 机制 | 新增 pipeline.go | 0.5 天 |
| Phase 4 | 添加中间件（日志、重试、指标） | 新增 middleware.go | 1 天 |
| Phase 5 | 测试覆盖 + 文档完善 | *_test.go + 文档 | 0.5 天 |

### 8.2 Phase 1 详细步骤（最优先）

1. 在 types.go 中添加 Stage、RunState、Hook 接口
2. 保持 Agent 结构体和 runLoop 不变
3. 新增 state.go 文件
4. 编写接口单元测试

### 8.3 Phase 2 详细步骤

1. 逐个提取 Stage：ContextCompactionStage / LLMInvocationStage / ToolExecutionStage
2. 每个 Stage 放独立文件
3. runLoop 改为调用 Pipeline

### 8.4 向下兼容

```go
func (a *Agent) Run(ctx context.Context, content string) ([]provider.Message, error) {
    pipeline := NewPipeline(a.buildStages(), WithHooks(a.buildHooks()...))
    state := &RunState{
        Messages:  append(a.state.Messages, provider.Message{Role: provider.RoleUser, Content: content}),
        MaxRounds: maxAgentRounds,
    }
    finalState, err := pipeline.Run(ctx, state)
    a.state.Messages = finalState.Messages
    return finalState.Messages, err
}
```

---

## 9. 附录：对比现有代码

### 9.1 文件结构对比

```
当前结构：
internal/agent/
  types.go    ← Agent + 事件类型 + 工具定义
  loop.go     ← runLoop + streamResponse + executeTool + maybeCompact

目标结构：
internal/agent/
  types.go        ← Agent + Stage + RunState + Hook 接口
  pipeline.go     ← Pipeline 引擎
  stage_compact.go ← ContextCompactionStage
  stage_llm.go     ← LLMInvocationStage
  stage_tool.go    ← ToolExecutionStage
  stage_output.go  ← OutputStage
  middleware.go    ← 内置中间件
  agent.go         ← Agent 结构体（兼容层）
```

### 9.2 核心变化

| 现有代码 | 新架构 |
|---------|--------|
| Agent.runLoop(ctx) | Pipeline.Run(ctx, state) |
| Agent.maybeCompact() | ContextCompactionStage.Run |
| Agent.streamResponse() | LLMInvocationStage.Run |
| Agent.executeTool() | ToolExecutionStage.Run |
| Agent.buildLLMContext() | 移至 LLMInvocationStage |
| Agent.PublishEvent() | 通过 Hook 或中间件 |
| 循环控制写 runLoop | Pipeline.Run 统一控制 |

### 9.3 不变的部分

- provider/types.go — LLMProvider 接口、事件流、消息类型
- ui/tools.go — 工具实现（bash、read_file 等）
- ui/app.go、ui/chat.go 等 UI 层代码
- .env、go.mod、main.go 等配置和入口

---

## 总结

本方案通过 **Stage + Pipeline + Hook** 三层抽象，将目前硬编码的 Agent Loop 改造为可插拔、可组合、可测试、可观测的架构。

改造后：

- **添加新能力** = 写一个新 Stage，插入 Pipeline
- **观察运行时** = 注册一个 Hook
- **增强已有 Stage** = 包一层 Middleware
- **复用 Loop 逻辑** = 导出 Pipeline + Stage

整体改造成本约 **3.5 天**，可分 5 个阶段逐步实施。
