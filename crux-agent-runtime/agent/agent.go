package agent

import (
	"context"
	"fmt"
	"sync"

	core "crux-ai/core"
)

// AgentState holds the agent's mutable state.
type AgentState struct {
	Model         core.Model
	SystemPrompt  string
	Messages      []core.Message
	Tools         []AgentTool
	ToolExecution ToolExecutionMode

	// Options forwarded to AgentLoopConfig
	ConvertToLlm        func([]core.Message) []core.Message
	TransformContext    func([]core.Message) []core.Message
	GetApiKey           func() string
	ShouldStopAfterTurn func(core.AssistantMessage, []core.ToolResultMessage) bool
	PrepareNextTurn     func(*AgentLoopConfig, core.AssistantMessage, []core.ToolResultMessage, []core.Message)
	BeforeToolCall      func(BeforeToolCallContext) *ToolCallBlock
	AfterToolCall       func(AfterToolCallContext) *ToolCallOverride
	StreamFn            StreamFn
	SimpleStreamOptions core.SimpleStreamOptions
}

// AgentOptions configures a new Agent.
type AgentOptions struct {
	InitialState *AgentState
}

// Agent is a stateful wrapper around the agent loop.
type Agent struct {
	mu          sync.RWMutex
	state       AgentState
	subscribers []func(AgentEvent)
	steering    []core.Message
	followUp    []core.Message
	cancel      context.CancelFunc
	streamWg    sync.WaitGroup
}

// New creates a new Agent.
func New(opts AgentOptions) *Agent {
	a := &Agent{}
	if opts.InitialState != nil {
		a.state = *opts.InitialState
	}
	if a.state.Messages == nil {
		a.state.Messages = make([]core.Message, 0)
	}
	return a
}

// State returns a copy of the agent's current state.
func (a *Agent) State() AgentState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

// SetTools updates the agent's tools.
func (a *Agent) SetTools(tools []AgentTool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Tools = tools
}

// SetModel updates the agent's model.
func (a *Agent) SetModel(model core.Model) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Model = model
}

// SetSystemPrompt updates the system prompt.
func (a *Agent) SetSystemPrompt(prompt string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.SystemPrompt = prompt
}

// Messages returns the current message history.
func (a *Agent) Messages() []core.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Messages
}

// Subscribe registers a listener for agent events.
func (a *Agent) Subscribe(fn func(AgentEvent)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.subscribers = append(a.subscribers, fn)
}

// Steering injects messages that will be processed in the current turn.
func (a *Agent) Steering(msgs ...core.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steering = append(a.steering, msgs...)
}

// FollowUp injects messages that will be processed after the current turn.
func (a *Agent) FollowUp(msgs ...core.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUp = append(a.followUp, msgs...)
}

// Abort cancels the current run.
func (a *Agent) Abort() {
	a.mu.Lock()
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Run starts a new agent run with the given prompts.
func (a *Agent) Run(ctx context.Context, prompts ...core.Message) ([]core.Message, error) {
	a.mu.Lock()
	// 把 prompts 拼接到当前历史前，先复制以避免后续 processStream 再次 append 导致重复
	baseMessages := append([]core.Message{}, a.state.Messages...)
	baseMessages = append(baseMessages, prompts...)

	runCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	config := a.buildConfig()

	steering := a.steering
	a.steering = nil
	followUp := a.followUp
	a.followUp = nil
	a.mu.Unlock()

	config.GetSteeringMessages = func() []core.Message {
		a.mu.Lock()
		msgs := steering
		steering = nil
		a.mu.Unlock()
		return msgs
	}
	config.GetFollowUpMessages = func() []core.Message {
		a.mu.Lock()
		msgs := followUp
		followUp = nil
		a.mu.Unlock()
		return msgs
	}

	stream := AgentLoop(runCtx, baseMessages, config)
	a.processStream(runCtx, stream)

	result, err := stream.Result()
	a.streamWg.Wait()
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.state.Messages = result
	a.cancel = nil
	a.mu.Unlock()

	return result, nil
}

// RunContinue resumes the agent from its current message history.
func (a *Agent) RunContinue(ctx context.Context) ([]core.Message, error) {
	a.mu.Lock()

	runCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	config := a.buildConfig()

	steering := a.steering
	a.steering = nil
	followUp := a.followUp
	a.followUp = nil

	messages := make([]core.Message, len(a.state.Messages))
	copy(messages, a.state.Messages)
	a.mu.Unlock()

	config.GetSteeringMessages = func() []core.Message {
		a.mu.Lock()
		msgs := steering
		steering = nil
		a.mu.Unlock()
		return msgs
	}
	config.GetFollowUpMessages = func() []core.Message {
		a.mu.Lock()
		msgs := followUp
		followUp = nil
		a.mu.Unlock()
		return msgs
	}

	stream := AgentLoopContinue(runCtx, config, messages)
	a.processStream(runCtx, stream)

	result, err := stream.Result()
	a.streamWg.Wait()
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.state.Messages = result
	a.cancel = nil
	a.mu.Unlock()

	return result, nil
}

// buildConfig creates an AgentLoopConfig from the agent's state.
func (a *Agent) buildConfig() AgentLoopConfig {
	return AgentLoopConfig{
		SimpleStreamOptions: a.state.SimpleStreamOptions,
		Model:               a.state.Model,
		SystemPrompt:        a.state.SystemPrompt,
		Tools:               a.state.Tools,
		ToolExecution:       a.state.ToolExecution,
		ConvertToLlm:        a.state.ConvertToLlm,
		TransformContext:    a.state.TransformContext,
		GetApiKey:           a.state.GetApiKey,
		ShouldStopAfterTurn: a.state.ShouldStopAfterTurn,
		PrepareNextTurn:     a.state.PrepareNextTurn,
		BeforeToolCall:      a.state.BeforeToolCall,
		AfterToolCall:       a.state.AfterToolCall,
		StreamFn:            a.state.StreamFn,
	}
}

// processStream subscribes to the event stream and forwards events to subscribers.
func (a *Agent) processStream(ctx context.Context, stream *AgentEventStream) {
	a.mu.RLock()
	subs := make([]func(AgentEvent), len(a.subscribers))
	copy(subs, a.subscribers)
	a.mu.RUnlock()

	a.streamWg.Add(1)

	go func() {
		defer a.streamWg.Done()
		_, err := stream.ForEach(ctx, func(evt AgentEvent) error {
			for _, fn := range subs {
				func() {
					defer func() {
						if r := recover(); r != nil {
							fmt.Printf("agent: subscriber panic: %v\n", r)
						}
					}()
					fn(evt)
				}()
			}
			return nil
		})
		if err != nil {
			fmt.Printf("agent: stream error: %v\n", err)
		}
	}()
}
