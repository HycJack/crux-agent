# crux-ai 模块设计

## 一、模块概述

`crux-ai` 是 Crux 框架的底层核心模块，提供供应商无关的 AI 客户端抽象。它定义了跨供应商的词汇表，并提供将这些类型转换为实际 HTTP/SSE 流量的适配器。

## 二、核心组件

### 2.1 目录结构

```
crux-ai/
├── core/                      # 核心类型和注册表
│   ├── types.go               # 基础类型定义
│   ├── registry.go            # 供应商注册表
│   ├── env.go                 # 环境变量解析
│   └── events.go              # 事件类型
├── ai/                        # 流式入口点
│   ├── api.go                 # 公共API
│   ├── models.go              # 模型管理
│   └── models_generated.go    # 生成的模型数据
├── providers/                 # 供应商适配器
│   ├── openai/                # OpenAI兼容API
│   ├── anthropic/             # Anthropic
│   ├── google/                # Google/Gemini
│   ├── mistral/               # Mistral
│   ├── bedrock/               # AWS Bedrock
│   ├── faux/                  # 模拟供应商
│   └── register.go            # 注册所有供应商
├── testenv/                   # 测试环境
└── cmd/                       # CLI演示
```

### 2.2 模块职责

| 子包 | 职责 |
|------|------|
| `core` | 定义跨供应商的类型系统和注册表机制 |
| `ai` | 提供高层流式 API 入口点 |
| `providers/<vendor>` | 实现特定供应商的适配器 |
| `testenv` | 提供测试用的模拟环境 |

## 三、类型系统设计

### 3.1 ContentBlock（内容块）

采用接口标签模式实现类型安全的联合类型：

```go
type ContentBlock interface {
    contentTag()  // 标记方法，确保类型安全
}

// 四种内容块类型
type TextContent struct {
    Type          string
    Text          string
    TextSignature string
}

type ThinkingContent struct {
    Type              string
    Thinking          string
    ThinkingSignature string
    Redacted          bool
}

type ImageContent struct {
    Type     string
    Data     string    // base64编码
    MimeType string
}

type ToolCall struct {
    Type             string
    ID               string
    Name             string
    Arguments        json.RawMessage
    ThoughtSignature string
}
```

**设计亮点**：
- 通过 `contentTag()` 方法实现编译期类型检查
- 支持多模态内容的统一处理

### 3.2 Message（消息类型）

```go
type Message interface {
    messageTag()
    GetTimestamp() time.Time
}

// 三种消息角色
type UserMessage struct {
    Role      string
    Content   any           // 可以是 string 或 []ContentBlock
    Timestamp time.Time
}

type AssistantMessage struct {
    Role          string
    Content       []ContentBlock
    API           KnownAPI
    Provider      KnownProvider
    Model         string
    ResponseModel string
    ResponseID    string
    Diagnostics   []Diagnostic
    Usage         Usage
    StopReason    StopReason
    ErrorMessage  string
    Timestamp     time.Time
}

type ToolResultMessage struct {
    Role       string
    ToolCallID string
    ToolName   string
    Content    []ContentBlock
    Details    any
    IsError    bool
    Timestamp  time.Time
}
```

### 3.3 Model（模型定义）

```go
type Model struct {
    ID               string
    Name             string
    API              KnownAPI          // API协议类型
    Provider         KnownProvider     // 供应商标识
    BaseURL          string            // 自定义基础URL
    Reasoning        bool              // 是否支持推理
    ThinkingLevelMap map[string]string // 推理强度映射
    Input            []Modality        // 支持的输入模态
    Cost             Cost              // 定价信息
    ContextWindow    int               // 上下文窗口大小
    MaxTokens        int               // 最大输出token数
    Headers          map[string]string // 自定义请求头
    Compat           *Compat           // 兼容性标志
}

type Compat struct {
    SupportsStore           bool   // 是否支持存储
    SupportsDeveloperRole   bool   // 是否支持developer角色
    SupportsReasoningEffort bool   // 是否支持推理强度控制
    MaxTokensField          string // max_tokens字段名
    RequiresToolResultName  bool   // 是否需要工具结果名称
    RequiresThinkingAsText  bool   // 是否需要将思考作为文本
    ThinkingFormat          string // 思考内容格式
    CacheControlFormat      string // 缓存控制格式
}
```

### 3.4 枚举类型

| 枚举 | 用途 | 值 |
|------|------|----|
| `KnownAPI` | API协议标识 | `openai-completions`, `anthropic-messages`, `bedrock-converse-stream` 等 |
| `KnownProvider` | 供应商标识 | `anthropic`, `openai`, `google`, `mistral` 等 |
| `Modality` | 输入/输出模态 | `text`, `image`, `audio` |
| `ThinkingLevel` | 推理强度 | `minimal`, `low`, `medium`, `high`, `xhigh` |
| `StopReason` | 停止原因 | `stop`, `length`, `toolUse`, `error`, `aborted` |
| `CacheRetention` | 缓存策略 | `none`, `short`, `long` |
| `Transport` | 传输方式 | `sse`, `websocket`, `auto` |

## 四、供应商注册表

### 4.1 设计模式：服务定位器

```go
// 核心接口
type APIProvider interface {
    Stream(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) (*AssistantMessageEventStream, error)
    StreamSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) (*AssistantMessageEventStream, error)
}

// 注册机制
var apiProviders = make(map[KnownAPI]APIProvider)

func RegisterProvider(api KnownAPI, provider APIProvider, sourceID ...string) {
    apiProviders[api] = provider
}

func GetProvider(api KnownAPI) (APIProvider, error) {
    p, ok := apiProviders[api]
    if !ok {
        return nil, fmt.Errorf("no provider registered for API: %s", api)
    }
    return p, nil
}
```

### 4.2 支持的供应商

| 供应商 | API协议 | 支持的特性 |
|--------|----------|------------|
| OpenAI | `openai-completions`, `openai-responses` | 文本、图片、推理 |
| Anthropic | `anthropic-messages` | 文本、图片、推理、缓存 |
| Google | `google-generative`, `google-vertex` | 文本、图片、推理 |
| Mistral | `mistral-conversations` | 文本、图片 |
| Bedrock | `bedrock-converse-stream` | 文本、图片 |
| Azure OpenAI | `azure-openai-responses` | 文本、图片 |
| OpenAI Codex | `openai-codex-responses` | 代码补全 |

## 五、流式 API 设计

### 5.1 高层入口

```go
// 简单流式调用
func Stream(ctx context.Context, model core.Model, msgs []core.Message, opts ...core.StreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error)

// 带推理控制的流式调用
func StreamSimple(ctx context.Context, model core.Model, msgs []core.Message, opts ...core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error)

// 带完整上下文的流式调用
func StreamSimpleWithContext(ctx context.Context, model core.Model, llmCtx core.Context, opts ...core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error)
```

### 5.2 事件类型

```go
type AssistantMessageEvent interface {
    eventTag()
}

// 事件类型枚举
type EventStart struct { ... }              // 流式响应开始
type EventTextStart struct { ... }          // 文本块开始
type EventTextDelta struct { ... }          // 文本增量
type EventTextEnd struct { ... }            // 文本块结束
type EventThinkingStart struct { ... }      // 思考块开始
type EventThinkingDelta struct { ... }      // 思考增量
type EventThinkingEnd struct { ... }        // 思考块结束
type EventToolCallStart struct { ... }      // 工具调用开始
type EventToolCallDelta struct { ... }      // 工具调用参数增量
type EventToolCallEnd struct { ... }        // 工具调用结束
type EventDone struct { ... }               // 完成
type EventError struct { ... }              // 错误
```

### 5.3 EventStream 实现

```go
type EventStream[T any, R any] struct {
    ch     chan streamEvt[T]
    done   chan struct{}
    stop   chan struct{}
    result R
    err    error
    closed bool
    mu     sync.Mutex
}

// 主要方法
func (s *EventStream[T, R]) Push(event T) bool     // 推送事件
func (s *EventStream[T, R]) End(result R)          // 结束流（成功）
func (s *EventStream[T, R]) Error(err error)       // 结束流（错误）
func (s *EventStream[T, R]) Stop()                 // 停止流
func (s *EventStream[T, R]) Result() (R, error)    // 获取结果
func (s *EventStream[T, R]) ForEach(ctx context.Context, fn func(T) error) (R, error) // 遍历事件
```

## 六、环境变量解析

### 6.1 API 密钥解析

```go
var providerEnvVars = map[KnownProvider][]string{
    ProviderAnthropic:     {"ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"},
    ProviderOpenAI:        {"OPENAI_API_KEY"},
    ProviderGoogle:        {"GOOGLE_API_KEY", "GEMINI_API_KEY"},
    ProviderGoogleVertex:  {"GOOGLE_CLOUD_PROJECT"},
    ProviderMistral:       {"MISTRAL_API_KEY"},
    ProviderAzureOpenAI:   {"AZURE_OPENAI_API_KEY"},
    // ... 更多供应商
}

func ResolveAPIKey(provider KnownProvider, optsKey string) string {
    if optsKey != "" {
        return optsKey
    }
    return GetEnvAPIKey(provider)
}
```

### 6.2 基础 URL 解析

```go
func ResolveBaseURL(model Model, defaultURL string) string {
    if model.BaseURL != "" {
        return strings.TrimRight(model.BaseURL, "/")
    }
    return strings.TrimRight(defaultURL, "/")
}
```

## 七、模型管理

### 7.1 模型注册表

```go
var modelsMap = make(map[core.KnownProvider]map[string]core.Model)

func LoadModels(models map[core.KnownProvider]map[string]core.Model)
func GetModel(provider core.KnownProvider, modelID string) (core.Model, error)
func GetModels(provider core.KnownProvider) []core.Model
func GetProviders() []core.KnownProvider
```

### 7.2 推理强度支持

```go
func GetSupportedThinkingLevels(model core.Model) []core.ThinkingLevel
func ClampThinkingLevel(model core.Model, level core.ThinkingLevel) core.ThinkingLevel
```

## 八、供应商适配器实现

### 8.1 OpenAI 适配器

```go
// 消息转换
func ConvertMessages(messages []core.Message, model core.Model) ([]map[string]any, error)

// 工具转换
func ConvertTools(tools []core.Tool) []map[string]any

// 停止原因映射
func MapStopReason(reason string) core.StopReason
```

### 8.2 Anthropic 适配器

特点：
- 支持缓存（CacheRead/CacheWrite）
- 原生推理支持
- 大上下文窗口（200K tokens）

### 8.3 Google 适配器

支持两种 API：
- `google-generative`: 直接 API
- `google-vertex`: Vertex AI

### 8.4 Faux（模拟）供应商

用于测试的模拟供应商，不消耗真实 token：

```go
type FauxProvider struct{}

func (p *FauxProvider) Stream(...) (*core.AssistantMessageEventStream, error) {
    // 返回预定义的响应
}
```

## 九、错误处理

### 9.1 错误类型

| 错误场景 | 处理方式 |
|----------|----------|
| 供应商未注册 | 返回错误 |
| API 密钥缺失 | 返回错误 |
| 网络超时 | 返回错误 |
| 流式中断 | 发送 `EventError` |
| 模型不支持特性 | 降级处理或返回错误 |

### 9.2 重试机制

```go
type StreamOptions struct {
    MaxRetries      int               // 最大重试次数
    MaxRetryDelayMs int               // 最大重试延迟
    TimeoutMs       int               // 请求超时
}
```

## 十、性能优化

### 10.1 HTTP 客户端配置

```go
func NewTimeoutClient(timeoutMs int) *http.Client {
    timeout := 5 * time.Minute
    if timeoutMs > 0 {
        timeout = time.Duration(timeoutMs) * time.Millisecond
    }
    return &http.Client{Timeout: timeout}
}
```

### 10.2 连接池

通过 HTTP 客户端的 `Transport` 配置实现连接复用。

### 10.3 流式处理

- 使用缓冲通道（64）减少阻塞
- 非阻塞发送事件
- 支持上下文取消