package token

import (
	"encoding/json"

	core "crux-ai/core"
)

// MessageCounter counts tokens in a slice of messages.
type MessageCounter struct {
	counter *Counter
}

// NewMessageCounter creates a MessageCounter for the given model.
func NewMessageCounter(model string) (*MessageCounter, error) {
	c, err := GetCounter(model)
	if err != nil {
		return nil, err
	}
	return &MessageCounter{counter: c}, nil
}

// CountMessages returns the total token count for a slice of messages.
// Includes per-message overhead (role, formatting, separators).
func (mc *MessageCounter) CountMessages(messages []core.Message) int {
	total := 0
	for _, msg := range messages {
		total += mc.CountMessage(msg)
	}
	return total
}

// CountMessage returns the token count for a single message.
func (mc *MessageCounter) CountMessage(msg core.Message) int {
	// Per-message overhead: <|start|>{role}\n ... <|end|> ≈ 4 tokens
	const messageOverhead = 4

	switch m := msg.(type) {
	case core.UserMessage:
		return messageOverhead + mc.countRole("user") + mc.countContent(m.Content)
	case core.AssistantMessage:
		tokens := messageOverhead + mc.countRole("assistant")
		for _, block := range m.Content {
			tokens += mc.countContentBlock(block)
		}
		return tokens
	case core.ToolResultMessage:
		tokens := messageOverhead + mc.countRole("tool")
		for _, block := range m.Content {
			tokens += mc.countContentBlock(block)
		}
		return tokens
	default:
		return messageOverhead
	}
}

func (mc *MessageCounter) countRole(role string) int {
	return mc.counter.CountTokens(role)
}

func (mc *MessageCounter) countContent(content any) int {
	switch c := content.(type) {
	case string:
		return mc.counter.CountTokens(c)
	case []core.ContentBlock:
		total := 0
		for _, block := range c {
			total += mc.countContentBlock(block)
		}
		return total
	default:
		b, _ := json.Marshal(c)
		return mc.counter.CountTokens(string(b))
	}
}

func (mc *MessageCounter) countContentBlock(block core.ContentBlock) int {
	switch b := block.(type) {
	case core.TextContent:
		return mc.counter.CountTokens(b.Text)
	case core.ThinkingContent:
		return mc.counter.CountTokens(b.Thinking)
	case core.ImageContent:
		// Base64 images: rough estimate, each token ≈ 4 chars
		return len(b.Data) / 4
	case core.ToolCall:
		return mc.counter.CountTokens(b.Name) + len(b.Arguments)/4
	default:
		return 0
	}
}

// CountSystemPrompt counts tokens in a system prompt string.
func (mc *MessageCounter) CountSystemPrompt(prompt string) int {
	if prompt == "" {
		return 0
	}
	// System message has its own format overhead
	return mc.counter.CountTokens(prompt) + 4
}

// CountTools counts tokens in tool definitions.
func (mc *MessageCounter) CountTools(tools []core.Tool) int {
	total := 0
	for _, tool := range tools {
		total += mc.counter.CountTokens(tool.Name)
		total += mc.counter.CountTokens(tool.Description)
		if len(tool.Parameters) > 0 {
			total += len(tool.Parameters) / 4
		}
		// Per-tool overhead
		total += 10
	}
	return total
}

// EstimateRequestTokens estimates total tokens for a complete LLM request.
func (mc *MessageCounter) EstimateRequestTokens(systemPrompt string, messages []core.Message, tools []core.Tool) RequestTokenEstimate {
	system := mc.CountSystemPrompt(systemPrompt)
	msgTokens := mc.CountMessages(messages)
	toolTokens := mc.CountTools(tools)

	// Base overhead: reply priming, etc.
	const baseOverhead = 10

	return RequestTokenEstimate{
		System:  system,
		Messages: msgTokens,
		Tools:   toolTokens,
		Total:   system + msgTokens + toolTokens + baseOverhead,
	}
}

// RequestTokenEstimate breaks down token usage by category.
type RequestTokenEstimate struct {
	System   int `json:"system"`
	Messages int `json:"messages"`
	Tools    int `json:"tools"`
	Total    int `json:"total"`
}
