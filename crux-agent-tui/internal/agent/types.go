// Package agent implements a self-contained agent runtime for the TUI.
// It replaces the dependency on crux-agent-runtime with a local implementation.
package agent

import (
	"context"
	"encoding/json"

	"crux-agent-tui/internal/provider"
)

// AgentEvent is the interface for all agent streaming events.
type AgentEvent interface {
	agentEventTag()
}

type EventMessageUpdate struct {
	Type  string
	Delta string
}

func (EventMessageUpdate) agentEventTag() {}

type EventToolExecStart struct {
	ToolName string
	Args     string
	ToolID   string
}

func (EventToolExecStart) agentEventTag() {}

type EventToolExecEnd struct {
	ToolName string
	Result   string
	IsError  bool
	ToolID   string
}

func (EventToolExecEnd) agentEventTag() {}

type EventTurnEnd struct {
	ErrorMessage string
}

func (EventTurnEnd) agentEventTag() {}

// AgentTool defines a tool that the agent can call.
type AgentTool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Execute     ToolExecuteFunc
}

type ToolExecuteFunc func(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (AgentToolResult, error)

type AgentToolResult struct {
	Content string
	IsError bool
}

// AgentState holds the agent's mutable state.
type AgentState struct {
	Model        string
	BaseURL      string
	APIKey       string
	SystemPrompt string
	Messages     []provider.Message
	Tools        []AgentTool
	MaxTokens    int
	Headers      map[string]string
}

// CompactionConfig configures context compaction.
type CompactionConfig struct {
	MaxTokens   int
	TokenBudget int // estimated tokens per message
}

// Agent is a stateful agent that runs the LLM loop.
type Agent struct {
	state       AgentState
	compaction  CompactionConfig
	subscribers []func(AgentEvent)
	provider    provider.LLMProvider
	cancel      context.CancelFunc
}

// New creates a new Agent.
func New(state AgentState, prov provider.LLMProvider, opts ...CompactionConfig) *Agent {
	a := &Agent{
		state:    state,
		provider: prov,
	}
	if a.state.Messages == nil {
		a.state.Messages = make([]provider.Message, 0)
	}
	if len(opts) > 0 {
		a.compaction = opts[0]
	}
	return a
}

func (a *Agent) State() AgentState {
	return a.state
}

func (a *Agent) Subscribe(fn func(AgentEvent)) {
	a.subscribers = append(a.subscribers, fn)
}

func (a *Agent) Abort() {
	if a.cancel != nil {
		a.cancel()
	}
}

func (a *Agent) Reset() {
	a.state.Messages = make([]provider.Message, 0)
}

// Compact forces a context compaction.
func (a *Agent) Compact() {
	a.maybeCompact()
}

// Run starts the agent with the given user message.
func (a *Agent) Run(ctx context.Context, content string) ([]provider.Message, error) {
	runCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	defer cancel()

	// Add user message
	a.state.Messages = append(a.state.Messages, provider.Message{
		Role:    provider.RoleUser,
		Content: content,
	})

	// Run the agent loop
	err := a.runLoop(runCtx)
	if err != nil {
		return a.state.Messages, err
	}

	return a.state.Messages, nil
}

// PublishEvent publishes an event to all subscribers (used by TUI).
func (a *Agent) PublishEvent(evt AgentEvent) {
	for _, fn := range a.subscribers {
		fn(evt)
	}
}
