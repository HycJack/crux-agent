// Package context provides context window management and compaction pipeline.
package context

import (
	"fmt"

	"github.com/hycjack/crux-agent-harness/token"
	core "github.com/hycjack/crux-ai/core"
)

// Budget describes the token budget for a context window.
type Budget struct {
	ContextWindow int // Model's max context window
	MaxOutput     int // Reserved for model output
	Headroom      int // Safety margin
}

// DefaultBudget returns a sensible default for most models.
func DefaultBudget(contextWindow int) Budget {
	return Budget{
		ContextWindow: contextWindow,
		MaxOutput:     8192,
		Headroom:      1024,
	}
}

// Available returns the token budget available for input (system + tools + messages).
func (b Budget) Available() int {
	avail := b.ContextWindow - b.MaxOutput - b.Headroom
	if avail < 0 {
		return 0
	}
	return avail
}

// Status reports current token usage against a budget.
type Status struct {
	Used      int     `json:"used"`
	Available int     `json:"available"`
	Ratio     float64 `json:"ratio"` // used/available, >1.0 means over budget
}

// CheckStatus calculates how much of the budget is used.
func CheckStatus(counter *token.MessageCounter, systemPrompt string, messages []core.Message, tools []core.Tool, budget Budget) Status {
	est := counter.EstimateRequestTokens(systemPrompt, messages, tools)
	avail := budget.Available()
	ratio := float64(est.Total) / float64(avail)
	if avail == 0 {
		ratio = 0
	}
	return Status{
		Used:      est.Total,
		Available: avail,
		Ratio:     ratio,
	}
}

// NeedsCompaction returns true if the context is over the threshold.
func NeedsCompaction(status Status, threshold float64) bool {
	return status.Ratio >= threshold
}

// CompactionRequest describes what needs to be compacted.
type CompactionRequest struct {
	// Messages to summarize (older messages)
	ToSummarize []core.Message
	// Messages to keep verbatim (recent messages)
	ToKeep []core.Message
	// Token count before compaction
	TokensBefore int
	// Token count of kept messages
	TokensKept int
	// Split index (for session entry tracking)
	SplitIndex int
}

// PlanCompaction decides which messages to summarize and which to keep.
// minKeep: minimum number of recent messages to preserve.
func PlanCompaction(counter *token.MessageCounter, systemPrompt string, messages []core.Message, tools []core.Tool, budget Budget, minKeep int) (*CompactionRequest, error) {
	if len(messages) <= minKeep {
		return nil, fmt.Errorf("not enough messages to compact: %d <= %d", len(messages), minKeep)
	}

	est := counter.EstimateRequestTokens(systemPrompt, messages, tools)
	avail := budget.Available()

	if est.Total <= avail {
		return nil, fmt.Errorf("no compaction needed: %d tokens used, %d available", est.Total, avail)
	}

	// Ensure minKeep is within valid bounds
	if minKeep < 1 {
		minKeep = 1
	}
	if minKeep > len(messages)-1 {
		minKeep = len(messages) - 1
	}

	// Binary search: find the split point where keeping [split:] fits in budget
	low := 0
	high := len(messages) - minKeep

	for low < high {
		mid := (low + high) / 2
		kept := messages[mid:]
		keptEst := counter.EstimateRequestTokens(systemPrompt, kept, tools)

		if keptEst.Total <= avail {
			high = mid // Try keeping more (earlier split)
		} else {
			low = mid + 1 // Need to keep less
		}
	}

	splitIdx := low
	// Ensure splitIdx is within valid bounds
	maxSplitIdx := len(messages) - minKeep
	if splitIdx > maxSplitIdx {
		splitIdx = maxSplitIdx
	}
	if splitIdx < 0 {
		splitIdx = 0
	}

	// Ensure we have at least minKeep messages to keep
	if len(messages)-splitIdx < minKeep {
		splitIdx = len(messages) - minKeep
	}

	toSummarize := messages[:splitIdx]
	toKeep := messages[splitIdx:]

	keptEst := counter.EstimateRequestTokens(systemPrompt, toKeep, tools)

	return &CompactionRequest{
		ToSummarize:  toSummarize,
		ToKeep:       toKeep,
		TokensBefore: est.Total,
		TokensKept:   keptEst.Total,
		SplitIndex:   splitIdx,
	}, nil
}
