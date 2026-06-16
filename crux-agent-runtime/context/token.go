// Package context provides context window management for agents.
//
// It includes:
//   - Token counting for estimating message size
//   - Context window management
//   - Compaction strategies (SlideWindow, LLMSummarize)
//   - Integration with session management
package context

import (
	"github.com/hycjack/crux-ai/core"
)

// TokenCounter is a function that estimates token count.
// || Token 计数函数类型
type TokenCounter func(systemPrompt string, messages []core.Message, tools []core.Tool) int

// DefaultTokenCounter provides a simple token counting heuristic.
// It assumes ~4 chars per token (rough estimate for English text).
// || 默认 Token 计数器（粗略估计）
func DefaultTokenCounter(systemPrompt string, messages []core.Message, tools []core.Tool) int {
	tokens := len(systemPrompt) / 4

	for _, msg := range messages {
		tokens += messageTokenCount(msg) / 4
	}

	// Add overhead for tools
	tokens += len(tools) * 100

	return tokens
}

// messageTokenCount estimates the token count for a single message.
func messageTokenCount(msg core.Message) int {
	switch m := msg.(type) {
	case core.UserMessage:
		return userMessageTokenCount(m)
	case core.AssistantMessage:
		return contentTokenCount(m.Content)
	case core.ToolResultMessage:
		return contentTokenCount(m.Content)
	default:
		return 0
	}
}

// userMessageTokenCount handles the any type of UserMessage.Content
func userMessageTokenCount(m core.UserMessage) int {
	switch c := m.Content.(type) {
	case []core.ContentBlock:
		return contentTokenCount(c)
	case string:
		return len(c)
	default:
		return 0
	}
}

// contentTokenCount estimates the token count for content blocks.
func contentTokenCount(blocks []core.ContentBlock) int {
	count := 0
	for _, block := range blocks {
		switch b := block.(type) {
		case core.TextContent:
			count += len(b.Text)
		case core.ThinkingContent:
			count += len(b.Thinking)
		case core.ToolCall:
			count += len(b.Name) + len(b.Arguments) + 50 // base overhead for tool call
		}
	}
	return count
}

// ContextWindowConfig configures the context window management.
// || 上下文窗口配置
type ContextWindowConfig struct {
	// MaxTokens is the maximum token count for the context window.
	// Default 128000.
	MaxTokens int

	// ReserveTokens is the number of tokens to reserve for the response.
	// Default 4096.
	ReserveTokens int

	// MinMessages is the minimum number of messages to keep after compaction.
	// Default 4 (system + last user + last assistant + last tool_result).
	MinMessages int

	// TokenCounter is the function to estimate token count.
	// If nil, DefaultTokenCounter is used.
	TokenCounter TokenCounter
}

// DefaultContextWindowConfig returns a default configuration.
func DefaultContextWindowConfig() ContextWindowConfig {
	return ContextWindowConfig{
		MaxTokens:     128000,
		ReserveTokens: 4096,
		MinMessages:   4,
		TokenCounter:  DefaultTokenCounter,
	}
}

// NeedsCompaction checks if the messages exceed the context window.
func NeedsCompaction(counter TokenCounter, systemPrompt string, messages []core.Message, tools []core.Tool, config ContextWindowConfig) bool {
	if counter == nil {
		counter = DefaultTokenCounter
	}
	tokens := counter(systemPrompt, messages, tools)
	availableTokens := config.MaxTokens - config.ReserveTokens
	return tokens > availableTokens
}

// EstimateTokens estimates the token count for the given context.
func EstimateTokens(counter TokenCounter, systemPrompt string, messages []core.Message, tools []core.Tool) int {
	if counter == nil {
		counter = DefaultTokenCounter
	}
	return counter(systemPrompt, messages, tools)
}
