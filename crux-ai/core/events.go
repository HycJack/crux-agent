package core

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// EventStream is an async event stream for streaming LLM responses.
type EventStream[T any, R any] struct {
	ch     chan streamEvt[T]
	done   chan struct{}
	stop   chan struct{}
	result R
	err    error
	closed bool
	mu     sync.Mutex
}

type streamEvt[T any] struct {
	value T
	err   error
	done  bool
}

// Value returns the event payload. Zero on terminal events.
func (e streamEvt[T]) Value() T { return e.value }

// Err returns the terminal error if the stream failed. nil on success.
func (e streamEvt[T]) Err() error { return e.err }

// Done reports whether this event signals the end of the stream
// (after which no more events will be delivered).
func (e streamEvt[T]) Done() bool { return e.done }

// NewEventStream creates a new EventStream.
func NewEventStream[T any, R any]() *EventStream[T, R] {
	return &EventStream[T, R]{
		ch:   make(chan streamEvt[T], 64),
		done: make(chan struct{}),
		stop: make(chan struct{}),
	}
}

// Push sends an event to the stream.
func (s *EventStream[T, R]) Push(event T) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	stop := s.stop
	ch := s.ch
	s.mu.Unlock()

	select {
	case <-stop:
		return false
	case ch <- streamEvt[T]{value: event}:
		return true
	default:
		return false
	}
}

// End signals successful completion with a result.
func (s *EventStream[T, R]) End(result R) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.result = result
	select {
	case s.ch <- streamEvt[T]{done: true}:
	default:
	}
	close(s.ch)
	close(s.done)
}

// Error signals an error and terminates the stream.
func (s *EventStream[T, R]) Error(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.err = err
	select {
	case s.ch <- streamEvt[T]{err: err, done: true}:
	default:
	}
	close(s.ch)
	close(s.done)
}

// Stop signals the producer to stop sending events.
func (s *EventStream[T, R]) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.stop)
	}
}

// Result waits for the stream to complete and returns the final result.
func (s *EventStream[T, R]) Result() (R, error) {
	<-s.done
	return s.result, s.err
}

// Events returns a channel that yields stream events.
func (s *EventStream[T, R]) Events() <-chan streamEvt[T] {
	return s.ch
}

// ForEach iterates over all events in the stream, calling fn for each one.
func (s *EventStream[T, R]) ForEach(ctx context.Context, fn func(T) error) (R, error) {
	var zeroR R
	for {
		select {
		case <-ctx.Done():
			s.Stop()
			return zeroR, ctx.Err()
		case evt, ok := <-s.Events():
			if !ok {
				return s.Result()
			}
			if evt.done {
				if evt.err != nil {
					return zeroR, evt.err
				}
				return s.Result()
			}
			if err := fn(evt.value); err != nil {
				s.Stop()
				return zeroR, err
			}
		}
	}
}

// --- Streaming Events ---

// AssistantMessageEvent is the interface for all streaming events.
type AssistantMessageEvent interface {
	eventTag()
}

// EventStart signals the start of a streaming response.
type EventStart struct {
	Type      string        `json:"type"`
	API       KnownAPI      `json:"api"`
	Provider  KnownProvider `json:"provider"`
	Model     string        `json:"model"`
	Timestamp time.Time     `json:"timestamp"`
}

func (EventStart) eventTag() {}

// EventTextStart signals the start of a text block.
// ContentIndex disambiguates between multiple concurrent text blocks when
// the model interleaves text with other content. Index -1 means "unset".
type EventTextStart struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
}

func (EventTextStart) eventTag() {}

// EventTextDelta represents a text streaming delta.
type EventTextDelta struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

func (EventTextDelta) eventTag() {}

// EventTextEnd signals the end of a text block. TextSignature carries
// the provider's cryptographic signature for verification on replay.
type EventTextEnd struct {
	Type          string `json:"type"`
	ContentIndex  int    `json:"contentIndex"`
	Content       string `json:"content,omitempty"`
	TextSignature string `json:"textSignature,omitempty"`
}

func (EventTextEnd) eventTag() {}

// EventThinkingStart signals the start of a thinking block.
type EventThinkingStart struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
}

func (EventThinkingStart) eventTag() {}

// EventThinkingDelta represents a thinking streaming delta.
type EventThinkingDelta struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

func (EventThinkingDelta) eventTag() {}

// EventThinkingEnd signals the end of a thinking block.
type EventThinkingEnd struct {
	Type              string `json:"type"`
	ContentIndex      int    `json:"contentIndex"`
	Content           string `json:"content,omitempty"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
}

func (EventThinkingEnd) eventTag() {}

// EventToolCallStart signals the start of a tool call.
type EventToolCallStart struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	ID           string `json:"id"`
	Name         string `json:"name"`
}

func (EventToolCallStart) eventTag() {}

// EventToolCallDelta represents a tool call arguments delta.
type EventToolCallDelta struct {
	Type           string `json:"type"`
	ContentIndex   int    `json:"contentIndex"`
	ID             string `json:"id"`
	ArgumentsDelta string `json:"argumentsDelta"`
}

func (EventToolCallDelta) eventTag() {}

// EventToolCallEnd signals the end of a tool call. Arguments carries the
// fully assembled JSON arguments for the call.
type EventToolCallEnd struct {
	Type             string          `json:"type"`
	ContentIndex     int             `json:"contentIndex"`
	ID               string          `json:"id"`
	Arguments        json.RawMessage `json:"arguments"`
	ThoughtSignature string          `json:"thoughtSignature,omitempty"`
}

func (EventToolCallEnd) eventTag() {}

// EventDone signals successful completion. Reason explicitly indicates
// why the stream ended so consumers do not need to inspect Message.StopReason.
type EventDone struct {
	Type    string           `json:"type"`
	Reason  StopReason       `json:"reason"`
	Message AssistantMessage `json:"message"`
}

func (EventDone) eventTag() {}

// EventError signals an error.
type EventError struct {
	Type         string `json:"type"`
	ErrorMessage string `json:"errorMessage"`
}

func (EventError) eventTag() {}

// AssistantMessageEventStream is a type alias for the event stream.
type AssistantMessageEventStream = EventStream[AssistantMessageEvent, AssistantMessage]

// CalculateCost computes the cost of a request from per-million-token rates.
func CalculateCost(model Model, usage Usage) CostBreakdown {
	inputCost := float64(usage.Input) * model.Cost.Input / 1_000_000
	outputCost := float64(usage.Output) * model.Cost.Output / 1_000_000
	cacheReadCost := float64(usage.CacheRead) * model.Cost.CacheRead / 1_000_000
	cacheWriteCost := float64(usage.CacheWrite) * model.Cost.CacheWrite / 1_000_000

	return CostBreakdown{
		Input:      inputCost,
		Output:     outputCost,
		CacheRead:  cacheReadCost,
		CacheWrite: cacheWriteCost,
		Total:      inputCost + outputCost + cacheReadCost + cacheWriteCost,
	}
}
