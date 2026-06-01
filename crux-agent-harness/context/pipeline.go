package context

import (
	"context"
	"sync"

	core "crux-ai/core"
	"crux-agent-harness/token"
)

// PipelineConfig configures the context management pipeline.
type PipelineConfig struct {
	// Model is the LLM model (used for token counting and compaction).
	Model core.Model
	// Budget is the token budget.
	Budget Budget
	// CompactionThreshold triggers compaction when usage exceeds this ratio (0.0-1.0).
	// Default: 0.9 (compact when 90% of budget is used).
	CompactionThreshold float64
	// MinMessagesToKeep preserves this many recent messages during compaction.
	MinMessagesToKeep int
	// Compactor is the compaction strategy. Default: LLMCompactor.
	Compactor Compactor
	// OnCompaction is called when compaction occurs (for logging/observability).
	OnCompaction func(result *CompactionResult)
}

// DefaultPipelineConfig returns sensible defaults.
func DefaultPipelineConfig(model core.Model, contextWindow int) PipelineConfig {
	return PipelineConfig{
		Model:               model,
		Budget:              DefaultBudget(contextWindow),
		CompactionThreshold: 0.9,
		MinMessagesToKeep:   10,
	}
}

// CompactionResult holds the outcome of a compaction.
type CompactionResult struct {
	Summary      string `json:"summary"`
	TokensBefore int    `json:"tokensBefore"`
	TokensAfter  int    `json:"tokensAfter"`
	TokensSaved  int    `json:"tokensSaved"`
	KeptCount    int    `json:"keptCount"`
}

// Pipeline manages the context window automatically.
type Pipeline struct {
	mu     sync.RWMutex
	config PipelineConfig
	mc     *token.MessageCounter
}

// NewPipeline creates a new context management pipeline.
func NewPipeline(config PipelineConfig) (*Pipeline, error) {
	mc, err := token.NewMessageCounter(config.Model.ID)
	if err != nil {
		return nil, err
	}

	if config.CompactionThreshold <= 0 {
		config.CompactionThreshold = 0.9
	}
	if config.MinMessagesToKeep <= 0 {
		config.MinMessagesToKeep = 10
	}
	if config.Compactor == nil {
		config.Compactor = NewLLMCompactor(config.Model)
	}

	return &Pipeline{config: config, mc: mc}, nil
}

// Check evaluates the current context and returns its status.
func (p *Pipeline) Check(systemPrompt string, messages []core.Message, tools []core.Tool) Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return CheckStatus(p.mc, systemPrompt, messages, tools, p.config.Budget)
}

// ShouldCompact returns true if compaction is needed.
func (p *Pipeline) ShouldCompact(systemPrompt string, messages []core.Message, tools []core.Tool) bool {
	status := p.Check(systemPrompt, messages, tools)
	return NeedsCompaction(status, p.config.CompactionThreshold)
}

// Compact performs compaction if needed. Returns the compacted messages and result.
// If no compaction is needed, returns the original messages and nil result.
func (p *Pipeline) Compact(ctx context.Context, systemPrompt string, messages []core.Message, tools []core.Tool, opts ...core.SimpleStreamOptions) ([]core.Message, *CompactionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	req, err := PlanCompaction(p.mc, systemPrompt, messages, tools, p.config.Budget, p.config.MinMessagesToKeep)
	if err != nil {
		// No compaction needed or not enough messages
		return messages, nil, nil
	}

	summary, err := p.config.Compactor.Compact(ctx, *req, opts...)
	if err != nil {
		return messages, nil, err
	}

	// Build compacted messages:
	// [summary as user message] + [kept messages]
	summaryMsg := core.UserMessage{
		Role:    "user",
		Content: summaryPrefix + summary + summarySuffix,
	}
	compacted := make([]core.Message, 0, 1+len(req.ToKeep))
	compacted = append(compacted, summaryMsg)
	compacted = append(compacted, req.ToKeep...)

	// Calculate new token usage
	newEst := p.mc.EstimateRequestTokens(systemPrompt, compacted, tools)

	result := &CompactionResult{
		Summary:      summary,
		TokensBefore: req.TokensBefore,
		TokensAfter:  newEst.Total,
		TokensSaved:  req.TokensBefore - newEst.Total,
		KeptCount:    len(req.ToKeep),
	}

	if p.config.OnCompaction != nil {
		p.config.OnCompaction(result)
	}

	return compacted, result, nil
}

// MessageCounter returns the underlying message counter.
func (p *Pipeline) MessageCounter() *token.MessageCounter {
	return p.mc
}

const (
	summaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	summarySuffix = "\n</summary>"
)
