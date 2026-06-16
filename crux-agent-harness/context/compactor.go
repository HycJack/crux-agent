package context

import (
	"context"
	"fmt"
	"strings"

	"github.com/hycjack/crux-ai/llm"
	core "github.com/hycjack/crux-ai/core"
)

// Compactor is the interface for compaction strategies.
type Compactor interface {
	// Compact summarizes older messages into a single summary message.
	Compact(ctx context.Context, req CompactionRequest, opts ...core.SimpleStreamOptions) (string, error)
}

// --- LLM Compactor (default) ---

// LLMCompactor uses an LLM to generate a summary of older messages.
type LLMCompactor struct {
	Model        core.Model
	SummaryPrompt string
}

// NewLLMCompactor creates an LLM-based compactor with a sensible default prompt.
func NewLLMCompactor(model core.Model) *LLMCompactor {
	return &LLMCompactor{
		Model: model,
		SummaryPrompt: `Summarize the following conversation history concisely, preserving:
- Key decisions and conclusions
- Important facts and context
- File operations (reads/writes) and their results
- Tool call outcomes
- The user's goals and current progress

Conversation:
%s`,
	}
}

func (c *LLMCompactor) Compact(ctx context.Context, req CompactionRequest, opts ...core.SimpleStreamOptions) (string, error) {
	serialized := SerializeMessages(req.ToSummarize)
	prompt := fmt.Sprintf(c.SummaryPrompt, serialized)

	var streamOpts []core.SimpleStreamOptions
	if len(opts) > 0 {
		streamOpts = opts
	}

	msg, err := llm.CompleteSimple(ctx, c.Model, []core.Message{
		core.UserMessage{Content: prompt},
	}, streamOpts...)
	if err != nil {
		return "", fmt.Errorf("llm compaction failed: %w", err)
	}

	summary := ExtractText(msg)
	if summary == "" {
		return "", fmt.Errorf("llm returned empty summary")
	}
	return summary, nil
}

// --- Sliding Window Compactor (no LLM needed) ---

// SlidingWindowCompactor discards old messages without summarization.
// Use when LLM calls are too expensive or latency-sensitive.
type SlidingWindowCompactor struct{}

func (c *SlidingWindowCompactor) Compact(ctx context.Context, req CompactionRequest, opts ...core.SimpleStreamOptions) (string, error) {
	return "[Earlier conversation history removed to fit context window]", nil
}

// --- Hybrid Compactor ---

// HybridCompactor tries LLM first, falls back to sliding window.
type HybridCompactor struct {
	LLM      *LLMCompactor
	Fallback *SlidingWindowCompactor
}

func NewHybridCompactor(model core.Model) *HybridCompactor {
	return &HybridCompactor{
		LLM:      NewLLMCompactor(model),
		Fallback: &SlidingWindowCompactor{},
	}
}

func (c *HybridCompactor) Compact(ctx context.Context, req CompactionRequest, opts ...core.SimpleStreamOptions) (string, error) {
	summary, err := c.LLM.Compact(ctx, req, opts...)
	if err != nil {
		// Fallback to sliding window
		return c.Fallback.Compact(ctx, req, opts...)
	}
	return summary, nil
}

// --- Serialization helper ---

// SerializeMessages converts messages to human-readable text for summarization.
func SerializeMessages(messages []core.Message) string {
	var parts []string
	for _, msg := range messages {
		switch m := msg.(type) {
		case core.UserMessage:
			parts = append(parts, fmt.Sprintf("User: %v", m.Content))
		case core.AssistantMessage:
			text := extractAssistantText(m)
			if text != "" {
				parts = append(parts, fmt.Sprintf("Assistant: %s", text))
			}
		case core.ToolResultMessage:
			text := extractToolResultText(m)
			parts = append(parts, fmt.Sprintf("Tool [%s]: %s", m.ToolName, text))
		}
	}
	return strings.Join(parts, "\n\n")
}

// ExtractText extracts text content from an AssistantMessage.
func ExtractText(msg core.AssistantMessage) string {
	var parts []string
	for _, block := range msg.Content {
		if tc, ok := block.(core.TextContent); ok && tc.Text != "" {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractAssistantText(msg core.AssistantMessage) string {
	var parts []string
	for _, block := range msg.Content {
		if tc, ok := block.(core.TextContent); ok && tc.Text != "" {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractToolResultText(msg core.ToolResultMessage) string {
	var parts []string
	for _, block := range msg.Content {
		if tc, ok := block.(core.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
