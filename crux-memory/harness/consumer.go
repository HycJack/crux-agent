// Package harness adapts crux-memory to the crux-harness hooks.Consumer
// interface. Wire it into hooks.Fanout exactly like AuditConsumer or
// BudgetConsumer — no cruxd main.go changes required.
//
// The consumer buffers assistant stream chunks per session and flushes
// them to L0 on EventStreamDone. User messages are captured via
// EventTurnStart + the session's existing message log (or via an
// explicit POST to memoryd's /v1/capture if a tighter integration is
// needed).
//
// Usage:
//
//	fanout := hooks.NewFanout()
//	fanout.Add(harness.NewConsumer(pipeline, log.Default()))
//
// The consumer is non-blocking: it enqueues capture on the pipeline and
// returns Decision{} immediately. The LLM stages run asynchronously in the
// pipeline's own goroutines.
package harness

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/hycjack/crux-harness/hooks"

	"github.com/crux-memory/crux-memory/l0"
	"github.com/crux-memory/crux-memory/pipeline"
)

// Consumer implements hooks.Consumer by forwarding turn events into a
// crux-memory pipeline.
type Consumer struct {
	p *pipeline.Pipeline

	mu     sync.Mutex
	buf    map[string]string // sessionID → accumulated assistant stream
}

// NewConsumer wires a Consumer to a pipeline.
func NewConsumer(p *pipeline.Pipeline) *Consumer {
	return &Consumer{p: p, buf: make(map[string]string)}
}

// Name implements hooks.Consumer.
func (c *Consumer) Name() string { return "crux-memory" }

// OnEvent implements hooks.Consumer.
//
// Event flow:
//   turn_start → reset stream buffer
//   stream_chunk → append to buffer
//   stream_done → flush buffer to L0 as assistant message + async tick
//   turn_end → noop (stream_done already flushed)
func (c *Consumer) OnEvent(ctx context.Context, e hooks.Event) hooks.Decision {
	switch e.Type {
	case hooks.EventTurnStart:
		c.mu.Lock()
		delete(c.buf, e.SessionID)
		c.mu.Unlock()

	case hooks.EventStreamChunk:
		if e.SessionID == "" || e.Content == "" {
			return hooks.Decision{}
		}
		c.mu.Lock()
		c.buf[e.SessionID] += e.Content
		c.mu.Unlock()

	case "stream_done":
		c.mu.Lock()
		full := strings.TrimSpace(c.buf[e.SessionID])
		delete(c.buf, e.SessionID)
		c.mu.Unlock()
		if full == "" || e.SessionID == "" {
			return hooks.Decision{}
		}
		if err := c.p.Capture(ctx, e.SessionID, l0.RoleAssistant, full); err != nil {
			log.Printf("[crux-memory] capture assistant session=%s: %v", e.SessionID, err)
			return hooks.Decision{}
		}
		// Async tick — never block the agent loop.
		go func() {
			if err := c.p.MaybeTick(ctx); err != nil {
				log.Printf("[crux-memory] tick: %v", err)
			}
		}()
	}
	return hooks.Decision{}
}

// CaptureUser is a helper callers can invoke from cruxd wiring to record
// the raw user message at turn start. It is not strictly necessary (the
// conversation is reconstructable from session logs), but capturing early
// means a crash mid-turn still preserves the user prompt.
func (c *Consumer) CaptureUser(ctx context.Context, sessionID, content string) error {
	if sessionID == "" || content == "" {
		return nil
	}
	return c.p.Capture(ctx, sessionID, l0.RoleUser, content)
}