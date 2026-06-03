// Package core defines the fundamental types shared across crux-ai.
package core

import (
	"fmt"
)

// CompactionConfig configures the context compaction behavior.
type CompactionConfig struct {
	// ContextWindow is the model's maximum context window in tokens.
	ContextWindow int
	// MaxOutput is the reserved tokens for model output.
	MaxOutput int
	// Headroom is the safety margin in tokens.
	Headroom int
	// Threshold triggers compaction when usage exceeds this ratio (0.0-1.0).
	Threshold float64
	// MinMessagesToKeep preserves this many recent messages during compaction.
	MinMessagesToKeep int
}

// DefaultCompactionConfig returns sensible defaults.
func DefaultCompactionConfig(contextWindow int) CompactionConfig {
	return CompactionConfig{
		ContextWindow:     contextWindow,
		MaxOutput:         8192,
		Headroom:          1024,
		Threshold:         0.9,
		MinMessagesToKeep: 10,
	}
}

// AvailableBudget returns the token budget available for input.
func (c CompactionConfig) AvailableBudget() int {
	avail := c.ContextWindow - c.MaxOutput - c.Headroom
	if avail < 0 {
		return 0
	}
	return avail
}

// CompactionStatus reports current token usage against the budget.
type CompactionStatus struct {
	Used      int     `json:"used"`
	Available int     `json:"available"`
	Ratio     float64 `json:"ratio"` // used/available, >1.0 means over budget
}

// CompactionResult holds the outcome of a compaction.
type CompactionResult struct {
	Summary      string `json:"summary"`
	TokensBefore int    `json:"tokensBefore"`
	TokensAfter  int    `json:"tokensAfter"`
	TokensSaved  int    `json:"tokensSaved"`
	KeptCount    int    `json:"keptCount"`
}

// Compactor defines the interface for compaction strategies.
type Compactor interface {
	Compact(ctx interface{}, systemPrompt string, toSummarize []Message) (string, error)
}

// CompactionRequest describes what needs to be compacted.
type CompactionRequest struct {
	ToSummarize  []Message
	ToKeep       []Message
	TokensBefore int
	TokensKept   int
	SplitIndex   int
}

// PlanCompaction decides which messages to summarize and which to keep.
func PlanCompaction(
	tokenCounter func(systemPrompt string, messages []Message, tools []Tool) int,
	systemPrompt string,
	messages []Message,
	tools []Tool,
	config CompactionConfig,
) (*CompactionRequest, error) {

	if len(messages) <= config.MinMessagesToKeep {
		return nil, fmt.Errorf("not enough messages to compact: %d <= %d", len(messages), config.MinMessagesToKeep)
	}

	tokensUsed := tokenCounter(systemPrompt, messages, tools)
	available := config.AvailableBudget()

	if available == 0 {
		return nil, fmt.Errorf("no compaction budget available")
	}

	// Use Threshold to determine if compaction is needed, consistent with NeedsCompaction
	ratio := float64(tokensUsed) / float64(available)
	if ratio < config.Threshold {
		return nil, fmt.Errorf("no compaction needed: %.2f ratio < %.2f threshold", ratio, config.Threshold)
	}

	minKeep := config.MinMessagesToKeep
	if minKeep < 1 {
		minKeep = 1
	}
	if minKeep > len(messages)-1 {
		minKeep = len(messages) - 1
	}

	low := 0
	high := len(messages) - minKeep

	for low < high {
		mid := (low + high) / 2
		kept := messages[mid:]
		keptTokens := tokenCounter(systemPrompt, kept, tools)

		if keptTokens <= available {
			high = mid
		} else {
			low = mid + 1
		}
	}

	splitIdx := low
	maxSplitIdx := len(messages) - minKeep
	if splitIdx > maxSplitIdx {
		splitIdx = maxSplitIdx
	}
	if splitIdx < 0 {
		splitIdx = 0
	}

	if len(messages)-splitIdx < minKeep {
		splitIdx = len(messages) - minKeep
	}

	toSummarize := messages[:splitIdx]
	toKeep := messages[splitIdx:]
	keptTokens := tokenCounter(systemPrompt, toKeep, tools)

	return &CompactionRequest{
		ToSummarize:  toSummarize,
		ToKeep:       toKeep,
		TokensBefore: tokensUsed,
		TokensKept:   keptTokens,
		SplitIndex:   splitIdx,
	}, nil
}

// NeedsCompaction returns true if compaction should be triggered.
func NeedsCompaction(
	tokenCounter func(systemPrompt string, messages []Message, tools []Tool) int,
	systemPrompt string,
	messages []Message,
	tools []Tool,
	config CompactionConfig,
) bool {
	if len(messages) <= config.MinMessagesToKeep {
		return false
	}

	tokensUsed := tokenCounter(systemPrompt, messages, tools)
	available := config.AvailableBudget()
	if available == 0 {
		return false
	}

	ratio := float64(tokensUsed) / float64(available)
	return ratio >= config.Threshold
}

const (
	summaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	summarySuffix = "\n</summary>"
)

// BuildCompactedMessages builds the compacted message list.
func BuildCompactedMessages(summary string, toKeep []Message) []Message {
	summaryMsg := UserMessage{
		Role:    "user",
		Content: []ContentBlock{TextContent{Type: "text", Text: summaryPrefix + summary + summarySuffix}},
	}
	compacted := make([]Message, 0, 1+len(toKeep))
	compacted = append(compacted, summaryMsg)
	compacted = append(compacted, toKeep...)
	return compacted
}
