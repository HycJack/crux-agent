# AGENTS.md — crux-ai 项目结构与开发约定

> 给 AI 编码代理（和接手维护者）的项目指南。读完这一份就能掌握代码组织、模块边界、命名约定和常见坑。

## 1. 项目定位

**crux-ai** 是一个 Go 语言的多 LLM 提供商统一抽象层。

- **模块路径**：`crux-ai`（[go.mod](go.mod)）
- **Go 版本**：1.23+
- **Go 外部依赖**：`santhosh-tekuri/jsonschema/v6`、`golang.org/x/text`（均为间接依赖）
- **状态**：实验阶段（`Version = "v0.0.1"`）

---

## 2. 顶层目录布局

```
crux-ai/
├── core/            # 核心类型 + 错误 + 事件流 + 注册表（零外部依赖）
├── ai/              # 公共 API：Stream / Complete / 模型注册
├── providers/       # 12 个 LLM provider 实现 + 路由注册
├── internal/        # 9 个内部工具包（不导出）
├── testenv/         # 测试环境辅助（加载 .env.test）
├── cmd/             # CLI 工具（crux-ai）
└── cruxai.go        # Facade 包：把 core/ai/providers 的导出符号聚合成单一入口
```

> ⚠️ 旧文档提到 `llm/` 和 `router/` 包 —— **这些包不存在**，请使用 `ai/` 和 `providers/`。任何引用 `github.com/hycjack/crux-ai/llm` 或 `…/router` 的导入都已失效。

### 2.1 包依赖图（单向、无环）

```
                     ┌──────────────┐
                     │   providers/ │──┐
                     └──────┬───────┘  │
                            │          │
              ┌─────────────┼──────────┤
              ▼             ▼          ▼
        ┌──────────┐   ┌──────────┐  ┌──────────┐
        │  ai/     │←──│  core/   │  │ internal │
        └────┬─────┘   └────┬─────┘  └────┬─────┘
             │              │             │
             └──────────────┴─────────────┘
                            │
                     ┌──────▼───────┐
                     │   cruxai.go  │
                     └──────────────┘
```

**关键不变量**：
- `core/` 不依赖任何项目内部包（仅依赖标准库）。
- `ai/` 依赖 `core/`。
- `providers/*` 依赖 `core/`、`internal/`、`providers/openai/convert` 等。
- `internal/` 不导出，包外不可导入。
- `cruxai.go` 是一个 Facade，依赖 `ai/`、`core/`，不依赖 `providers/`（用户需自行 `_ "crux-ai/providers"`）。

---

## 3. `core/` — 核心类型

| 文件 | 职责 |
|------|------|
| `types.go` | `KnownAPI`、`KnownProvider`、`Model`、`Message` 接口族（`UserMessage` / `AssistantMessage` / `ToolResultMessage`）、`Context`、`StreamOptions`、`SimpleStreamOptions`、`NewTimeoutClient` |
| `errors.go` | 哨兵错误（`ErrOverflow`/`ErrAuth`/...）+ 类型化错误（`*OverflowError`/`*AuthError`/...）+ `ClassifyHTTPError` |
| `events.go` | `EventStream[T,R]`、`AssistantMessageEvent` 联合接口、`EventStart`/`EventTextDelta`/... |
| `registry.go` | `APIProvider` 接口、`RegisterProvider` / `GetProvider` / images 镜像 |
| `retry.go` | 自动重试（`RetryConfig` + `Run`）；typed-sentinel 判断 + `IsAuthError` / `IsContextOverflow` |
| `timeouts.go` | 流超时中间件（`TimeoutError{Kind, Timeout}`）+ Race 抽象 |
| `overflow.go` | 上下文溢出文本检测（`IsContextOverflowError(string)`） |
| `abort.go` | `AbortSignal` 组合 |
| `compaction.go` | 压缩配置（**当前未在生产代码使用**） |
| `headers.go` | `ProviderHeaders` + `MergeProviderHeaders`（**当前仅自测覆盖**） |
| `provider_env.go` | `ProviderEnv` + `GetProviderEnvValue`（**当前未在生产代码使用**） |
| `env.go` | `GetEnvAPIKey` / `ResolveAPIKey` / `FindEnvKeys` |
| `diagnostics.go` | `Diagnostic` 增强版（含 `DiagnosticErrorInfo`） |
| `prompt_cache.go` | 缓存断点辅助 |
| `clamp.go` | token 数裁剪 |
| `httpclient.go` | `SSEClient` 共享实例 |
| `transform.go` | 跨模型消息变换（图像降级、thinking 截断、tool_call_id 归一化） |

### 3.1 核心类型速查

```go
// MessageRole — 类型安全的角色
type MessageRole string
const (
    MessageRoleUser      MessageRole = "user"
    MessageRoleAssistant MessageRole = "assistant"
    MessageRoleTool      MessageRole = "tool"
    MessageRoleSystem    MessageRole = "system"
)

// Message — 接口，可序列化为 user / assistant / toolResult
type Message interface {
    messageTag()
    GetTimestamp() time.Time
}

// Context — 一次完整调用的输入
type Context struct {
    SystemPrompt string
    Messages     []Message
    Tools        []Tool
}

// StreamOptions — 运行时选项（含 Signal / OnPayload / Retry 等元数据）
type StreamOptions struct {
    Temperature *float64
    MaxTokens   *int
    Signal      <-chan struct{}
    APIKey      string
    // ... (TimeoutMs, MaxRetries, Headers, OnPayload, OnResponse, ...)
}
```

> 💡 **`Context` 与 `ChatRequest`**：`core.ChatRequest = core.Context` 别名 + `NewChatRequest(msgs, tools...)` 构造函数。生产代码普遍直接使用 `core.Context{Messages: ...}`，别名/构造函数目前无外部调用者（见第 6 节「死代码」）。

### 3.2 错误体系

哨兵 + 类型化双层结构。`errors.Is` 走哨兵；`errors.As` 走类型化。

| 哨兵 | 类型化 | 触发场景 |
|------|--------|---------|
| `ErrOverflow` | `*OverflowError{Provider, Message, ContextWindow, Usage}` | 上下文窗口溢出（410/413） |
| `ErrAuth` | `*AuthError{Provider, Cause}` | 401/403 |
| `ErrRateLimit` | `*RateLimitError{Provider, RetryAfter, Cause}` | 408/429 |
| `ErrServer` | `*ServerError{Provider, StatusCode, Cause}` | 5xx |
| `ErrNetwork` | `*NetworkError{Provider, Cause}` | DNS / connect / EOF |
| `ErrTimeout` | `*HTTPTimeoutError{Source, Duration, Provider, ToolName, Cause}` | `context.DeadlineExceeded` 包装 |
| `ErrAborted` | `*AbortError{Cause}` / `*CompactionCancelledError{Reason}` | 取消 |

**重要命名冲突修复**：旧的 `TimeoutError` 与 `timeouts.go` 中流级超时 `TimeoutError{Kind, Timeout}` 同名不同型，合并时统一改名为 **`HTTPTimeoutError`**（包级别 `context.DeadlineExceeded` 包装）。两者职责互不重叠。

### 3.3 注册表

```go
// 注册（仅一次，启动时由 providers/register.go 自动调用）
core.RegisterProvider(core.APIOpenAICompletions, router, "builtin")

// 查找（每个请求一次）
p, err := core.GetProvider(model.API)
```

注册器按来源（`sourceID`）跟踪，便于插件热卸载。镜像注册器独立维护：`RegisterImagesProvider` / `GetImagesProvider`。

---

## 4. `ai/` — 公共 API 层

唯一对外暴露的"业务级"调用层。所有 `ai.*` 函数都是 `core.GetProvider → provider.Stream → EventStream` 的薄壳。

| 文件 | 导出符号 |
|------|---------|
| `api.go` | `Stream` / `Complete` / `StreamSimple` / `StreamSimpleWithContext` / `CompleteSimple` / `GenerateImages` |
| `models.go` | `LoadModels` / `GetModel` / `GetModels` / `GetProviders` / `GetSupportedThinkingLevels` / `ClampThinkingLevel` / `ModelsAreEqual` |
| `models_generated.go` | 编译期生成的 `LoadModels(...)` 注入（所有 model 元数据） |

**惯用法**：

```go
// 1. 启动时：自动注册 provider（一次性）
import _ "crux-ai/providers"

// 2. 拿到模型
m, _ := ai.GetModel(core.ProviderOpenAI, "gpt-4o")

// 3. 调用
stream, _ := ai.Stream(ctx, m, msgs, core.StreamOptions{APIKey: "..."})
result, _ := stream.Result()  // 阻塞到流结束
```

---

## 5. `providers/` — Provider 实现

### 5.1 目录

| 路径 | KnownAPI | 备注 |
|------|----------|------|
| `openai/` | `APIOpenAICompletions` / `APIOpenAIResponses` / `APIAzureOpenAIResponses` / `APIOpenAICodexResponses` | 四个 API + `convert` 工具包 |
| `anthropic/` | `APIAnthropicMessages` | 原生 Claude Messages API |
| `google/` | `APIGoogleGenerative` / `APIGoogleVertex` | Gemini + Vertex |
| `bedrock/` | `APIBedrockConverse` | AWS Bedrock Converse 流 |
| `mistral/` | `APIMistralConversations` | 原生 Mistral Conversations |
| `deepseek/` | `APIOpenAICompletions`（router） | OpenAI 协议，router 分发 |
| `kimi/` | `APIOpenAICompletions`（router） | OpenAI 协议，router 分发 |
| `xiaomi/` | `APIOpenAICompletions`（router） | OpenAI 协议，router 分发 |
| `glm/` | `APIOpenAICompletions`（router） | OpenAI 协议，router 分发 |
| `faux/` | `FauxAPI` | Mock provider，仅测试 |
| `images/openrouter.go` | `OpenRouterImagesAPI` | 图像生成 |
| `compat/` | （辅助） | OpenAI-协议通用引擎，被 router 引用 |
| `register.go` | — | `init()` 中注册所有 builtin provider |

### 5.2 两种实现风格

**A. 原生实现**（Anthropic / Google / Mistral / Bedrock / OpenAI 各自 API）

每个 provider 实现 `core.APIProvider` 接口（`Stream` + `StreamSimple`）。自行处理 SSE / header / body 序列化。

**B. OpenAI-协议通用引擎**（DeepSeek / Kimi / Xiaomi / GLM / OpenAI 直接）

通过 `providers/compat` 的 `compat.Router` 注册：

```go
// providers/register.go
openaiCompat := compat.NewRouter().
    WithConfig(openai.NewCompat()).  // OpenAI 直接
    WithConfig(xiaomi.New()).
    WithConfig(glm.New()).
    WithConfig(deepseek.New()).
    WithConfig(kimi.New())
core.RegisterProvider(core.APIOpenAICompletions, openaiCompat, "builtin")
```

Router 内部按 `model.Provider` 字段查找配置，统一走 `compat.Config{DefaultBaseURL, Path, ExtraHeaders, BuildBody, FinalizeResponse}`。

### 5.3 compat.Config 字段

| 字段 | 用途 |
|------|------|
| `Provider` | 标识（router 查找 key） |
| `DefaultBaseURL` | 模型未指定 BaseURL 时使用 |
| `Path` | 默认 `/chat/completions` |
| `ExtraHeaders` | 每次请求附加 header |
| `BuildBody` | 定制 body（DeepSeek 用它抑制 reasoning 模型的 tool_choice） |
| `FinalizeResponse` | 后处理响应（如注入自定义字段） |

### 5.4 Provider 接口契约

```go
type APIProvider interface {
    Stream(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) (*AssistantMessageEventStream, error)
    StreamSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) (*AssistantMessageEventStream, error)
}
```

**必须满足**：
- ✅ **并发安全**：同一 provider 实例可被多个 goroutine 同时调用。
- ✅ **同步返回 stream**：错误在 `Stream` 返回值中给出（如 `GetProvider` 失败）；流内错误通过 `EventError` / `Result()` 错误返回。
- ✅ **context 取消语义**：通过 `opts.Signal` / `ctx` 触发时，须立即停止请求并 `Error()` 流。

---

## 6. 当前**死代码 / 未使用导出**清单

> 编译通过但运行时无外部调用者的导出符号。建议作为未来清理候选。

### 6.1 `core/` 未使用

| 符号 | 现状 |
|------|------|
| `Race` / `RaceConfig` / `RaceResult` / `RaceKind` / `NewRace` | 流竞态中间件，仅自测 |
| `CombinedAbort` | 组合 abort signal，无外部用法 |
| `CompactionConfig` / `DefaultCompactionConfig` / `CompactionRequest` / `CompactionResult` / `CompactionStatus` / `Compactor` | 压缩抽象，**整个 compaction.go 未被引用** |
| `StreamTimeoutConfig` / `StreamWithTimeout` / `NewEventStreamWithTimeout` | 超时中间件，仅自测 |
| `AgentTool` / `AgentToolResult` / `ToolExecutionMode` / `ToolExecuteFunc` | agent 工具契约（上一轮合并新增），未接入 |
| `NewChatRequest` / `ChatRequest` 别名 | `Context` 直接使用更普遍 |
| `ProviderHeaders` / `ProviderHeadersToRecord` / `MergeProviderHeaders` | 仅 `headers_test.go` 使用 |
| `ProviderEnv` / `GetProviderEnvValue` / `HasProviderEnvValue` | sandbox 兜底，未调用 |
| `PromptCache` 相关 | 未引用 |

### 6.2 `providers/openai/`

| 符号 | 现状 |
|------|------|
| `NewCompletions()` | 原生 OpenAI Chat Completions。**经 register.go 切换到 compat router 后已无注册**；保留为兼容性死代码。 |
| `CompletionsProvider` 类型 | 同上，无引用 |

### 6.3 后续动作建议（**仅建议，不在本轮执行**）

1. 删除 6.1 节列出的死代码（保留 `ChatRequest` 别名作为可选 API）。
2. 决定 `NewCompletions()` 命运：要么删除、要么切回原生（性能更优但失去 router 分发能力）。
3. 把 `AgentTool` 系列接入实际 agent 循环或下移 `internal/`。

---

## 7. 已知优化点 / 可复用模式

### 7.1 Provider 内重复样板

每个原生 provider（openai/responses.go、anthropic.go、bedrock.go、google/*.go、mistral.go 等）都重复以下样板：

```go
client := core.NewTimeoutClient(opts.TimeoutMs)
apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)
// ... 然后构造 http.NewRequestWithContext、设置 Content-Type、Authorization、合并 model.Headers + opts.Headers + cfg.ExtraHeaders
```

**建议**：抽 `core.NewProviderRequest(ctx, method, url, body, provider, model, opts, cfg)` 统一负责 timeout / auth / header 合并 / 错误包装。当前在 8 个文件中重复。

### 7.2 compat 引擎的硬编码 5 分钟

`providers/compat/compat.go:203` 写死 `core.WrapHTTPTimeout(model.Provider, 5*time.Minute, err)`。应改为使用 `opts.TimeoutMs`（已有）。

### 7.3 跨 provider 共享的 SSE 处理

虽然 `compat.compat.go` 的 `processSSE` 较为通用，但 Anthropic / Bedrock / Mistral 各自维护独立的 SSE 处理循环。可考虑把通用部分抽到 `internal/sse` 并加 per-provider 钩子。

### 7.4 `Compat` 结构体（core.Compat）

模型上的 `Compat.SupportsStore` / `SupportsReasoningEffort` / `RequiresToolResultName` 等字段定义已存在，但**没有任何 provider 实际读取**。可在 Phase 2 中真正使用以启用 OpenAI 兼容 provider 的行为切换。

---

## 8. 命名与代码风格约定

- **包名**：单数、短、小写；不要带下划线（`sse` ✓，`jsonparse` ✓）。
- **接口位置**：被消费方定义（`APIProvider` 定义在 `core/`，被 `providers/*` 实现）。
- **错误前缀**：类型化错误以名词结尾（`OverflowError` / `AuthError`），函数以动词（`WrapHTTPTimeout` / `ClassifyHTTPError`）。
- **常量**：camelCase 或 PascalCase（`APIOpenAICompletions` / `ProviderOpenAI`）。
- **可选字段**：指针 + `omitempty` json tag，避免空字符串歧义（如 `MaxTokens *int`）。
- **中文注释**：仅在解释 *为什么* 的地方使用；公开 API 注释保持英文。
- **测试文件**：与被测文件同包（`package core`），不用 `_test` 后缀包名；表驱动 + 子测试 (`t.Run`)。

---

## 9. 构建与测试命令

```bash
cd crux-ai
go build ./...              # 全部包构建
go vet ./...                # 静态检查
go test ./core/             # 单元测试
go test ./providers/openai/ # 某个 provider 的测试
go test ./... -count=1      # 全量测试
```

CLI：

```bash
go run ./cmd/crux-ai providers          # 列出已注册 provider
go run ./cmd/crux-ai models             # 列出已加载模型
go run ./cmd/crux-ai complete openai gpt-4o "Hello"
```

---

## 10. 添加新 Provider 速查

1. **OpenAI 协议**：在 `providers/<name>/` 下写 30 行 `New() compat.Config`；在 `providers/register.go` 加一行 `WithConfig(<name>.New())`。
2. **新协议**：在 `providers/<name>/` 实现 `core.APIProvider`（`Stream` + `StreamSimple`），分配新的 `core.KnownAPI` 常量，在 `core/types.go` 加常量，在 `providers/register.go` 注册。
3. 添加模型：编辑 `ai/models_generated.go` 或源数据生成脚本（推荐）。
4. 添加测试：表驱动单测 + 集成测试（参考 `providers/mistral/mistral_test.go`）。

---

## 11. 不要做的事

- ❌ 不要导入 `github.com/hycjack/crux-ai/llm` 或 `…/router` —— 这些路径不存在。
- ❌ 不要在 `core/` 内引用 `ai/` / `providers/` / `internal/`（保持零依赖）。
- ❌ 不要在 `provider` 包间互相导入（除 `providers/openai/convert` 这种纯工具子包）。
- ❌ 不要导出 `internal/` 内任何符号。
- ❌ 不要把 `Compat` 字段当摆设（要么删除、要么接逻辑）。