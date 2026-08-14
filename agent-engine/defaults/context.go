package defaults

import (
	"context"
	"log"
	"sync"

	"github.com/hycjack/crux-kernel/plugin"
	core "github.com/hycjack/crux-ai/core"
)

// ─── ContextPipeline ────────────────────────────────────────────────────────

// ContextPipeline manages the context window with automatic compaction.
//
// It uses MessageCounter (backed by tiktoken) for token counting, and a
// sliding-window compaction strategy that keeps the first message plus
// the last N messages.
type ContextPipeline struct {
	mu                  sync.RWMutex
	mc                  *MessageCounter
	compactionThreshold float64
	minMessagesToKeep   int
	messages            []core.Message
	totalTokens         int
	maxTokens           int
	compactions         int
}

// NewContextPipeline creates a new context pipeline with the given model.
// maxTokens sets the context window budget (e.g., 128000 for gpt-4o).
func NewContextPipeline(model core.Model, maxTokens int) (*ContextPipeline, error) {
	mc, err := NewMessageCounter(model.ID)
	if err != nil {
		return nil, err
	}
	return &ContextPipeline{
		mc:                  mc,
		maxTokens:           maxTokens,
		compactionThreshold: 0.9,
		minMessagesToKeep:   10,
		messages:            make([]core.Message, 0),
	}, nil
}

// AddMessage appends a message and updates the token estimate.
func (p *ContextPipeline) AddMessage(msg core.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.messages = append(p.messages, msg)
	p.totalTokens += p.mc.CountMessage(msg)
	return nil
}

// GetMessages returns all messages.
func (p *ContextPipeline) GetMessages() []core.Message {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]core.Message, len(p.messages))
	copy(result, p.messages)
	return result
}

// IsNearLimit checks if token usage is near the compaction threshold.
func (p *ContextPipeline) IsNearLimit(threshold float64) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.maxTokens <= 0 {
		return false
	}
	return float64(p.totalTokens)/float64(p.maxTokens) >= threshold
}

// GetStats returns the current context stats.
func (p *ContextPipeline) GetStats() plugin.ContextStats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	usage := 0.0
	if p.maxTokens > 0 {
		usage = float64(p.totalTokens) / float64(p.maxTokens)
	}
	return plugin.ContextStats{
		TotalTokens:     p.totalTokens,
		MessageCount:    len(p.messages),
		Compactions:     p.compactions,
		MaxTokens:       p.maxTokens,
		AvailableTokens: p.maxTokens - p.totalTokens,
		UsagePercent:    usage,
	}
}

// Compact runs compaction: keeps first + last N messages, discards the middle.
func (p *ContextPipeline) Compact(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.messages) <= p.minMessagesToKeep+2 {
		return nil
	}

	kept := make([]core.Message, 0, p.minMessagesToKeep+1)
	if len(p.messages) > 0 {
		kept = append(kept, p.messages[0])
	}

	start := len(p.messages) - p.minMessagesToKeep
	if start < 1 {
		start = 1
	}
	kept = append(kept, p.messages[start:]...)

	p.messages = kept
	p.totalTokens = 0
	for _, msg := range kept {
		p.totalTokens += p.mc.CountMessage(msg)
	}
	p.compactions++
	return nil
}

// compile-time interface check
var _ plugin.ContextPlugin = (*ContextPipeline)(nil)

// ─── Compaction helper (for engine.CompactionConfig) ────────────────────────

// NewCompactor creates a compaction function suitable for engine.CompactionConfig.
// It uses the context pipeline's sliding-window compaction logic.
func NewCompactor(pipeline *ContextPipeline) func(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
	return func(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
		if len(msgs) <= pipeline.minMessagesToKeep+2 {
			return msgs, false, nil
		}
		kept := make([]core.Message, 0, pipeline.minMessagesToKeep+1)
		kept = append(kept, msgs[0])
		start := len(msgs) - pipeline.minMessagesToKeep
		if start < 1 {
			start = 1
		}
		kept = append(kept, msgs[start:]...)
		return kept, len(kept) < len(msgs), nil
	}
}

// NewTokenCounter creates a token counting function suitable for engine.CompactionConfig.
// It wraps MessageCounter.EstimateRequestTokens for use as a function reference.
func NewTokenCounter(model core.Model) func(string, []core.Message, []core.Tool) int {
	mc, err := NewMessageCounter(model.ID)
	if err != nil {
		log.Printf("warning: failed to create message counter for %s, using fallback: %v", model.ID, err)
		// Fallback: use a simple estimation if MessageCounter can't initialize
		return func(systemPrompt string, messages []core.Message, tools []core.Tool) int {
			total := countTokensFallback(systemPrompt)
			for _, msg := range messages {
				total += countTokensFallback(messageText(msg))
			}
			for _, t := range tools {
				total += countTokensFallback(t.Name) + countTokensFallback(t.Description) + len(t.Parameters)/4 + 20
			}
			return total
		}
	}
	return mc.AsTokenCounter()
}

// messageText extracts a flat text representation from a message (fallback only).
func messageText(msg core.Message) string {
	switch m := msg.(type) {
	case core.UserMessage:
		if s, ok := m.Content.(string); ok {
			return s
		}
	case core.AssistantMessage:
		var s string
		for _, block := range m.Content {
			if tc, ok := block.(core.TextContent); ok {
				s += tc.Text
			}
		}
		return s
	case core.ToolResultMessage:
		var s string
		for _, block := range m.Content {
			if tc, ok := block.(core.TextContent); ok {
				s += tc.Text
			}
		}
		return s
	}
	return ""
}
