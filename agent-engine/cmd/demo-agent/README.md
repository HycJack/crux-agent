# demo-agent — crux-ai + agent-engine 完整示例

一个使用 `crux-ai`（Provider 适配器）与 `agent-engine`（Agent 运行时）的
完整可运行 demo agent，演示 agent-engine 的所有核心能力。

## 演示的能力

| 能力 | 说明 |
|------|------|
| **多 Provider 支持** | 通过 `-provider` 选择 anthropic / openai / google / deepseek / glm / kimi / xai 等 |
| **Tool 调用** | 内置 `get_weather` / `read_file` 两个工具，含 JSON Schema |
| **事件订阅** | 订阅 `AgentEvent` 流，渲染到终端 |
| **Steering/FollowUp** | 在运行中注入消息（控制台 REPL 不直接展示，可通过 API 调用） |
| **Abort + 自动补齐** | Ctrl-C 取消后自动补齐被中断的 tool call |
| **ProviderStreamFn** | 两层事件模式（ProviderEvent → AssistantMessageEvent 自动桥接） |
| **CompactionConfig** | 自动上下文压缩（通过 `engine.AgentLoopConfig.Compaction`） |
| **Pipeline** | 用 `engine.NewPipeline` 编排 Stage 替代默认 AgentLoop |

## 编译与运行

### 1. 编译

```bash
cd agent-engine
go build -o demo-agent ./cmd/demo-agent/
```

### 2. 配置 API key

```bash
# Anthropic
export ANTHROPIC_API_KEY=sk-ant-...

# OpenAI
export OPENAI_API_KEY=sk-...

# Google Gemini
export GOOGLE_API_KEY=...

# DeepSeek / GLM / Kimi / Xiaomi 等
export DEEPSEEK_API_KEY=sk-...
```

### 3. 运行

#### 单查询模式

```bash
# 默认 provider (anthropic) + 默认模型 (claude-sonnet-4-20250514)
./demo-agent -query "北京天气怎么样"

# 指定 provider + 模型
./demo-agent -provider openai -model gpt-4o -query "你好"

# DeepSeek
./demo-agent -provider deepseek -model deepseek-chat -query "写一首诗"

# 显示每个 AgentEvent（调试用）
./demo-agent -provider anthropic -model claude-sonnet-4-20250514 -query "hi" -events
```

#### 交互 REPL

```bash
./demo-agent
```

进入 REPL 后：
- 输入问题直接对话
- `/abort` 取消当前 turn（自动补齐被中断的 tool call）
- `/reset` 清除历史
- `/history` 打印消息历史
- `/quit` 退出

### 4. 启用两层事件模式

```bash
./demo-agent -two-layer
```

`mockProviderStreamFn()` 给出了 `ProviderStreamFn` 的完整写法，引擎会
自动通过 `core.CanonicalizeProviderStream` 桥接到 `AssistantMessageEvent`。
下游消费逻辑完全不变。

## 架构

```
┌─────────────────────────────────────────────────────────────┐
│ demo-agent (cmd/demo-agent/main.go)                         │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ Agent (wraps engine.Agent)                              │ │
│ │   - Prompt(text) → Run() + Subscribe events            │ │
│ │   - Steer / FollowUp / Abort                           │ │
│ │   - History / Reset                                    │ │
│ │ └─────────────────────────────────────────────────────┘ │
│              │                                              │
│              ▼                                              │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ engine.Agent (stateful wrapper)                         │ │
│ │   - mu sync.RWMutex, state AgentState, cancel func      │ │
│ │   - AppendInterruptedToolResults on Abort()             │ │
│ │   - Steering / FollowUp / Subscribe                    │ │
│ │ └─────────────────────────────────────────────────────┘ │
│              │                                              │
│              ▼                                              │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ engine.AgentLoopConfig                                  │ │
│ │   - Model / SystemPrompt / Tools                        │ │
│ │   - BeforeToolCall / AfterToolCall                      │ │
│ │   - StreamFn OR ProviderStreamFn                        │ │
│ │   - Compaction (token counter + compactor)              │ │
│ │ └─────────────────────────────────────────────────────┘ │
│              │                                              │
│              ▼                                              │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ crux-ai (Provider registry + 12+ adapters)              │ │
│ │   - Anthropic / OpenAI / Google / Mistral / Bedrock     │ │
│ │   - DeepSeek / GLM / Kimi / Xiaomi / Ollama             │ │
│ │   - Faux (testing)                                      │ │
│ └─────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## 关键代码片段

### Agent 封装

```go
type Agent struct {
    harness *engine.Agent
    mu      sync.Mutex
    running bool
}

func (a *Agent) Prompt(ctx context.Context, text string) ([]core.Message, error) {
    a.mu.Lock()
    if a.running { /* refuse */ }
    a.running = true
    a.mu.Unlock()

    defer func() { a.mu.Lock(); a.running = false; a.mu.Unlock() }()

    userMsg := core.UserMessage{
        Role: core.MessageRoleUser, Content: text, Timestamp: time.Now(),
    }
    return a.harness.Run(ctx, userMsg)
}
```

### Tool 定义

```go
var weatherSchema = json.RawMessage(`{
    "type": "object",
    "properties": {"city": {"type": "string"}},
    "required": ["city"]
}`)

tool := engine.AgentTool{
    Name:        "get_weather",
    Description: "Get current weather for a city",
    Parameters:  weatherSchema,
    Execute: func(ctx context.Context, id string, args json.RawMessage, onUpdate func(json.RawMessage)) (engine.AgentToolResult, error) {
        // ...执行工具
        return engine.AgentToolResult{
            Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "..."}},
        }, nil
    },
}
```

### 事件订阅

```go
printer := NewEventPrinter(true, false)
agent.Subscribe(printer.OnEvent)

func (p *EventPrinter) OnEvent(evt engine.AgentEvent) {
    switch e := evt.(type) {
    case engine.EventMessageUpdate:
        if d, ok := e.AssistantEvent.(core.EventTextDelta); ok {
            fmt.Print(d.Delta)  // 流式输出
        }
    case engine.EventToolExecStart:
        fmt.Fprintf(os.Stderr, "→ tool %s(%s)\n", e.ToolName, e.Args)
    // ...更多事件
    }
}
```

### 两层事件模式（ProviderStreamFn）

```go
// 在 agent-engine/engine/types.go 已经定义好
type ProviderStreamFn func(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.ProviderEventStream, error)

// 在配置时使用
config := engine.AgentLoopConfig{
    ProviderStreamFn: func(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.ProviderEventStream, error) {
        ps := core.NewProviderEventStream()
        go func() {
            ps.Push(core.ProviderResponseStart{Model: model.ID, Timestamp: time.Now()})
            for chunk := range parseSSE(httpResp) {
                switch chunk.Type {
                case "text":
                    ps.Push(core.ProviderTextDelta{Delta: chunk.Data})
                case "thinking":
                    ps.Push(core.ProviderThinkingDelta{Delta: chunk.Data})
                case "tool_call":
                    ps.Push(core.ProviderToolCall{ID: chunk.ID, Name: chunk.Name, Arguments: chunk.Args})
                }
            }
            ps.Push(core.ProviderResponseEnd{Message: finalMsg, FinishReason: "stop"})
        }()
        return ps, nil
    },
}
// engine 内部自动调用 core.CanonicalizeProviderStream 桥接到 AssistantMessageEvent
```

## 测试

```bash
go test -v ./cmd/demo-agent/ -count=1
```

测试覆盖：
- `TestAgent_Abort_CompletesInterruptedToolCalls` — Abort 后自动补齐 tool result
- `TestEngine_PipelineStages` — Pipeline Stage 完整实现
- `TestProviderStreamFn_TwoLayer` — ProviderEvent → AssistantMessageEvent 桥接
- `TestMessageText` — text 提取
- `TestAnthropicProviderRegistered` — provider 注册

## 与 tau_agent/harness.py 对比

| tau_agent/harness.py | demo-agent |
|----------------------|------------|
| `AgentHarness` | `Agent` (cmd/demo-agent/main.go) |
| `prompt(content)` | `Prompt(text)` |
| `steer(content)` | `Steer(text)` |
| `follow_up(content)` | `FollowUp(text)` |
| `cancel()` | `Abort()` |
| `subscribe(listener)` | `Subscribe(fn)` |
| `_append_interrupted_tool_results()` | `engine.Agent.Abort()` 自动调用 `appendInterruptedToolResults` |
| `messages` property | `History()` |

核心逻辑（自动补齐、订阅、steering、follow-up）完全对应，
实现语言差异由 agent-engine/engine 抽象层抹平。