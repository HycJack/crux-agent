# crux-ai 项目结构说明

## 项目概述

crux-ai 是一个 Go 语言的 LLM 客户端抽象库，基于 pi-ai-go 迁移并吸收了 crux/crux/crux-ai 的优点。

## 包结构

```
crux-ai/
├── core/           # 核心类型定义（零依赖）
├── llm/            # 公共 API 层
├── providers/      # 12+ Provider 实现
├── internal/       # 8 个内部工具包
├── router/         # 角色路由系统
├── testenv/        # 测试环境
├── cmd/            # CLI 工具
└── cruxai.go       # Facade 包
```

### 依赖关系（单向流动，无循环）

```
core (零依赖) → llm (公共API) → providers (实现)
                 ↓
              router (角色路由)
                 ↓
              internal (工具)
```

---

## `core/` — 核心类型

| 文件 | 职责 |
|------|------|
| `types.go` | KnownAPI、KnownProvider、Message 接口、Model、Cost、Usage |
| `errors.go` | 错误类型和分类 |
| `events.go` | 事件流定义（EventStart, EventTextDelta 等）|
| `registry.go` | Provider 注册表 |
| `retry.go` | 自动重试机制 |
| `httpclient.go` | HTTP 客户端封装 |
| `env.go` | 环境变量解析 |
| `content_unmarshal.go` | ContentBlock 反序列化 |

### 核心类型

**MessageRole** — 消息角色（类型安全，编译时检查）
```go
type MessageRole string
const (
    MessageRoleUser      MessageRole = "user"
    MessageRoleAssistant MessageRole = "assistant"
    MessageRoleTool      MessageRole = "tool"
    MessageRoleSystem    MessageRole = "system"
)
```

**Message** — 消息接口
```go
type Message interface {
    messageTag()
    GetTimestamp() time.Time
}

// 实现：UserMessage, AssistantMessage, ToolResultMessage
```

**ChatRequest** — LLM 请求参数（Context 别名）
```go
type ChatRequest = Context
func NewChatRequest(messages []Message, tools ...[]Tool) ChatRequest
```

**Model** — 模型元数据
```go
type Model struct {
    ID        string
    Name      string
    API       KnownAPI
    Provider  KnownProvider
    BaseURL   string
    Cost      Cost
    // ...
}
```

---

## `llm/` — 公共 API 层

提供简洁的公共 API，封装了 Provider 调用逻辑。

| 文件 | 职责 |
|------|------|
| `api.go` | Stream, Complete, StreamSimple 等公共函数 |
| `models.go` | 模型管理（GetModel, GetModels, GetProviders）|

### 使用示例

```go
import "github.com/hycjack/crux-ai/llm"

// 流式调用
stream, err := llm.Stream(ctx, model, messages, opts)

// 非流式调用
result, err := llm.Complete(ctx, model, messages, opts)

// 简化推理控制
stream, err := llm.StreamSimple(ctx, model, messages, simpleOpts)

// 模型管理
model, err := llm.GetModel(core.ProviderOpenAI, "gpt-4o")
models := llm.GetModels(core.ProviderOpenAI)
providers := llm.GetProviders()
```

---

## `providers/` — Provider 实现

### Provider 接口

```go
type APIProvider interface {
    Stream(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) (*AssistantMessageEventStream, error)
    StreamSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) (*AssistantMessageEventStream, error)
}
```

### 支持的 Provider

| Provider | 包名 | 说明 |
|----------|------|------|
| OpenAI | `providers/openai` | GPT 系列 |
| Anthropic | `providers/anthropic` | Claude 系列 |
| Google | `providers/google` | Gemini 系列 |
| Bedrock | `providers/bedrock` | AWS Bedrock |
| Mistral | `providers/mistral` | Mistral AI |
| DeepSeek | `providers/deepseek` | DeepSeek 系列 |
| Kimi | `providers/kimi` | 月之暗面 Kimi |
| GLM | `providers/glm` | 智谱 GLM |
| Xiaomi | `providers/xiaomi` | 小米 MiMo |
| OpenRouter | `providers/openrouter` | OpenRouter 聚合 |
| Faux | `providers/faux` | Mock Provider |
| Compat | `providers/compat` | 兼容层 |

---

## `router/` — 角色路由系统

根据任务类型自动选择最佳模型。

### 角色类型

```go
type Role string
const (
    RoleDefault Role = "default"  // 通用任务
    RoleSmol    Role = "smol"     // 小快速模型
    RoleSlow    Role = "slow"     // 大高质量模型
    RolePlan    Role = "plan"     // 推理模型
    RoleCommit  Role = "commit"   // 代码编辑
)
```

### 使用示例

```go
import "github.com/hycjack/crux-ai/router"

// 配置角色映射
cfg := router.Config{
    Default: router.ModelSpec{
        ID:       "gpt-4o",
        Provider: core.ProviderOpenAI,
        API:      core.APIOpenAICompletions,
    },
    Smol: router.ModelSpec{
        ID:       "gpt-4o-mini",
        Provider: core.ProviderOpenAI,
        API:      core.APIOpenAICompletions,
    },
    Slow: router.ModelSpec{
        ID:       "claude-opus-4-5",
        Provider: core.ProviderAnthropic,
        API:      core.APIAnthropicMessages,
    },
}

// 创建路由器
r, _ := router.New(cfg)

// 根据角色调用
resp, _ := r.Stream(ctx, router.RoleSmol, messages, core.StreamOptions{})
result, _ := r.Complete(ctx, router.RolePlan, messages, core.StreamOptions{})

// 动态更新配置
r.SetConfig(newCfg)
```

---

## `internal/` — 内部工具包

| 包 | 职责 |
|------|------|
| `sse` | SSE 解析 |
| `jsonparse` | JSON 解析 |
| `validation` | 验证工具 |
| `oauth` | OAuth 认证 |
| `diagnostics` | 诊断工具 |
| `hash` | 哈希计算 |
| `overflow` | 溢出处理 |
| `sanitize` | 输入清理 |

---

## `cruxai.go` — Facade 包

统一入口，重新导出 core 和 llm 的类型：

```go
import "github.com/hycjack/crux-ai/cruxai"

type Message = core.Message
type Model = core.Model
type Provider = core.APIProvider
// ...

const Version = core.Version
```

---

## 关键设计决策

### 1. 消息设计 - 接口 vs 结构体

**选择接口设计** (与 pi-ai-go 一致)：
```go
type Message interface {
    messageTag()
    GetTimestamp() time.Time
}

type UserMessage struct { ... }
type AssistantMessage struct { ... }
type ToolResultMessage struct { ... }
```

**优点**:
- ✅ 类型层次清晰
- ✅ 每种消息有专门字段
- ✅ 编译时类型检查

**权衡**:
- ❌ 序列化需要自定义
- ❌ 需要类型断言

### 2. 角色类型安全

**MessageRole 类型** vs 字符串：
```go
// 类型安全
msg := UserMessage{Role: MessageRoleUser, Content: "hi"}
if msg.Role == MessageRoleUser { ... }  // 编译时检查

// 字符串（已废弃）
msg := UserMessage{Role: "user", Content: "hi"}  // 拼写错误无法发现
```

### 3. Provider 接口

**APIProvider 接口**：
- `Stream` - 流式调用
- `StreamSimple` - 简化推理控制

**设计原则**:
- ✅ Provider 必须是并发安全的
- ✅ 每个调用使用独立的 stream
- ✅ 返回事件流而非简单的 chunk

### 4. 错误处理与重试

**分层错误处理**：
```go
// 1. Provider 层：返回结构化错误
type APIError struct {
    StatusCode int
    Reason     string
    Message    string
    Provider   string
    Model      string
}

// 2. retry.go：自动重试
type RetryConfig struct {
    Enabled    bool
    MaxRetries int
    BaseDelay  time.Duration
    MaxDelay   time.Duration
    Multiplier float64
}

// 3. 命名常量替代魔数
const (
    DefaultBaseDelay = 2 * time.Second
    DefaultMaxDelay = 5 * time.Minute
    DefaultMaxRetries = 3
    DefaultBackoffMultiplier = 2.0
)
```

### 5. 角色路由系统

**设计目的**：
- 根据任务类型自动选择模型
- 配置驱动，易于 A/B 测试
- 减少硬编码模型名称

**ModelSpec 设计**：
```go
type ModelSpec struct {
    ID       string
    Provider core.KnownProvider
    API      core.KnownAPI
}
```

最小化模型引用，与 core.Model 解耦。

---

## 开发指南

### 添加新的 Provider

1. 在 `providers/` 下创建子包
2. 实现 `APIProvider` 接口
3. 在 `providers/register.go` 中注册

详见 [providers/README.md](providers/README.md)（如存在）

### 添加新的内部工具

1. 在 `internal/` 下创建新包
2. 使用 lowercase 包名
3. 保持零外部依赖（或最小依赖）

### 修改核心类型

⚠️ **谨慎修改** — 核心类型的变化会影响所有 Provider

1. 在 `core/types.go` 中修改
2. 更新所有使用该类型的 Provider
3. 运行所有测试验证

---

## 测试

```bash
# 运行所有测试
go test ./...

# 运行特定包的测试
go test ./core/
go test ./llm/
go test ./router/
go test ./providers/openai/

# 运行特定测试
go test ./router/ -run TestRoleRouter_Resolve

# 查看测试覆盖率
go test ./... -cover
```

---

## 依赖管理

### 内部依赖
- `core` 零依赖
- `llm` 依赖 `core`
- `providers/*` 依赖 `core`
- `router` 依赖 `core`
- `internal/*` 零依赖或最小依赖

### 外部依赖
- `github.com/santhosh-tekuri/jsonschema/v6` - JSON Schema 验证

### Go 版本
- Go 1.23.0+

---

## 版本历史

### v0.0.1 (当前)
- 基于 pi-ai-go 迁移
- 合并 ai/ 到 llm/
- 添加 MessageRole 类型安全
- 添加 ChatRequest 别名
- 添加 router/ 角色路由系统
- 命名常量替代魔数
- 完整文档

### 来源
- [pi-ai-go](https://github.com/) - 原始项目
- [crux/crux/crux-ai](file:///mnt/workspace/crux/crux/crux-ai) - 借鉴的优秀设计
