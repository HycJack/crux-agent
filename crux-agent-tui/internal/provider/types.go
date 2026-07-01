// Package provider provides the core types used by the agent and openai provider.
// This replaces the crux-ai/core dependency with a self-contained set of types.
package provider

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// ---------- Roles ----------

type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
	RoleSystem    MessageRole = "system"
)

// ---------- StopReason ----------

type StopReason string

const (
	StopStop    StopReason = "stop"
	StopLength  StopReason = "length"
	StopToolUse StopReason = "toolUse"
	StopError   StopReason = "error"
	StopAborted StopReason = "aborted"
)

// ---------- ContentBlock ----------

type ContentBlock interface {
	ContentTag()
}

type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (TextContent) ContentTag() {}

type ToolCallContent struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (ToolCallContent) ContentTag() {}

// ---------- Message ----------

type Message struct {
	Role         MessageRole    `json:"role"`
	Content      string         `json:"content,omitempty"`
	ToolCallID   string         `json:"toolCallId,omitempty"`
	ToolName     string         `json:"toolName,omitempty"`
	IsError      bool           `json:"isError,omitempty"`
	ContentBlock []ContentBlock `json:"-"`
	StopReason   StopReason     `json:"-"`
	ErrorMessage string         `json:"-"`

	// Raw content blocks for assistant messages with tool calls
	ToolCalls []ToolCallContent `json:"-"`
}

// ---------- Tool ----------

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ---------- Context / Options ----------

type LLMContext struct {
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []Tool    `json:"tools,omitempty"`
}

type StreamOptions struct {
	APIKey     string
	BaseURL    string
	Model      string
	MaxTokens  int
	Timeout    time.Duration
	Headers    map[string]string
	SkipSystem bool // set to true for providers that use a "developer" role
}

// ---------- Streaming Events ----------

type StreamEvent interface {
	StreamEventTag()
}

type EventTextDelta struct {
	Delta string
}

func (EventTextDelta) StreamEventTag() {}

type EventToolCallStart struct {
	ID   string
	Name string
}

func (EventToolCallStart) StreamEventTag() {}

type EventToolCallDelta struct {
	ID   string
	Delta string
}

func (EventToolCallDelta) StreamEventTag() {}

type EventToolCallEnd struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

func (EventToolCallEnd) StreamEventTag() {}

type EventDone struct {
	StopReason StopReason
	Content    string
}

func (EventDone) StreamEventTag() {}

type EventError struct {
	Err error
}

func (EventError) StreamEventTag() {}

// ---------- EventStream ----------

type EventStream struct {
	ch     chan streamEvt
	done   chan struct{}
	stop   chan struct{}
	result string
	err    error
	closed bool
	mu     sync.Mutex
}

type streamEvt struct {
	event StreamEvent
	err   error
	done  bool
}

func NewEventStream() *EventStream {
	return &EventStream{
		ch:   make(chan streamEvt, 64),
		done: make(chan struct{}),
		stop: make(chan struct{}),
	}
}

func (s *EventStream) Push(event StreamEvent) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	ch := s.ch
	stop := s.stop
	s.mu.Unlock()

	select {
	case <-stop:
	case ch <- streamEvt{event: event}:
	}
}

func (s *EventStream) End(result string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.result = result
	select {
	case s.ch <- streamEvt{done: true}:
	default:
	}
	close(s.ch)
	close(s.done)
}

func (s *EventStream) Error(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.err = err
	select {
	case s.ch <- streamEvt{err: err, done: true}:
	default:
	}
	close(s.ch)
	close(s.done)
}

func (s *EventStream) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.stop)
	}
}

func (s *EventStream) Result() (string, error) {
	<-s.done
	return s.result, s.err
}

func (s *EventStream) ForEach(ctx context.Context, fn func(StreamEvent) error) (string, error) {
	for {
		select {
		case <-ctx.Done():
			s.Stop()
			return "", ctx.Err()
		case evt, ok := <-s.ch:
			if !ok {
				return s.Result()
			}
			if evt.done {
				if evt.err != nil {
					return "", evt.err
				}
				return s.Result()
			}
			if err := fn(evt.event); err != nil {
				s.Stop()
				return "", err
			}
		}
	}
}

// ---------- LLMProvider interface ----------

type LLMProvider interface {
	Stream(ctx context.Context, llmCtx LLMContext, opts StreamOptions) (*EventStream, error)
}
