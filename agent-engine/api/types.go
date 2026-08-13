// Package api implements an OpenAI-compatible HTTP API layer for agent-engine.
//
// It provides:
//  1. OpenAI Chat Completions request/response types
//  2. HTTP handler for /v1/chat/completions (both streaming and non-streaming)
//  3. Request → core.Context translator
//  4. Response → OpenAI JSON/SSE serializer
//
// Usage (embedding in your HTTP server):
//
//	handler := api.NewChatHandler(api.HandlerConfig{
//	    Model:             myModel,
//	    SystemPrompt:      "You are...",
//	    Tools:             myTools,
//	    SimpleStreamOptions: core.SimpleStreamOptions{
//	        StreamOptions: core.StreamOptions{APIKey: apiKey},
//	    },
//	})
//	mux.Handle("POST /v1/chat/completions", handler)
//
// For stateful agent sessions:
//
//	handler := api.NewStatefulHandler(agent)
//	mux.Handle("POST /v1/chat/completions", handler)
package api

import (
	"encoding/json"
)

// ─── OpenAI Chat Completions Request ────────────────────────────────────────

// ChatRequest represents an OpenAI-compatible chat completions request.
type ChatRequest struct {
	Model       string          `json:"model"`
	Messages    []ChatMessage   `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Tools       json.RawMessage `json:"tools,omitempty"`       // tool definitions
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"` // "auto" | "none" | "required" | {...}
	Stop        json.RawMessage `json:"stop,omitempty"`        // string or []string
	User        string          `json:"user,omitempty"`
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`

	// Non-standard but widely supported fields
	ReasoningEffort string `json:"reasoning_effort,omitempty"` // low | medium | high

	// Metadata passthrough
	Metadata map[string]any `json:"metadata,omitempty"`
}

// StreamOptions mirrors the OpenAI stream_options parameter.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ChatMessage represents a message in the OpenAI format.
type ChatMessage struct {
	Role       string          `json:"role"`                 // "system" | "user" | "assistant" | "tool"
	Content    json.RawMessage `json:"content"`              // string or []ContentPart
	Name       string          `json:"name,omitempty"`       // optional user name
	ToolCallID string          `json:"tool_call_id,omitempty"` // for tool role
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"` // for assistant role
}

// ContentPart is a single content block in multi-modal messages.
type ContentPart struct {
	Type     string          `json:"type"`               // "text" | "image_url"
	Text     string          `json:"text,omitempty"`
	ImageURL *ImageURL       `json:"image_url,omitempty"`
}

// ImageURL represents an image URL in a user message.
type ImageURL struct {
	URL string `json:"url"`
}

// ─── OpenAI Chat Completions Response ───────────────────────────────────────

// ChatResponse is a non-streaming chat completions response.
type ChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []ResponseChoice `json:"choices"`
	Usage   *UsageInfo     `json:"usage,omitempty"`
}

// ResponseChoice is a single completion choice.
type ResponseChoice struct {
	Index        int           `json:"index"`
	Message      ResponseMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

// ResponseMessage is the assistant's response message.
type ResponseMessage struct {
	Role      string           `json:"role"`
	Content   *string          `json:"content"` // null when tool_calls is set
	ToolCalls []ResponseToolCall `json:"tool_calls,omitempty"`
}

// ResponseToolCall represents a tool call in the response.
type ResponseToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"` // "function"
	Function ResponseFunction   `json:"function"`
}

// ResponseFunction represents the function details in a tool call.
type ResponseFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// UsageInfo mirrors OpenAI usage information.
type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ─── SSE Event Types ────────────────────────────────────────────────────────

// SSEChatChunk is a streaming chunk (SSE data).
type SSEChatChunk struct {
	ID                string            `json:"id"`
	Object            string            `json:"object"`
	Created           int64             `json:"created"`
	Model             string            `json:"model"`
	Choices           []SSEChoice       `json:"choices"`
	Usage             *UsageInfo        `json:"usage,omitempty"`
	SystemFingerprint string            `json:"system_fingerprint,omitempty"`
}

// SSEChoice is a single streaming choice.
type SSEChoice struct {
	Index        int            `json:"index"`
	Delta        SSEDelta       `json:"delta"`
	FinishReason *string        `json:"finish_reason,omitempty"`
}

// SSEDelta is the content delta in a streaming chunk.
type SSEDelta struct {
	Role             string             `json:"role,omitempty"`
	Content          string             `json:"content,omitempty"`
	ToolCalls        []SSEToolCallDelta `json:"tool_calls,omitempty"`
	ReasoningContent string             `json:"reasoning_content,omitempty"`
}

// SSEToolCallDelta is a streaming tool call delta.
type SSEToolCallDelta struct {
	Index    int              `json:"index"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"` // "function"
	Function *SSEFunctionDelta `json:"function,omitempty"`
}

// SSEFunctionDelta is the function arguments delta for a tool call.
type SSEFunctionDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}
