# Providers - AI 提供者实现层

本目录实现了多种 LLM 提供者的 API 适配器，是 pi-ai-go 架构中的**提供者层**。

## 架构定位

```
core/ ← 零依赖 (类型 + EventStream + 注册表)
  ↑
ai/   ← 仅依赖 core (公开 API + Model 注册表)
  ↑
providers/ ← 仅依赖 core (LLM 实现)  ← 当前目录
  ↑
agent/ ← 依赖 core + ai (多轮循环 + 工具执行)
```

提供者层**仅依赖 core 层**，实现了 `core.APIProvider` 接口，将不同 LLM 提供者的 API 统一为标准的事件流输出。

## 目录结构

```
providers/
├── register.go           # 内置提供者注册入口
├── anthropic/            # Anthropic Messages API
├── openai/               # OpenAI 系列 API
│   ├── completions.go    # OpenAI Completions API
│   ├── responses.go      # OpenAI Responses API (新格式)
│   ├── codex.go          # OpenAI Codex API
│   ├── azure.go          # Azure OpenAI API
│   ├── shared.go         # 共享工具函数
│   └── convert/          # 消息转换工具
├── google/               # Google AI 系列
│   ├── google.go         # Google Generative AI
│   ├── vertex.go         # Google Vertex AI
│   └── shared.go         # 共享转换逻辑
├── mistral/              # Mistral Conversations API
├── bedrock/              # AWS Bedrock Converse API
├── compat/               # OpenAI 兼容层（路由器）
├── deepseek/             # DeepSeek (通过 compat)
├── glm/                  # 智谱 GLM (通过 compat)
├── kimi/                 # Kimi (通过 compat)
├── xiaomi/               # 小米 AI (通过 compat)
├── openrouter/           # OpenRouter (图像生成)
└── images/               # 图像生成提供者
```

## API 协议对比

| 提供者 | API 协议 | 端点格式 | 思考支持 | 特殊要求 |
|--------|----------|----------|----------|----------|
| **Anthropic** | `anthropic-messages` | `/v1/messages` | ✅ thinking + signature | `anthropic-version` 头 |
| **OpenAI Completions** | `openai-completions` | `/v1/chat/completions` | ❌ | 标准 OpenAI 格式 |
| **OpenAI Responses** | `openai-responses` | `/v1/responses` | ✅ reasoning_effort | 新格式，input/output |
| **OpenAI Codex** | `openai-codex-responses` | `/v1/responses` | ✅ reasoning_effort | 继承 Responses |
| **Azure OpenAI** | `azure-openai-responses` | 自定义 | ✅ | Azure 认证 |
| **Google AI** | `google-generative` | `/v1beta/models/{model}:streamGenerateContent` | ✅ thinkingConfig | contents/parts 结构 |
| **Google Vertex** | `google-vertex` | Vertex AI 端点 | ✅ | Google Cloud 认证 |
| **Mistral** | `mistral-conversations` | `/v1/chat/completions` | ✅ prompt_mode="reasoning" | 工具调用 ID 必须 9 字符 |
| **AWS Bedrock** | `bedrock-converse-stream` | AWS SDK | ✅ | IAM 认证 |
| **DeepSeek** | `openai-completions` (compat) | `/v1/chat/completions` | ✅ reasoning_content | 推理模型禁用 tool_choice |
| **GLM/Kimi/小米** | `openai-completions` (compat) | 各自端点 | 部分支持 | OpenAI 兼容格式 |

## 提供者实现差异

### 1. Anthropic（原生实现）

**特点**：
- 使用原生 Anthropic Messages API
- 支持 Extended Thinking（`thinking` 块 + `signature`）
- 支持 interleaved thinking（交替思考）
- 独特的消息格式（`content` 块数组）

**关键实现**：
```go
// 请求格式
{
  "model": "claude-sonnet-4-20250514",
  "messages": [{"role": "user", "content": [...]}],
  "thinking": {"type": "enabled", "budget_tokens": 10000}
}

// 响应事件类型
content_block_start → content_block_delta → content_block_stop
message_start → message_delta → message_stop
```

**签名机制**：Anthropic 的 thinking 和 text 块都有 `signature` 字段，多轮对话时必须原样传回。

### 2. OpenAI 系列

#### 2.1 Completions API（传统格式）

**特点**：
- 标准 OpenAI Chat Completions 格式
- 工具调用使用 `tool_calls` 数组
- 流式响应使用 `choices[0].delta`

**关键实现**：
```go
// 请求格式
{
  "model": "gpt-4o",
  "messages": [{"role": "user", "content": "..."}],
  "tools": [...]
}

// 响应格式
data: {"choices": [{"delta": {"content": "Hello"}}]}
data: [DONE]
```

#### 2.2 Responses API（新格式）

**特点**：
- OpenAI 新的 Responses API
- 支持 `reasoning.effort` 参数
- 使用 `input` 替代 `messages`
- 工具调用使用 `function_call` 类型

**关键实现**：
```go
// 请求格式
{
  "model": "o1-pro",
  "input": [...],
  "reasoning": {"effort": "high"}
}

// 响应事件类型
response.created → response.output_text.delta → response.completed
response.function_call_arguments.delta
```

#### 2.3 Codex API

**特点**：
- 继承 Responses API 格式
- 额外支持 `text.verbosity` 参数
- 用于 ChatGPT Codex 模型

### 3. Google 系列

**特点**：
- 使用 Google Generative AI / Vertex AI API
- `contents` 数组 + `parts` 结构
- `systemInstruction` 单独字段
- 工具调用使用 `functionCall`

**关键实现**：
```go
// 请求格式
{
  "contents": [{"role": "user", "parts": [{"text": "..."}]}],
  "systemInstruction": {"parts": [{"text": "..."}]},
  "thinkingConfig": {"includeThoughts": true, "thinkingLevel": "HIGH"}
}

// 响应格式
data: {"candidates": [{"content": {"parts": [...]}}]}
```

**思考检测**：通过 `IsThinkingPart()` 函数判断是否为思考内容（检查 `thought` 字段）。

### 4. Mistral

**特点**：
- OpenAI 兼容格式
- 支持 `prompt_mode: "reasoning"` 启用推理
- **工具调用 ID 必须是 9 字符字母数字**

**关键实现**：
```go
// ID 规范化（Mistral 要求）
func normalizeToolCallID(id string) string {
    // 确保返回 9 字符字母数字 ID
}

// 推理配置
{
  "prompt_mode": "reasoning",
  "reasoning_effort": "high"
}
```

### 5. AWS Bedrock

**特点**：
- 使用 AWS SDK 调用 Bedrock Converse API
- 支持 IAM 认证
- 跨区域调用支持

### 6. 兼容层（compat）

**设计目标**：
- 统一处理 OpenAI 兼容的第三方提供者
- 减少重复代码
- 支持提供者特定的定制

**架构**：
```
compat.Router
├── openai.NewCompat()      → api.openai.com
├── deepseek.New()          → api.deepseek.com
├── glm.New()               → open.bigmodel.cn
├── kimi.New()              → api.moonshot.cn
└── xiaomi.New()            → api.xiaomi.com
```

**使用方式**：
```go
// 提供者只需定义 Config
func New() compat.Config {
    return compat.Config{
        Provider:       core.ProviderDeepSeek,
        DefaultBaseURL: "https://api.deepseek.com/v1",
        BuildBody: func(model core.Model, c core.Context, opts core.StreamOptions, body map[string]any) error {
            // 提供者特定的请求定制
            if isReasoningModel(model.ID) {
                delete(body, "tool_choice")  // DeepSeek 推理模型不支持 tool_choice
            }
            return nil
        },
    }
}
```

## 消息转换差异

### 用户消息

| 提供者 | 文本格式 | 图像格式 |
|--------|----------|----------|
| Anthropic | `{"type": "text", "text": "..."}` | `{"type": "image", "source": {"type": "base64", ...}}` |
| OpenAI | `"text"` 或 `[{"type": "text", ...}]` | `{"type": "image_url", "image_url": {"url": "data:..."}}` |
| Google | `{"parts": [{"text": "..."}]}` | `{"parts": [{"inlineData": {"mimeType": "...", "data": "..."}}]}` |

### 工具调用

| 提供者 | 请求格式 | 响应格式 |
|--------|----------|----------|
| Anthropic | `{"type": "tool_use", "name": "...", "input_schema": {...}}` | `{"type": "tool_use", "id": "...", "name": "...", "input": {...}}` |
| OpenAI | `{"type": "function", "function": {"name": "...", "parameters": {...}}}` | `{"tool_calls": [{"id": "...", "function": {"name": "...", "arguments": "..."}}]}` |
| Google | `{"functionDeclarations": [...]}` | `{"functionCall": {"name": "...", "args": {...}}}` |

### 工具结果

| 提供者 | 格式 |
|--------|------|
| Anthropic | `{"type": "tool_result", "tool_use_id": "...", "content": "..."}` |
| OpenAI | `{"role": "tool", "tool_call_id": "...", "content": "..."}` |
| Mistral | `{"role": "tool", "tool_call_id": "..."}` (ID 必须 9 字符) |

## SSE 流处理模式

所有提供者都遵循相同的 SSE 处理模式：

```go
scanner := bufio.NewScanner(body)
scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

for scanner.Scan() {
    line := scanner.Text()
    if !strings.HasPrefix(line, "data: ") { continue }
    data := strings.TrimPrefix(line, "data: ")
    // 解析 JSON，推送事件到 stream
}

// Finalization — 必须在循环外执行
if textBuf.Len() > 0 { /* flush text */ }
msg.Usage.Cost = core.CalculateCost(model, msg.Usage)
stream.Push(core.EventDone{Message: msg})
```

**重要**：Finalization 必须在 scanner 循环外执行，确保连接中断时也能正确结束。

## 注册机制

```go
// register.go
func RegisterBuiltInProviders() {
    // 原生实现
    core.RegisterProvider(core.APIAnthropicMessages, anthropic.New(), "builtin")
    
    // OpenAI 兼容路由器
    openaiCompat := compat.NewRouter().
        WithConfig(openai.NewCompat()).
        WithConfig(deepseek.New()).
        WithConfig(glm.New()).
        WithConfig(kimi.New()).
        WithConfig(xiaomi.New())
    core.RegisterProvider(core.APIOpenAICompletions, openaiCompat, "builtin")
    
    // 其他原生实现
    core.RegisterProvider(core.APIOpenAIResponses, openai.NewResponses(), "builtin")
    core.RegisterProvider(core.APIGoogleGenerative, google.New(), "builtin")
}
```

## 添加新提供者

### 方式一：使用兼容层（推荐）

适用于 OpenAI 兼容的提供者：

```go
// providers/newprovider/newprovider.go
package newprovider

import (
    "pi-ai-go/core"
    "pi-ai-go/providers/compat"
)

func New() compat.Config {
    return compat.Config{
        Provider:       core.ProviderNewProvider,
        DefaultBaseURL: "https://api.newprovider.com/v1",
        ExtraHeaders:   map[string]string{"X-Custom": "value"},
        BuildBody:      func(model core.Model, c core.Context, opts core.StreamOptions, body map[string]any) error {
            // 可选：定制请求体
            return nil
        },
    }
}
```

### 方式二：原生实现

适用于有特殊 API 格式的提供者：

1. 创建 `providers/newprovider/newprovider.go`
2. 实现 `core.APIProvider` 接口
3. 在 `register.go` 中注册

```go
type Provider struct{}

func (p *Provider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
    // 实现
}

func (p *Provider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
    // 实现
}
```

## 环境变量

| 提供者 | 环境变量（按优先级） |
|--------|---------------------|
| Anthropic | `ANTHROPIC_OAUTH_TOKEN`, `ANTHROPIC_API_KEY` |
| OpenAI | `OPENAI_API_KEY` |
| Google | `GOOGLE_API_KEY`, `GEMINI_API_KEY` |
| Mistral | `MISTRAL_API_KEY` |
| DeepSeek | `DEEPSEEK_API_KEY` |
| GLM | `GLM_API_KEY`, `ZAI_API_KEY` |
| Kimi | `KIMI_API_KEY`, `MOONSHOT_API_KEY` |
| 小米 | `XIAOMI_API_KEY`, `MI_API_KEY` |

## 测试

每个提供者都有对应的 `_test.go` 文件，测试覆盖：
- 消息转换正确性
- SSE 解析正确性
- 错误处理
- 边界情况

运行测试：
```bash
go test ./providers/...
```
