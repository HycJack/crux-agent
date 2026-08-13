package defaults

import (
	"context"
	"fmt"

	core "github.com/hycjack/crux-ai/core"
)

// ─── Compaction strategies (LLM-summarize + sliding window) ────────────────
//
// These complement `NewCompactor` / `NewContextPipeline` (plain sliding
// window) by adding an LLM-based summarizer ahead of the window, so old
// messages are *semantically condensed* into a single summary rather than
// dropped wholesale.
//
// Use `NewLLMSummarizeCompactor` to build a compaction function directly
// compatible with `engine.CompactionConfig.Compactor`.

// Compactor is a strategy that takes a message slice and returns a
// (possibly shorter) replacement. It MUST be safe to call from a single
// goroutine; concurrency is the caller's responsibility.
type Compactor interface {
	// Compact returns a replacement slice. If it returns
	// (msgs, false), the caller should keep using the original
	// (no compaction needed). If (newMsgs, true), the caller should
	// adopt newMsgs.
	Compact(ctx context.Context, msgs []core.Message) (newMsgs []core.Message, changed bool, err error)

	// Name identifies the strategy (for telemetry / debugging).
	Name() string
}

// ─── SlideWindow ───────────────────────────────────────────────────────────

// SlideWindow keeps the system prompt + last N messages.
type SlideWindow struct {
	// MaxMessages is the maximum number of messages to keep, including
	// the system prompt. Default 50.
	MaxMessages int
}

// NewSlideWindow creates a SlideWindow that keeps the system prompt
// plus the last (MaxMessages-1) non-system messages.
func NewSlideWindow(maxMessages int) *SlideWindow {
	if maxMessages <= 0 {
		maxMessages = 50
	}
	return &SlideWindow{MaxMessages: maxMessages}
}

func (s *SlideWindow) Name() string { return "slide_window" }

func (s *SlideWindow) Compact(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
	if len(msgs) <= s.MaxMessages {
		return msgs, false, nil
	}
	tail := msgs[len(msgs)-s.MaxMessages:]
	out := append([]core.Message{}, tail...)
	return out, true, nil
}

// ─── LLMSummarize ──────────────────────────────────────────────────────────

// LLMSummarize drops the oldest messages and replaces them with a single
// "summary" message produced by calling Summarize on the dropped messages.
// If Summarize is nil, the dropped messages are simply dropped and a
// placeholder is inserted.
type LLMSummarize struct {
	// KeepLast is the number of recent messages to keep verbatim.
	// Default 10.
	KeepLast int

	// MinTrigger is the minimum number of messages required to even
	// consider compacting. Default 30. Below this, compaction is a
	// no-op (the cost of the LLM call exceeds the savings).
	MinTrigger int

	// Summarize produces a summary string from the dropped messages.
	// It may be nil — in which case we insert a placeholder
	// ("[summary of N older messages elided]") instead.
	Summarize func(ctx context.Context, dropped []core.Message) (summary string, err error)
}

// NewLLMSummarize creates a default-configured LLMSummarize.
func NewLLMSummarize() *LLMSummarize {
	return &LLMSummarize{
		KeepLast:   10,
		MinTrigger: 30,
	}
}

func (l *LLMSummarize) Name() string { return "llm_summarize" }

func (l *LLMSummarize) Compact(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
	if l.MinTrigger <= 0 {
		l.MinTrigger = 30
	}
	if len(msgs) < l.MinTrigger {
		return msgs, false, nil
	}
	keepLast := l.KeepLast
	if keepLast <= 0 {
		keepLast = 10
	}
	if len(msgs) <= keepLast+1 {
		return msgs, false, nil
	}

	// Split: head (system, if present) + middle (drop) + tail (keep).
	head := msgs[:1]
	start := 0
	if len(msgs) > 0 {
		if _, ok := msgs[0].(core.UserMessage); ok {
			head = nil
			start = 0
		} else {
			start = 1
		}
	}
	dropped := msgs[start : len(msgs)-keepLast]
	tail := msgs[len(msgs)-keepLast:]

	var summary string
	if l.Summarize != nil {
		s, err := l.Summarize(ctx, dropped)
		if err != nil {
			return msgs, false, fmt.Errorf("summarize: %w", err)
		}
		summary = s
	} else {
		summary = fmt.Sprintf("[summary of %d older messages elided]", len(dropped))
	}

	out := append([]core.Message{}, head...)
	out = append(out, core.UserMessage{
		Role:    core.MessageRoleUser,
		Content: summary,
	})
	out = append(out, tail...)
	return out, true, nil
}

// ─── ChainedCompactor ──────────────────────────────────────────────────────

// ChainedCompactor runs a list of compactors in order. The first one to
// report changed=true wins; the rest are skipped. If none change anything,
// the original messages are returned unchanged.
type ChainedCompactor struct {
	Compactors []Compactor
}

func (c *ChainedCompactor) Name() string { return "chained" }

func (c *ChainedCompactor) Compact(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
	current := msgs
	for _, s := range c.Compactors {
		out, changed, err := s.Compact(ctx, current)
		if err != nil {
			return msgs, false, err
		}
		if changed {
			return out, true, nil
		}
	}
	return msgs, false, nil
}

// ─── Builder ───────────────────────────────────────────────────────────────

// NewChainedCompactorFunc builds a compaction function suitable for
// engine.CompactionConfig.Compactor from an ordered list of strategies.
func NewChainedCompactorFunc(compactors ...Compactor) func(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
	chained := &ChainedCompactor{Compactors: compactors}
	return func(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
		return chained.Compact(ctx, msgs)
	}
}
