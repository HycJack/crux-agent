package turn

import (
	"context"
)

// ChannelTrigger is an in-process Trigger implementation.
// Use for tests and single-process deployments.
type ChannelTrigger struct {
	ch chan Event
}

// NewChannelTrigger creates a channel-based trigger.
func NewChannelTrigger() *ChannelTrigger {
	return &ChannelTrigger{ch: make(chan Event, 16)}
}

// Wait blocks until an event is fired.
func (t *ChannelTrigger) Wait(ctx context.Context) (Event, error) {
	select {
	case ev := <-t.ch:
		return ev, nil
	case <-ctx.Done():
		return Event{}, ctx.Err()
	}
}

// Fire signals the trigger (non-blocking).
func (t *ChannelTrigger) Fire(event Event) error {
	select {
	case t.ch <- event:
		return nil
	default:
		return fmtErr("trigger channel full")
	}
}

// fmtErr is a tiny helper to avoid importing fmt in this minimal file.
func fmtErr(s string) error {
	return &triggerError{s}
}

type triggerError struct{ s string }

func (e *triggerError) Error() string { return e.s }

// Compile-time check
var _ Trigger = (*ChannelTrigger)(nil)