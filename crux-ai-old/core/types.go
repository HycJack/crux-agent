/*
 * 功能说明：核心类型定义
 *
 * 解决的问题：
 * 1. 需要统一的类型定义，供所有层级使用
 * 2. 需要定义 API 协议、提供者、消息类型等核心概念
 * 3. 需要零依赖的类型层，确保可以被任何层级安全导入
 *
 * 解决方案：
 * 1. 定义 KnownAPI 枚举标识 API 协议类型
 * 2. 定义 KnownProvider 枚举标识 AI 提供者
 * 3. 定义 Message 接口和具体消息类型（UserMessage、AssistantMessage、ToolResultMessage）
 * 4. 定义 ContentBlock 联合类型支持文本、思考、图像、工具调用
 * 5. 定义 Model 结构描述模型能力和定价
 *
 * 应用场景：
 * - 所有 AI 提供者实现共享这些类型
 * - Agent 层使用这些类型构建对话
 * - 事件流使用这些类型传递消息
 */
// Package core defines the fundamental types shared across pi-ai-go.
// || 定义 pi-ai-go 共享的基础类型
// This package has zero internal dependencies and is safe for any layer to import.
// || 此包零内部依赖，任何层级都可以安全导入
package core

import (
	"context"
	"encoding/json"
	"time"
)

// KnownAPI identifies the API protocol used to communicate with a provider.
// || 标识与提供者通信的 API 协议
type KnownAPI string

// Version is the semantic version of the pi-ai-go core package. It is
// re-exported as piai.Version and consumed by downstream examples and
// integrations that want to print or compare against a known version.
// || pi-ai-go 核心包的语义版本号，被重新导出为 piai.Version
const Version = "v0.0.1"

const (
	APIOpenAICompletions    KnownAPI = "openai-completions"      // OpenAI Completions API
	APIAnthropicMessages    KnownAPI = "anthropic-messages"      // Anthropic Messages API
	APIBedrockConverse      KnownAPI = "bedrock-converse-stream" // AWS Bedrock Converse API
	APIOpenAIResponses      KnownAPI = "openai-responses"        // OpenAI Responses API
	APIAzureOpenAIResponses KnownAPI = "azure-openai-responses"  // Azure OpenAI Responses API
	APIOpenAICodexResponses KnownAPI = "openai-codex-responses"  // OpenAI Codex Responses API
	APIGoogleGenerative     KnownAPI = "google-generative"       // Google Generative AI API
	APIGoogleVertex         KnownAPI = "google-vertex"           // Google Vertex AI API
	APIMistralConversations KnownAPI = "mistral-conversations"   // Mistral Conversations API
	OpenRouter              KnownAPI = "openrouter"              // OpenRouter API
)

// KnownProvider identifies a specific AI provider.
// || 标识特定的 AI 提供者
type KnownProvider string

const (
	ProviderAnthropic     KnownProvider = "anthropic"             // Anthropic
	ProviderOpenAI        KnownProvider = "openai"                // OpenAI
	ProviderAmazonBedrock KnownProvider = "amazon-bedrock"        // AWS Bedrock
	ProviderGoogle        KnownProvider = "google"                // Google AI
	ProviderGoogleVertex  KnownProvider = "google-vertex"         // Google Vertex AI
	ProviderMistral       KnownProvider = "mistral"               // Mistral AI
	ProviderAzureOpenAI   KnownProvider = "azure-openai"          // Azure OpenAI
	ProviderOpenAICodex   KnownProvider = "openai-codex"          // OpenAI Codex
	ProviderGitHubCopilot KnownProvider = "github-copilot"        // GitHub Copilot
	ProviderOpenRouter    KnownProvider = "openrouter"            // OpenRouter
	ProviderFireworks     KnownProvider = "fireworks"             // Fireworks AI
	ProviderTogether      KnownProvider = "together"              // Together AI
	ProviderGroq          KnownProvider = "groq"                  // Groq
	ProviderXAI           KnownProvider = "xai"                   // xAI (Grok)
	ProviderDeepSeek      KnownProvider = "deepseek"              // DeepSeek
	ProviderCerebras      KnownProvider = "cerebras"              // Cerebras
	ProviderCloudflare    KnownProvider = "cloudflare"            // Cloudflare AI
	ProviderHuggingFace   KnownProvider = "huggingface"           // Hugging Face
	ProviderMoonshot      KnownProvider = "moonshotai"            // Moonshot AI
	ProviderMoonshotCN    KnownProvider = "moonshotai-cn"         // Moonshot AI (中国)
	ProviderMinimax       KnownProvider = "minimax"               // MiniMax
	ProviderMinimaxCN     KnownProvider = "minimax-cn"            // MiniMax (中国)
	ProviderXiaomi        KnownProvider = "xiaomi"                // 小米 AI
	ProviderVercelGateway KnownProvider = "vercel-ai-gateway"     // Vercel AI Gateway
	ProviderCloudflareGW  KnownProvider = "cloudflare-ai-gateway" // Cloudflare AI Gateway
	ProviderKimi          KnownProvider = "kimi"                  // Kimi
	ProviderGLM           KnownProvider = "glm"                   // 智谱 GLM
	ProviderZAI           KnownProvider = "zai"                   // ZAI
)

// Modality represents an input/output modality.
// || 表示输入/输出的模态类型
type Modality string

const (
	ModalityText  Modality = "text"  // 文本模态
	ModalityImage Modality = "image" // 图像模态
	ModalityAudio Modality = "audio" // 音频模态
)

// ThinkingLevel represents the depth of reasoning.
// || 表示推理的深度级别
type ThinkingLevel string

const (
	ThinkingMinimal ThinkingLevel = "minimal" // 最小思考
	ThinkingLow     ThinkingLevel = "low"     // 低思考
	ThinkingMedium  ThinkingLevel = "medium"  // 中等思考
	ThinkingHigh    ThinkingLevel = "high"    // 高思考
	ThinkingXHigh   ThinkingLevel = "xhigh"   // 超高思考
)

// StopReason indicates why generation stopped.
// || 表示生成停止的原因
type StopReason string

const (
	StopStop    StopReason = "stop"    // 正常结束
	StopLength  StopReason = "length"  // 达到最大长度
	StopToolUse StopReason = "toolUse" // 工具调用
	StopError   StopReason = "error"   // 错误
	StopAborted StopReason = "aborted" // 中止
)

// CacheRetention controls prompt caching behavior.
// || 控制 prompt 缓存行为
type CacheRetention string

const (
	CacheNone  CacheRetention = "none"  // 不缓存
	CacheShort CacheRetention = "short" // 短期缓存
	CacheLong  CacheRetention = "long"  // 长期缓存
)

// Transport selects the streaming transport.
// || 选择流式传输方式
type Transport string

const (
	TransportSSE       Transport = "sse"       // Server-Sent Events
	TransportWebSocket Transport = "websocket" // WebSocket
	TransportAuto      Transport = "auto"      // 自动选择
)

// Cost represents per-million-token pricing.
// || 表示每百万 token 的定价
type Cost struct {
	Input      float64 `json:"input"`                // 输入 token 价格
	Output     float64 `json:"output"`               // 输出 token 价格
	CacheRead  float64 `json:"cacheRead,omitempty"`  // 缓存读取价格
	CacheWrite float64 `json:"cacheWrite,omitempty"` // 缓存写入价格
}

// Model describes an AI model's capabilities and pricing.
// || 描述 AI 模型的能力和定价
type Model struct {
	ID               string            `json:"id"`                         // 模型 ID
	Name             string            `json:"name"`                       // 模型名称
	API              KnownAPI          `json:"api"`                        // API 协议
	Provider         KnownProvider     `json:"provider"`                   // 提供者
	BaseURL          string            `json:"baseUrl,omitempty"`          // 基础 URL（可选）
	Reasoning        bool              `json:"reasoning,omitempty"`        // 是否支持推理
	ThinkingLevelMap map[string]string `json:"thinkingLevelMap,omitempty"` // 思考级别映射
	Input            []Modality        `json:"input"`                      // 支持的输入模态
	Cost             Cost              `json:"cost"`                       // 定价信息
	ContextWindow    int               `json:"contextWindow"`              // 上下文窗口大小
	MaxTokens        int               `json:"maxTokens"`                  // 最大输出 token 数
	Headers          map[string]string `json:"headers,omitempty"`          // 自定义请求头
	Compat           *Compat           `json:"compat,omitempty"`           // 兼容性配置
}

// Compat holds compatibility flags for OpenAI-compatible APIs.
// || 存储 OpenAI 兼容 API 的兼容性标志
type Compat struct {
	SupportsStore           bool   `json:"supportsStore,omitempty"`           // 是否支持存储
	SupportsDeveloperRole   bool   `json:"supportsDeveloperRole,omitempty"`   // 是否支持 developer 角色
	SupportsReasoningEffort bool   `json:"supportsReasoningEffort,omitempty"` // 是否支持推理努力
	MaxTokensField          string `json:"maxTokensField,omitempty"`          // maxTokens 字段名
	RequiresToolResultName  bool   `json:"requiresToolResultName,omitempty"`  // 是否需要工具结果名称
	RequiresThinkingAsText  bool   `json:"requiresThinkingAsText,omitempty"`  // 是否将思考作为文本
	ThinkingFormat          string `json:"thinkingFormat,omitempty"`          // 思考格式
	CacheControlFormat      string `json:"cacheControlFormat,omitempty"`      // 缓存控制格式
}

// ContentBlock is a union type for message content.
// || 消息内容的联合类型
type ContentBlock interface {
	contentTag()
}

// TextContent represents text in a message.
// || 表示消息中的文本内容
type TextContent struct {
	Type          string `json:"type"`                    // 类型：text
	Text          string `json:"text"`                    // 文本内容
	TextSignature string `json:"textSignature,omitempty"` // 文本签名（用于 Anthropic）
}

func (TextContent) contentTag() {}

// ThinkingContent represents thinking/reasoning content.
// || 表示思考/推理内容
type ThinkingContent struct {
	Type              string `json:"type"`                        // 类型：thinking
	Thinking          string `json:"thinking"`                    // 思考内容
	ThinkingSignature string `json:"thinkingSignature,omitempty"` // 思考签名
	Redacted          bool   `json:"redacted,omitempty"`          // 是否已脱敏
}

func (ThinkingContent) contentTag() {}

// ImageContent represents an image in a message.
// || 表示消息中的图像内容
type ImageContent struct {
	Type     string `json:"type"`     // 类型：image
	Data     string `json:"data"`     // 图像数据（base64）
	MimeType string `json:"mimeType"` // MIME 类型
}

func (ImageContent) contentTag() {}

// ToolCall represents a tool invocation from the model.
// || 表示模型发起的工具调用
type ToolCall struct {
	Type             string          `json:"type"`                       // 类型：tool_use
	ID               string          `json:"id"`                         // 工具调用 ID
	Name             string          `json:"name"`                       // 工具名称
	Arguments        json.RawMessage `json:"arguments"`                  // 工具参数（JSON）
	ThoughtSignature string          `json:"thoughtSignature,omitempty"` // 思考签名
}

func (ToolCall) contentTag() {}

// MessageRole represents the role of a message sender.
// || 消息发送者的角色（类型安全，编译时检查）
type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"      // 用户
	MessageRoleAssistant MessageRole = "assistant"  // 助手
	MessageRoleTool      MessageRole = "tool"       // 工具
	MessageRoleSystem    MessageRole = "system"     // 系统
)

// Message is the interface for all message types.
// || 所有消息类型的接口
type Message interface {
	messageTag()
	GetTimestamp() time.Time
}

// UserMessage represents a user message.
// || 表示用户消息
type UserMessage struct {
	Role      MessageRole `json:"role"`      // 角色：user
	Content   any         `json:"content"`   // 内容（字符串或 ContentBlock 数组）
	Timestamp time.Time   `json:"timestamp"` // 时间戳
}

func (UserMessage) messageTag()               {}
func (m UserMessage) GetTimestamp() time.Time { return m.Timestamp }

// AssistantMessage represents a model response.
// || 表示模型响应消息
type AssistantMessage struct {
	Role          MessageRole    `json:"role"`                    // 角色：assistant
	Content       []ContentBlock `json:"content"`                 // 内容块
	API           KnownAPI       `json:"api"`                     // API 协议
	Provider      KnownProvider  `json:"provider"`                // 提供者
	Model         string         `json:"model"`                   // 模型名称
	ResponseModel string         `json:"responseModel,omitempty"` // 响应模型（可能与请求不同）
	ResponseID    string         `json:"responseId,omitempty"`    // 响应 ID
	Diagnostics   []Diagnostic   `json:"diagnostics,omitempty"`   // 诊断信息
	Usage         Usage          `json:"usage"`                   // Token 使用统计
	StopReason    StopReason     `json:"stopReason"`              // 停止原因
	ErrorMessage  string         `json:"errorMessage,omitempty"`  // 错误消息
	Timestamp     time.Time      `json:"timestamp"`               // 时间戳
}

func (AssistantMessage) messageTag()               {}
func (m AssistantMessage) GetTimestamp() time.Time { return m.Timestamp }

// ToolResultMessage represents a tool execution result.
// || 表示工具执行结果消息
type ToolResultMessage struct {
	Role       MessageRole    `json:"role"`              // 角色：tool
	ToolCallID string         `json:"toolCallId"`        // 对应的工具调用 ID
	ToolName   string         `json:"toolName"`          // 工具名称
	Content    []ContentBlock `json:"content"`           // 结果内容
	Details    any            `json:"details,omitempty"` // 详细信息
	IsError    bool           `json:"isError"`           // 是否错误
	Timestamp  time.Time      `json:"timestamp"`         // 时间戳
}

func (ToolResultMessage) messageTag()               {}
func (m ToolResultMessage) GetTimestamp() time.Time { return m.Timestamp }

// Usage represents token usage statistics.
// || 表示 token 使用统计
type Usage struct {
	Input       int           `json:"input"`                // 输入 token 数
	Output      int           `json:"output"`               // 输出 token 数
	CacheRead   int           `json:"cacheRead,omitempty"`  // 缓存读取 token 数
	CacheWrite  int           `json:"cacheWrite,omitempty"` // 缓存写入 token 数
	TotalTokens int           `json:"totalTokens"`          // 总 token 数
	Cost        CostBreakdown `json:"cost"`                 // 费用明细
}

// CostBreakdown represents the cost of a request.
// || 表示请求的费用明细
type CostBreakdown struct {
	Input      float64 `json:"input"`                // 输入费用
	Output     float64 `json:"output"`               // 输出费用
	CacheRead  float64 `json:"cacheRead,omitempty"`  // 缓存读取费用
	CacheWrite float64 `json:"cacheWrite,omitempty"` // 缓存写入费用
	Total      float64 `json:"total"`                // 总费用
}

// Diagnostic represents a diagnostic event during message processing.
// || 表示消息处理过程中的诊断事件
type Diagnostic struct {
	Type      string    `json:"type"`              // 诊断类型
	Timestamp time.Time `json:"timestamp"`         // 时间戳
	Error     string    `json:"error,omitempty"`   // 错误信息
	Details   any       `json:"details,omitempty"` // 详细信息
}

// Context represents the conversation context for a completion request.
// || 表示补全请求的对话上下文
type Context struct {
	SystemPrompt string    `json:"systemPrompt,omitempty"` // 系统提示
	Messages     []Message `json:"messages"`               // 消息列表
	Tools        []Tool    `json:"tools,omitempty"`        // 工具定义
}

// ChatRequest is an alias for Context, matching crux/crux/crux-ai naming.
// || ChatRequest 是 Context 的别名，与 crux/crux/crux-ai 命名一致
type ChatRequest = Context

// NewChatRequest creates a new ChatRequest (Context) with messages and optional tools.
// || 创建新的 ChatRequest，包含消息和可选工具
func NewChatRequest(messages []Message, tools ...[]Tool) ChatRequest {
	var t []Tool
	if len(tools) > 0 {
		t = tools[0]
	}
	return ChatRequest{Messages: messages, Tools: t}
}

// Tool represents a tool definition.
// || 表示工具定义
type Tool struct {
	Name        string          `json:"name"`                  // 工具名称
	Description string          `json:"description,omitempty"` // 工具描述
	Parameters  json.RawMessage `json:"parameters,omitempty"`  // 参数 JSON Schema
}

// StreamOptions are options for streaming completions.
// || 流式补全的选项
type StreamOptions struct {
	Temperature     *float64          `json:"temperature,omitempty"`     // 温度参数
	MaxTokens       *int              `json:"maxTokens,omitempty"`       // 最大输出 token 数
	Signal          <-chan struct{}   `json:"-"`                         // 取消信号
	APIKey          string            `json:"-"`                         // API Key
	Transport       Transport         `json:"transport,omitempty"`       // 传输方式
	CacheRetention  CacheRetention    `json:"cacheRetention,omitempty"`  // 缓存策略
	SessionID       string            `json:"sessionId,omitempty"`       // 会话 ID
	OnPayload       func(any)         `json:"-"`                         // 请求负载回调
	OnResponse      func(any)         `json:"-"`                         // 响应回调
	Headers         map[string]string `json:"-"`                         // 自定义请求头
	TimeoutMs       int               `json:"timeoutMs,omitempty"`       // 超时时间（毫秒）
	MaxRetries      int               `json:"maxRetries,omitempty"`      // 最大重试次数
	MaxRetryDelayMs int               `json:"maxRetryDelayMs,omitempty"` // 最大重试延迟（毫秒）
	Metadata        map[string]any    `json:"metadata,omitempty"`        // 元数据
}

// SimpleStreamOptions extends StreamOptions with unified reasoning controls.
// || 扩展 StreamOptions，添加统一的推理控制
type SimpleStreamOptions struct {
	StreamOptions
	Reasoning       ThinkingLevel  `json:"reasoning,omitempty"`       // 推理级别
	ThinkingBudgets map[string]int `json:"thinkingBudgets,omitempty"` // 思考预算
}

// --- Image types ---
// || --- 图像类型 ---

// ImagesModel describes an image generation model.
// || 描述图像生成模型
type ImagesModel struct {
	ID       string            `json:"id"`                // 模型 ID
	Name     string            `json:"name"`              // 模型名称
	API      KnownAPI          `json:"api"`               // API 协议
	Provider KnownProvider     `json:"provider"`          // 提供者
	BaseURL  string            `json:"baseUrl,omitempty"` // 基础 URL
	Input    []Modality        `json:"input"`             // 输入模态
	Output   []Modality        `json:"output"`            // 输出模态
	Cost     Cost              `json:"cost"`              // 定价
	Headers  map[string]string `json:"headers,omitempty"` // 自定义请求头
}

// AssistantImages represents the result of image generation.
// || 表示图像生成的结果
type AssistantImages struct {
	API          KnownAPI      `json:"api"`                    // API 协议
	Provider     KnownProvider `json:"provider"`               // 提供者
	Model        string        `json:"model"`                  // 模型名称
	Output       []ImageData   `json:"output"`                 // 输出图像
	ResponseID   string        `json:"responseId,omitempty"`   // 响应 ID
	Usage        *Usage        `json:"usage,omitempty"`        // Token 使用统计
	StopReason   StopReason    `json:"stopReason"`             // 停止原因
	ErrorMessage string        `json:"errorMessage,omitempty"` // 错误消息
	Timestamp    time.Time     `json:"timestamp"`              // 时间戳
}

// ImageData represents a generated image.
// || 表示生成的图像
type ImageData struct {
	Data     string `json:"data"`     // 图像数据（base64 或 URL）
	MimeType string `json:"mimeType"` // MIME 类型
}

// ImageOptions are options for image generation.
// || 图像生成的选项
type ImageOptions struct {
	APIKey  string            `json:"-"` // API Key
	Headers map[string]string `json:"-"` // 自定义请求头
	Signal  <-chan struct{}   `json:"-"` // 取消信号
}

// --- Agent Tool Contract Types ---
// These types define the shared contract between agent loops and tool
// implementations. They live in core so that tool packages can import
// them without depending on the agent package.

// ToolExecutionMode controls how tools are executed.
// || 控制工具执行模式
type ToolExecutionMode string

const (
	ToolExecParallel   ToolExecutionMode = "parallel"   // 并行执行
	ToolExecSequential ToolExecutionMode = "sequential" // 顺序执行
)

// AgentTool defines a tool that the agent can call.
// || 定义 Agent 可调用的工具
type AgentTool struct {
	Name          string            `json:"name"`                    // 工具名称
	Description   string            `json:"description,omitempty"`   // 工具描述
	Parameters    json.RawMessage   `json:"parameters,omitempty"`    // 参数 JSON Schema
	Label         string            `json:"label,omitempty"`         // 显示标签
	Execute       ToolExecuteFunc   `json:"-"`                       // 执行函数
	ExecutionMode ToolExecutionMode `json:"executionMode,omitempty"` // 执行模式（空=继承配置）
}

// ToolExecuteFunc is the function signature for tool execution.
// || 工具执行函数签名
type ToolExecuteFunc func(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (AgentToolResult, error)

// AgentToolResult is the result of a tool execution.
// || 工具执行结果
type AgentToolResult struct {
	Content   []ContentBlock    `json:"content,omitempty"`   // 结果内容
	Details   json.RawMessage   `json:"details,omitempty"`   // 详细信息
	IsError   bool              `json:"isError,omitempty"`   // 是否错误
	Terminate bool              `json:"terminate,omitempty"` // 是否终止 Agent
}
