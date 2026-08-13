// Package fake provides a deterministic, replay-based mock provider for testing.
//
// Design (inspired by tau_ai's FakeProvider):
//
//   - Accepts pre-recorded event streams during construction
//   - Returns streams in FIFO order (each Stream() call pops the next stream)
//   - Records all invocation parameters for assertion in tests
//   - Supports both the ProviderEvent and AssistantMessageEvent levels
//
// This is the "tape recorder" pattern: tests specify exact event sequences
// to exercise specific agent-loop code paths (tool calls, thinking blocks,
// errors, retries, empty responses, etc.) without making real API calls.
//
// Reference: tau_ai fake.py (Pi coding-agent harness).
package fake

import (
	"context"
	"sync"
	"time"

	core "github.com/hycjack/crux-ai/core"
)

// Provider is a deterministic mock provider that replays pre-recorded event
// streams. It implements core.APIProvider.
type Provider struct {
	mu sync.Mutex

	// AssistantMessage event streams, consumed in FIFO order.
	assistantStreams []*core.AssistantMessageEventStream

	// Provider-level event streams, canonicalized on pop.
	providerStreams  []*core.ProviderEventStream
	canonicalizeAPI  core.KnownAPI
	canonicalizeProv core.KnownProvider
	canonicalizeModel string

	// Recorded call history.
	calls []CallRecord

	// When true, each Stream/StreamSimple call that has no pre-recorded stream
	// returns an auto-reply "stop" response instead of an empty stream.
	autoReplay bool
}

// CallRecord captures one invocation of Stream/StreamSimple.
type CallRecord struct {
	Model   core.Model
	Context core.Context
	Options interface{} // core.StreamOptions or core.SimpleStreamOptions
}

// New creates a fake Provider with no pre-recorded streams.
// Use WithStreams or WithProviderStreams to add them.
func New() *Provider {
	return &Provider{}
}

// NewFromAssistantStreams creates a fake Provider pre-populated with
// AssistantMessageEvent streams.
func NewFromAssistantStreams(streams ...*core.AssistantMessageEventStream) *Provider {
	return &Provider{
		assistantStreams: streams,
	}
}

// NewFromProviderStreams creates a fake Provider pre-populated with
// ProviderEvent streams. Each stream is canonicalized via
// CanonicalizeProviderStream before being returned.
func NewFromProviderStreams(
	api core.KnownAPI,
	provider core.KnownProvider,
	model string,
	streams ...*core.ProviderEventStream,
) *Provider {
	return &Provider{
		providerStreams:   streams,
		canonicalizeAPI:   api,
		canonicalizeProv:  provider,
		canonicalizeModel: model,
	}
}

// Stream implements core.APIProvider.
func (p *Provider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.AssistantMessageEventStream, error) {
	return p.nextAssistantStream(model, llmCtx, opts)
}

// StreamSimple implements core.APIProvider.
func (p *Provider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.AssistantMessageEventStream, error) {
	return p.nextAssistantStream(model, llmCtx, opts)
}

// Calls returns the recorded call history. The slice is a snapshot.
func (p *Provider) Calls() []CallRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]CallRecord, len(p.calls))
	copy(result, p.calls)
	return result
}

// HasCalls returns true if at least one Stream/StreamSimple call was recorded.
func (p *Provider) HasCalls() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls) > 0
}

// Clear resets the call history and stream queues.
func (p *Provider) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = nil
	p.assistantStreams = nil
	p.providerStreams = nil
}

// AddAssistantStream appends one or more AssistantMessageEvent streams.
func (p *Provider) AddAssistantStream(streams ...*core.AssistantMessageEventStream) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.assistantStreams = append(p.assistantStreams, streams...)
}

// AddProviderStream appends one or more ProviderEvent streams.
func (p *Provider) AddProviderStream(streams ...*core.ProviderEventStream) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.providerStreams = append(p.providerStreams, streams...)
}

// SetAutoReplay enables auto-replay mode. When true, every Stream call returns
// a minimal "stop" response when no pre-recorded streams remain.
func (p *Provider) SetAutoReplay(enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.autoReplay = enabled
}

// nextAssistantStream pops and returns the next stream, or creates an
// auto-replay or empty stream if none remain.
func (p *Provider) nextAssistantStream(model core.Model, llmCtx core.Context, opts interface{}) (*core.AssistantMessageEventStream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Record the call
	p.calls = append(p.calls, CallRecord{
		Model:   model,
		Context: llmCtx,
		Options: opts,
	})

	// Pop from provider streams (canonicalize on demand)
	if len(p.providerStreams) > 0 {
		ps := p.providerStreams[0]
		p.providerStreams = p.providerStreams[1:]
		return core.CanonicalizeProviderStream(
			ps,
			p.canonicalizeAPI,
			p.canonicalizeProv,
			p.canonicalizeModel,
		), nil
	}

	// Pop from pre-recorded assistant streams
	if len(p.assistantStreams) > 0 {
		s := p.assistantStreams[0]
		p.assistantStreams = p.assistantStreams[1:]
		return s, nil
	}

	// Auto-replay: return a minimal "stop" response
	if p.autoReplay {
		return newAutoReplayStream(model), nil
	}

	// No streams left: return empty stream that immediately ends with stop
	return newEmptyStream(model), nil
}

// newAutoReplayStream creates a stream that emits a basic "stop" response.
func newAutoReplayStream(model core.Model) *core.AssistantMessageEventStream {
	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()
	go func() {
		msg := core.AssistantMessage{
			Role:       "assistant",
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			StopReason: core.StopStop,
			Timestamp:  time.Now(),
		}
		stream.Push(core.EventStart{
			Type:      "start",
			API:       model.API,
			Provider:  model.Provider,
			Model:     model.ID,
			Timestamp: time.Now(),
		})
		stream.Push(core.EventTextStart{Type: "text_start"})
		stream.Push(core.EventTextDelta{Type: "text_delta", Delta: "autoreplay "})
		stream.Push(core.EventTextDelta{Type: "text_delta", Delta: "response"})
		stream.Push(core.EventTextEnd{Type: "text_end"})
		stream.Push(core.EventDone{Type: "done", Message: msg})
		stream.End(msg)
	}()
	return stream
}

// newEmptyStream creates a stream that immediately signals "stop" with no content.
func newEmptyStream(model core.Model) *core.AssistantMessageEventStream {
	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()
	go func() {
		msg := core.AssistantMessage{
			Role:       "assistant",
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			StopReason: core.StopStop,
			Timestamp:  time.Now(),
		}
		stream.Push(core.EventStart{
			Type:      "start",
			API:       model.API,
			Provider:  model.Provider,
			Model:     model.ID,
			Timestamp: time.Now(),
		})
		stream.Push(core.EventDone{Type: "done", Reason: core.StopStop, Message: msg})
		stream.End(msg)
	}()
	return stream
}

// MustCollect is a synchronous helper: it calls Stream in a goroutine and
// collects all events into a slice. Useful in test assertions:
//
//	events := fake.MustCollect(ctx, fakeProv, model, context, opts)
//	assert.Equal(t, "text_delta", events[2].(core.EventTextDelta).Type)
func MustCollect(
	ctx context.Context,
	p *Provider,
	model core.Model,
	llmCtx core.Context,
	opts core.StreamOptions,
) []core.AssistantMessageEvent {
	stream, err := p.Stream(ctx, model, llmCtx, opts)
	if err != nil {
		panic(err)
	}
	var events []core.AssistantMessageEvent
	_, _ = stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		events = append(events, evt)
		return nil
	})
	return events
}
