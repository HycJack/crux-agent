// Package core defines the fundamental types shared across crux-ai.
// This package has zero internal dependencies and is safe for any layer to import.
package core

import (
	"encoding/json"
	"net/http"
	"time"
)

// KnownAPI identifies the API protocol used to communicate with a provider.
type KnownAPI string

const (
	APIOpenAICompletions    KnownAPI = "openai-completions"
	APIAnthropicMessages    KnownAPI = "anthropic-messages"
	APIBedrockConverse      KnownAPI = "bedrock-converse-stream"
	APIOpenAIResponses      KnownAPI = "openai-responses"
	APIAzureOpenAIResponses KnownAPI = "azure-openai-responses"
	APIOpenAICodexResponses KnownAPI = "openai-codex-responses"
	APIGoogleGenerative     KnownAPI = "google-generative"
	APIGoogleVertex         KnownAPI = "google-vertex"
	APIMistralConversations KnownAPI = "mistral-conversations"
)

// KnownProvider identifies a specific AI provider.
type KnownProvider string

const (
	ProviderAnthropic     KnownProvider = "anthropic"
	ProviderOpenAI        KnownProvider = "openai"
	ProviderAmazonBedrock KnownProvider = "amazon-bedrock"
	ProviderGoogle        KnownProvider = "google"
	ProviderGoogleVertex  KnownProvider = "google-vertex"
	ProviderMistral       KnownProvider = "mistral"
	ProviderAzureOpenAI   KnownProvider = "azure-openai"
	ProviderOpenAICodex   KnownProvider = "openai-codex"
	ProviderGitHubCopilot KnownProvider = "github-copilot"
	ProviderOpenRouter    KnownProvider = "openrouter"
	ProviderFireworks     KnownProvider = "fireworks"
	ProviderTogether      KnownProvider = "together"
	ProviderGroq          KnownProvider = "groq"
	ProviderXAI           KnownProvider = "xai"
	ProviderDeepSeek      KnownProvider = "deepseek"
	ProviderCerebras      KnownProvider = "cerebras"
	ProviderCloudflare    KnownProvider = "cloudflare"
	ProviderHuggingFace   KnownProvider = "huggingface"
	ProviderMoonshot      KnownProvider = "moonshotai"
	ProviderMoonshotCN    KnownProvider = "moonshotai-cn"
	ProviderMinimax       KnownProvider = "minimax"
	ProviderMinimaxCN     KnownProvider = "minimax-cn"
	ProviderXiaomi        KnownProvider = "xiaomi"
	ProviderVercelGateway KnownProvider = "vercel-ai-gateway"
	ProviderCloudflareGW  KnownProvider = "cloudflare-ai-gateway"
)

// Modality represents an input/output modality.
type Modality string

const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityAudio Modality = "audio"
)

// ThinkingLevel represents the depth of reasoning.
type ThinkingLevel string

const (
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
)

// StopReason indicates why generation stopped.
type StopReason string

const (
	StopStop    StopReason = "stop"
	StopLength  StopReason = "length"
	StopToolUse StopReason = "toolUse"
	StopError   StopReason = "error"
	StopAborted StopReason = "aborted"
)

// CacheRetention controls prompt caching behavior.
type CacheRetention string

const (
	CacheNone  CacheRetention = "none"
	CacheShort CacheRetention = "short"
	CacheLong  CacheRetention = "long"
)

// Transport selects the streaming transport.
type Transport string

const (
	TransportSSE       Transport = "sse"
	TransportWebSocket Transport = "websocket"
	TransportAuto      Transport = "auto"
)

// Cost represents per-million-token pricing.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead,omitempty"`
	CacheWrite float64 `json:"cacheWrite,omitempty"`
}

// Model describes an AI model's capabilities and pricing.
type Model struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	API              KnownAPI          `json:"api"`
	Provider         KnownProvider     `json:"provider"`
	BaseURL          string            `json:"baseUrl,omitempty"`
	Reasoning        bool              `json:"reasoning,omitempty"`
	ThinkingLevelMap map[string]string `json:"thinkingLevelMap,omitempty"`
	Input            []Modality        `json:"input"`
	Cost             Cost              `json:"cost"`
	ContextWindow    int               `json:"contextWindow"`
	MaxTokens        int               `json:"maxTokens"`
	Headers          map[string]string `json:"headers,omitempty"`
	Compat           *Compat           `json:"compat,omitempty"`
}

// Compat holds compatibility flags for OpenAI-compatible APIs.
type Compat struct {
	SupportsStore           bool   `json:"supportsStore,omitempty"`
	SupportsDeveloperRole   bool   `json:"supportsDeveloperRole,omitempty"`
	SupportsReasoningEffort bool   `json:"supportsReasoningEffort,omitempty"`
	MaxTokensField          string `json:"maxTokensField,omitempty"`
	RequiresToolResultName  bool   `json:"requiresToolResultName,omitempty"`
	RequiresThinkingAsText  bool   `json:"requiresThinkingAsText,omitempty"`
	ThinkingFormat          string `json:"thinkingFormat,omitempty"`
	CacheControlFormat      string `json:"cacheControlFormat,omitempty"`
}

// ContentBlock is a union type for message content.
type ContentBlock interface {
	contentTag()
}

// TextContent represents text in a message.
type TextContent struct {
	Type          string `json:"type"`
	Text          string `json:"text"`
	TextSignature string `json:"textSignature,omitempty"`
}

func (TextContent) contentTag() {}

// ThinkingContent represents thinking/reasoning content.
type ThinkingContent struct {
	Type              string `json:"type"`
	Thinking          string `json:"thinking"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
	Redacted          bool   `json:"redacted,omitempty"`
}

func (ThinkingContent) contentTag() {}

// ImageContent represents an image in a message.
type ImageContent struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

func (ImageContent) contentTag() {}

// ToolCall represents a tool invocation from the model.
type ToolCall struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Arguments        json.RawMessage `json:"arguments"`
	ThoughtSignature string          `json:"thoughtSignature,omitempty"`
}

func (ToolCall) contentTag() {}

// Message is the interface for all message types.
type Message interface {
	messageTag()
	GetTimestamp() time.Time
}

// UserMessage represents a user message.
type UserMessage struct {
	Role      string    `json:"role"`
	Content   any       `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

func (UserMessage) messageTag()               {}
func (m UserMessage) GetTimestamp() time.Time { return m.Timestamp }

// AssistantMessage represents a model response.
type AssistantMessage struct {
	Role          string         `json:"role"`
	Content       []ContentBlock `json:"content"`
	API           KnownAPI       `json:"api"`
	Provider      KnownProvider  `json:"provider"`
	Model         string         `json:"model"`
	ResponseModel string         `json:"responseModel,omitempty"`
	ResponseID    string         `json:"responseId,omitempty"`
	Diagnostics   []Diagnostic   `json:"diagnostics,omitempty"`
	Usage         Usage          `json:"usage"`
	StopReason    StopReason     `json:"stopReason"`
	ErrorMessage  string         `json:"errorMessage,omitempty"`
	Timestamp     time.Time      `json:"timestamp"`
}

func (AssistantMessage) messageTag()               {}
func (m AssistantMessage) GetTimestamp() time.Time { return m.Timestamp }

// ToolResultMessage represents a tool execution result.
type ToolResultMessage struct {
	Role       string         `json:"role"`
	ToolCallID string         `json:"toolCallId"`
	ToolName   string         `json:"toolName"`
	Content    []ContentBlock `json:"content"`
	Details    any            `json:"details,omitempty"`
	IsError    bool           `json:"isError"`
	Timestamp  time.Time      `json:"timestamp"`
}

func (ToolResultMessage) messageTag()               {}
func (m ToolResultMessage) GetTimestamp() time.Time { return m.Timestamp }

// Usage represents token usage statistics.
type Usage struct {
	Input       int           `json:"input"`
	Output      int           `json:"output"`
	CacheRead   int           `json:"cacheRead,omitempty"`
	CacheWrite  int           `json:"cacheWrite,omitempty"`
	TotalTokens int           `json:"totalTokens"`
	Cost        CostBreakdown `json:"cost"`
}

// CostBreakdown represents the cost of a request.
type CostBreakdown struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead,omitempty"`
	CacheWrite float64 `json:"cacheWrite,omitempty"`
	Total      float64 `json:"total"`
}

// Diagnostic is defined in diagnostics.go with richer error metadata.

// Context represents the conversation context for a completion request.
type Context struct {
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []Tool    `json:"tools,omitempty"`
}

// Tool represents a tool definition.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// StreamOptions are options for streaming completions.
type StreamOptions struct {
	Temperature     *float64          `json:"temperature,omitempty"`
	MaxTokens       *int              `json:"maxTokens,omitempty"`
	Signal          <-chan struct{}   `json:"-"`
	APIKey          string            `json:"-"`
	Transport       Transport         `json:"transport,omitempty"`
	CacheRetention  CacheRetention    `json:"cacheRetention,omitempty"`
	SessionID       string            `json:"sessionId,omitempty"`
	OnPayload       func(any)         `json:"-"`
	OnResponse      func(any)         `json:"-"`
	Headers         map[string]string `json:"-"`
	TimeoutMs       int               `json:"timeoutMs,omitempty"`
	MaxRetries      int               `json:"maxRetries,omitempty"`
	MaxRetryDelayMs int               `json:"maxRetryDelayMs,omitempty"`
	Metadata        map[string]any    `json:"metadata,omitempty"`
}

// SimpleStreamOptions extends StreamOptions with unified reasoning controls.
type SimpleStreamOptions struct {
	StreamOptions
	Reasoning       ThinkingLevel  `json:"reasoning,omitempty"`
	ThinkingBudgets map[string]int `json:"thinkingBudgets,omitempty"`
}

// --- Image types ---

// ImagesModel describes an image generation model.
type ImagesModel struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	API      KnownAPI          `json:"api"`
	Provider KnownProvider     `json:"provider"`
	BaseURL  string            `json:"baseUrl,omitempty"`
	Input    []Modality        `json:"input"`
	Output   []Modality        `json:"output"`
	Cost     Cost              `json:"cost"`
	Headers  map[string]string `json:"headers,omitempty"`
}

// AssistantImages represents the result of image generation.
type AssistantImages struct {
	API          KnownAPI      `json:"api"`
	Provider     KnownProvider `json:"provider"`
	Model        string        `json:"model"`
	Output       []ImageData   `json:"output"`
	ResponseID   string        `json:"responseId,omitempty"`
	Usage        *Usage        `json:"usage,omitempty"`
	StopReason   StopReason    `json:"stopReason"`
	ErrorMessage string        `json:"errorMessage,omitempty"`
	Timestamp    time.Time     `json:"timestamp"`
}

// ImageData represents a generated image.
type ImageData struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// ImageOptions are options for image generation.
type ImageOptions struct {
	APIKey  string            `json:"-"`
	Headers map[string]string `json:"-"`
	Signal  <-chan struct{}   `json:"-"`
}

// NewTimeoutClient creates an http.Client with the specified timeout in milliseconds.
// If timeoutMs is 0 or negative, defaults to 5 minutes.
func NewTimeoutClient(timeoutMs int) *http.Client {
	timeout := 5 * time.Minute
	if timeoutMs > 0 {
		timeout = time.Duration(timeoutMs) * time.Millisecond
	}
	return &http.Client{Timeout: timeout}
}
