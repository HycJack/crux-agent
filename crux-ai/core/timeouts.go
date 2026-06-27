// Package core provides streaming timeout controls.
//
// Reference: oh-my-pi (packages/ai/src/utils/idle-iterator.ts) design:
//
//	Two-phase timeout:
//	  1. firstEventTimeout: time from stream start to first event
//	  2. idleTimeout: maximum interval between events
//
// Heartbeat/keepalive events are distinguished via isProgressItem 鈥?// they do not reset the idle timer, preventing "server sends heartbeats
// but the model is actually stuck" scenarios.
package core

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ============================================================================
// Timeout Configuration
// ============================================================================

// StreamTimeoutConfig defines the two-phase timeout strategy for streaming requests.
//
// Typical behavior:
//
//	Timeline:  |-- firstEventTimeout --|---- idleTimeout ----|---- idleTimeout ----|-->
//	Events:    ...(waiting)...         EventStart  TextDelta  TextDelta  ...    Done
type StreamTimeoutConfig struct {
	// FirstEventTimeout is the timeout from stream start to the first event.
	// Set to 0 or negative to disable. Default: 100s.
	FirstEventTimeout time.Duration

	// IdleTimeout is the maximum interval between consecutive events.
	// Set to 0 or negative to disable. Default: 120s.
	IdleTimeout time.Duration

	// IsProgressItem determines if an event counts as "progress".
	// Progress events reset the idle timer; non-progress events (e.g. heartbeats) do not.
	// When nil, all events count as progress.
	IsProgressItem func(item any) bool
}

// DefaultStreamTimeoutConfig returns sensible default timeout configuration for most LLM providers.
func DefaultStreamTimeoutConfig() StreamTimeoutConfig {
	return StreamTimeoutConfig{
		FirstEventTimeout: 100 * time.Second,
		IdleTimeout:       120 * time.Second,
		IsProgressItem:    nil,
	}
}

// TimeoutError represents a timeout error.
type TimeoutError struct {
	Kind    string // "first_event" or "idle"
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("stream %s timeout after %v", e.Kind, e.Timeout)
}

// IsTimeoutError checks whether an error is a timeout error.
func IsTimeoutError(err error) bool {
	var te *TimeoutError
	return errors.As(err, &te)
}

// ============================================================================
// EventStream With Timeout
//
// StreamWithTimeout wraps an EventStream with automatic two-phase timeout control.
// Usage:
//
//	stream := NewEventStreamWithTimeout(rawStream, timeoutConfig)
//	msg, err := stream.ForEach(ctx, func(evt AssistantMessageEvent) error {
//	    fmt.Println(evt)
//	    return nil
//	})
//
// Differences from raw EventStream.ForEach:
//   1. First-event timeout: errors if first event takes too long
//   2. Idle timeout: errors if gap between events is too long
//   3. Heartbeat/keepalive events are not treated as "progress"
// ============================================================================

// StreamWithTimeout wraps an EventStream with timeout controls.
type StreamWithTimeout[T any, R any] struct {
	stream *EventStream[T, R]
	config StreamTimeoutConfig
}

// NewEventStreamWithTimeout creates a timeout middleware.
func NewEventStreamWithTimeout[T any, R any](
	stream *EventStream[T, R],
	config StreamTimeoutConfig,
) *StreamWithTimeout[T, R] {
	return &StreamWithTimeout[T, R]{
		stream: stream,
		config: config,
	}
}

// ForEach iterates over events with automatic two-phase timeout control.
// Returns the final AssistantMessage (same behavior as raw EventStream.ForEach).
//
// Note: This method already includes timeout control. Do NOT call both
// StreamWithTimeout.ForEach and raw EventStream.ForEach on the same EventStream.
func (w *StreamWithTimeout[T, R]) ForEach(ctx context.Context, handler func(T) error) (R, error) {
	firstEventReceived := false
	lastProgress := time.Now()

	for {
		var timeoutCh <-chan time.Time
		if !firstEventReceived && w.config.FirstEventTimeout > 0 {
			timeoutCh = time.After(w.config.FirstEventTimeout)
		} else if firstEventReceived && w.config.IdleTimeout > 0 {
			elapsed := time.Since(lastProgress)
			remaining := w.config.IdleTimeout - elapsed
			if remaining <= 0 {
				var zero R
				return zero, &TimeoutError{Kind: "idle", Timeout: w.config.IdleTimeout}
			}
			timeoutCh = time.After(remaining)
		}

		select {
		case <-ctx.Done():
			w.stream.Stop()
			var zero R
			return zero, ctx.Err()

		case <-timeoutCh:
			kind := "idle"
			timeout := w.config.IdleTimeout
			if !firstEventReceived {
				kind = "first_event"
				timeout = w.config.FirstEventTimeout
			}
			w.stream.Stop()
			var zero R
			return zero, &TimeoutError{Kind: kind, Timeout: timeout}

		case evt, ok := <-w.stream.Events():
			if !ok {
				return w.stream.Result()
			}
			if evt.done {
				if evt.err != nil {
					var zero R
					return zero, evt.err
				}
				return w.stream.Result()
			}

			if !firstEventReceived {
				firstEventReceived = true
				lastProgress = time.Now()
			}

			if w.config.IsProgressItem == nil || w.config.IsProgressItem(evt.value) {
				lastProgress = time.Now()
			}

			if err := handler(evt.value); err != nil {
				w.stream.Stop()
				var zero R
				return zero, err
			}
		}
	}
}

// ============================================================================
// Race Middleware (Promise.race pattern)
//
// Reference: oh-my-pi (packages/ai/src/utils/idle-iterator.ts) design:
//   Timeout, cancellation, and data signals are raced via Promise.race.
//   In Go we implement this via select, but provide a more structured approach.
//
// Race unifies timeout, cancellation, and data signal handling.
// Each Next() call returns a RaceResult indicating which signal won.
// ============================================================================

// RaceConfig configures the race.
type RaceConfig struct {
	// PerEventTimeout is the maximum wait between events.
	// Each Next() call restarts this timer, so it acts as an "inter-event idle timeout".
	// 0 means unlimited.
	PerEventTimeout time.Duration
	// Abort is an optional cancellation signal channel.
	Abort <-chan struct{}
}

// Race performs race-condition iteration over an EventStream.
type Race[T any, R any] struct {
	stream *EventStream[T, R]
	config RaceConfig
}

// RaceResult represents the outcome of a race.
type RaceResult[T any] struct {
	Item  T
	Kind  RaceKind
	Error error
}

// RaceKind indicates which signal won the race.
type RaceKind int

const (
	RaceEvent   RaceKind = iota // Normal data event
	RaceTimeout                 // Timeout
	RaceDone                    // Stream completed normally
	RaceAbort                   // Interrupted
	RaceError                   // Stream error
)

// NewRace creates a race iterator.
func NewRace[T any, R any](stream *EventStream[T, R], config RaceConfig) *Race[T, R] {
	return &Race[T, R]{stream: stream, config: config}
}

// Next waits for the next result, returning which signal won.
// Each call creates a fresh PerEventTimeout timer, so the timeout applies
// to the interval between successive Next() calls.
// Can be called repeatedly until RaceDone or RaceError is returned.
func (r *Race[T, R]) Next(ctx context.Context) RaceResult[T] {
	var timeoutCh <-chan time.Time
	if r.config.PerEventTimeout > 0 {
		timeoutCh = time.After(r.config.PerEventTimeout)
	}

	var abortCh <-chan struct{}
	if r.config.Abort != nil {
		abortCh = r.config.Abort
	}

	select {
	case <-ctx.Done():
		r.stream.Stop()
		return RaceResult[T]{Kind: RaceAbort, Error: ctx.Err()}

	case <-timeoutCh:
		r.stream.Stop()
		return RaceResult[T]{Kind: RaceTimeout}

	case <-abortCh:
		r.stream.Stop()
		return RaceResult[T]{Kind: RaceAbort}

	case evt, ok := <-r.stream.Events():
		if !ok {
			_, err := r.stream.Result()
			if err != nil {
				return RaceResult[T]{Kind: RaceError, Error: err}
			}
			return RaceResult[T]{Kind: RaceDone}
		}
		if evt.done {
			if evt.err != nil {
				return RaceResult[T]{Kind: RaceError, Error: evt.err}
			}
			return RaceResult[T]{Kind: RaceDone}
		}
		return RaceResult[T]{Item: evt.value, Kind: RaceEvent}
	}
}

// ============================================================================
// Convenience: Stream With Timeout
// ============================================================================

// StreamWithTimeoutFn executes a streaming LLM request and returns a
// StreamWithTimeout-wrapped stream.
//
// Usage:
//
//	timeoutCfg := DefaultStreamTimeoutConfig()
//	timeoutCfg.FirstEventTimeout = 30 * time.Second
//	timeoutCfg.IdleTimeout = 60 * time.Second
//
//	wrapped, err := StreamWithTimeoutFn(ctx, timeoutCfg, func(ctx) (*EventStream[AssistantMessageEvent, AssistantMessage], error) {
//	    return provider.Stream(ctx, model, llmCtx, opts)
//	})
//	if err != nil { ... }
//	msg, err := wrapped.ForEach(ctx, handler)
func StreamWithTimeoutFn[T AssistantMessageEvent, R any](
	ctx context.Context,
	timeoutCfg StreamTimeoutConfig,
	streamFn func(context.Context) (*EventStream[T, R], error),
) (*StreamWithTimeout[T, R], error) {
	innerStream, err := streamFn(ctx)
	if err != nil {
		return nil, err
	}
	wrapped := NewEventStreamWithTimeout(innerStream, timeoutCfg)
	return wrapped, nil
}

// ============================================================================
// Timeout Config Helper Methods
// ============================================================================

// WithFirstEventTimeout sets the first-event timeout and returns a copy.
func (c StreamTimeoutConfig) WithFirstEventTimeout(d time.Duration) StreamTimeoutConfig {
	c.FirstEventTimeout = d
	return c
}

// WithIdleTimeout sets the idle timeout and returns a copy.
func (c StreamTimeoutConfig) WithIdleTimeout(d time.Duration) StreamTimeoutConfig {
	c.IdleTimeout = d
	return c
}
