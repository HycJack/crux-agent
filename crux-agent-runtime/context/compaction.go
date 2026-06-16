package context

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/hycjack/crux-ai/core"
)

// Compactor is a strategy that takes a message slice and returns a
// (possibly shorter) replacement. It MUST be safe to call from a single
// goroutine; concurrency is the caller's responsibility.
// || 压缩策略接口
type Compactor interface {
	// Compact returns a replacement slice. If it returns
	// (msgs, false), the caller should keep using the original
	// (no compaction needed). If (newMsgs, true), the caller
	// should adopt newMsgs.
	Compact(ctx context.Context, msgs []core.Message) (newMsgs []core.Message, changed bool, err error)

	// Name identifies the strategy (for telemetry / debugging).
	Name() string
}

// ----------------------------------------------------------------------------
// SlideWindow
// ----------------------------------------------------------------------------

// SlideWindow keeps the system prompt + last N messages.
// || 滑动窗口策略：保留系统提示 + 最近 N 条消息
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
	// Find the system message index (if any). Convention: 0 = system.
	start := 0
	if len(msgs) > 0 {
		switch msgs[0].(type) {
		case core.UserMessage:
			// Check if it's a system prompt disguised as user message
			if um, ok := msgs[0].(core.UserMessage); ok {
				_ = um // Keep as is
			}
		}
	}
	tail := msgs[len(msgs)-(s.MaxMessages-start):]
	out := append([]core.Message{}, msgs[:start]...)
	out = append(out, tail...)
	return out, true, nil
}

// ----------------------------------------------------------------------------
// LLM Summarize
// ----------------------------------------------------------------------------

// LLMSummarize drops the oldest messages and replaces them with a single
// "summary" message. The summary is produced by calling Summarize on the
// dropped messages. If Summarize is nil, the dropped messages are simply
// dropped and a placeholder is inserted.
// || LLM 摘要策略：用 LLM 生成摘要替换旧消息
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

	// Calls counts how many times Summarize was invoked (for tests).
	Calls atomic.Int64
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

	// Split: head (system) + middle (drop) + tail (keep)
	head := msgs[:1] // assume msgs[0] is system; if not, we'll fix up below
	start := 0
	if len(msgs) > 0 {
		switch msgs[0].(type) {
		case core.UserMessage:
			// Not a system message
			head = nil
			start = 0
		default:
			start = 1
		}
	}
	dropped := msgs[start : len(msgs)-keepLast]
	tail := msgs[len(msgs)-keepLast:]

	var summary string
	if l.Summarize != nil {
		l.Calls.Add(1)
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

// ----------------------------------------------------------------------------
// ChainedCompactor — try strategies in order, fall through on skip
// ----------------------------------------------------------------------------

// ChainedCompactor runs a list of compactors in order. The first one
// to report `changed=true` wins; the rest are skipped. If none change
// anything, the original messages are returned unchanged.
// || 链式压缩器：按顺序尝试多个策略
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

// ----------------------------------------------------------------------------
// ContextWindowCompactor — token-aware compaction
// ----------------------------------------------------------------------------

// ContextWindowCompactor automatically compacts messages when they
// exceed the context window limit.
// || 上下文窗口压缩器：自动压缩超出窗口的消息
type ContextWindowCompactor struct {
	Config       ContextWindowConfig
	Counter      TokenCounter
	Inner        Compactor // Inner compactor to use when triggered
	SystemPrompt string
	Tools        []core.Tool
}

// NewContextWindowCompactor creates a new context window compactor.
func NewContextWindowCompactor(config ContextWindowConfig, inner Compactor) *ContextWindowCompactor {
	return &ContextWindowCompactor{
		Config: config,
		Counter: config.TokenCounter,
		Inner:   inner,
	}
}

func (c *ContextWindowCompactor) Name() string { return "context_window" }

func (c *ContextWindowCompactor) SetContext(systemPrompt string, tools []core.Tool) {
	c.SystemPrompt = systemPrompt
	c.Tools = tools
}

func (c *ContextWindowCompactor) Compact(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
	counter := c.Counter
	if counter == nil {
		counter = DefaultTokenCounter
	}

	// Check if compaction is needed.
	if !NeedsCompaction(counter, c.SystemPrompt, msgs, c.Tools, c.Config) {
		return msgs, false, nil
	}

	// Use inner compactor.
	if c.Inner != nil {
		return c.Inner.Compact(ctx, msgs)
	}

	// Fallback: simple truncation.
	availableTokens := c.Config.MaxTokens - c.Config.ReserveTokens
	keepMessages := estimateKeepMessages(counter, c.SystemPrompt, msgs, c.Tools, availableTokens, c.Config.MinMessages)

	if keepMessages >= len(msgs) {
		return msgs, false, nil
	}

	// Keep system prompt + last N messages.
	out := msgs[len(msgs)-keepMessages:]
	return out, true, nil
}

// estimateKeepMessages estimates how many messages we can keep.
func estimateKeepMessages(counter TokenCounter, systemPrompt string, msgs []core.Message, tools []core.Tool, availableTokens, minMessages int) int {
	// Start from the end and count backwards.
	tokens := 0
	keep := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		msgTokens := messageTokenCount(msgs[i]) / 4
		if tokens+msgTokens > availableTokens {
			break
		}
		tokens += msgTokens
		keep++
	}
	if keep < minMessages {
		keep = minMessages
	}
	return keep
}
