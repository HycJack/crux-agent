package engine

import (
	"context"
	"fmt"
	"log"
	"sync"

	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-kernel/plugin"
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
	ProviderStreamFn    ProviderStreamFn
	SimpleStreamOptions core.SimpleStreamOptions

	// GetFollowUpMessages returns messages to inject after the current turn.
	GetFollowUpMessages func() []core.Message

	// Hooks 是统一扩展主干。若非空，buildConfig 会把 Hooks 交给 AgentLoopConfig；
	// 上方 legacy func 字段仍可用，经 config.hooks() 归一化到 Hooks。
	Hooks plugin.Hooks
}

// AgentOptions configures a new Agent.
type AgentOptions struct {
	InitialState *AgentState

	// Compaction configures automatic context-window compaction. If set,
	// the agent will run pre-call compaction whenever estimated tokens
	// exceed Compaction.MaxTokens, and will retry-with-compaction when
	// the LLM returns a context-overflow error.
	Compaction CompactionConfig
}

// Agent is a stateful wrapper around the agent loop.
type Agent struct {
	mu          sync.RWMutex
	state       AgentState
	compaction  CompactionConfig
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
	a.compaction = opts.Compaction
	return a
}

// ─── Runtime config mutation ────────────────────────────────────────────────

// SetCompaction overrides the compaction config at runtime.
func (a *Agent) SetCompaction(c CompactionConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.compaction = c
}

// Compaction returns the current compaction config (copy).
func (a *Agent) Compaction() CompactionConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.compaction
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

// SetSimpleStreamOptions updates the stream options (API key, base URL, etc.).
func (a *Agent) SetSimpleStreamOptions(opts core.SimpleStreamOptions) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.SimpleStreamOptions = opts
}

// SetProviderStreamFn sets or clears the ProviderStreamFn (two-layer events).
func (a *Agent) SetProviderStreamFn(fn ProviderStreamFn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.ProviderStreamFn = fn
}

// AttachHooks 注入统一扩展主干（plugin.Hooks）。
// 这是应用层装配插件能力的入口：ctx.Mount 聚合各插件 Hooks 后调用本方法。
func (a *Agent) AttachHooks(h plugin.Hooks) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Hooks = h
}

// Hooks 返回当前注入的扩展主干副本。
func (a *Agent) Hooks() plugin.Hooks {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Hooks
}

// ─── Message history management ─────────────────────────────────────────────

// Reset clears the message history so the next run starts fresh.
// System prompt, model, and tools are preserved.
func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = make([]core.Message, 0)
}

// SetMessages replaces the agent's message history with a deep copy.
func (a *Agent) SetMessages(msgs []core.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = copyMessageSlice(msgs)
}

// Messages returns a copy of the current message history.
func (a *Agent) Messages() []core.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return copyMessageSlice(a.state.Messages)
}

// copyMessageSlice returns a shallow copy of a []core.Message slice so the
// caller cannot mutate the agent's internal state through the backing array.
func copyMessageSlice(msgs []core.Message) []core.Message {
	if msgs == nil {
		return nil
	}
	out := make([]core.Message, len(msgs))
	copy(out, msgs)
	return out
}

// ─── Event subscription ─────────────────────────────────────────────────────

// Subscribe registers a listener for agent events.
func (a *Agent) Subscribe(fn func(AgentEvent)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.subscribers = append(a.subscribers, fn)
}

// ─── Steering / FollowUp ────────────────────────────────────────────────────

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

// Abort cancels the current run and automatically inserts error tool results
// for any outstanding tool calls that were interrupted.
//
// Reference: tau_agent/harness.py _append_interrupted_tool_results().
func (a *Agent) Abort() {
	a.mu.Lock()
	cancel := a.cancel
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// Append interrupted tool results to ensure message history is self-consistent.
	// Without this, the next request would contain assistant tool calls without
	// corresponding tool results, which most providers reject.
	a.mu.Lock()
	count := appendInterruptedToolResults(&a.state.Messages)
	a.mu.Unlock()
	if count > 0 {
		log.Printf("agent: auto-completed %d interrupted tool result(s)", count)
	}
}

// appendInterruptedToolResults finds all assistant tool calls in msgs without
// corresponding tool results and appends error stub results. Returns the number
// of stubs added. Operates on a pointer to the messages slice so changes are
// visible to the caller.
func appendInterruptedToolResults(msgs *[]core.Message) int {
	returnedIDs := make(map[string]bool)
	for _, msg := range *msgs {
		if tr, ok := msg.(core.ToolResultMessage); ok {
			returnedIDs[tr.ToolCallID] = true
		}
	}

	added := 0
	for _, msg := range *msgs {
		am, ok := msg.(core.AssistantMessage)
		if !ok || len(am.Content) == 0 {
			continue
		}
		for _, call := range extractToolCalls(am) {
			if returnedIDs[call.ID] {
				continue
			}
			returnedIDs[call.ID] = true
			*msgs = append(*msgs, core.ToolResultMessage{
				Role: core.MessageRoleTool, ToolCallID: call.ID, ToolName: call.Name,
				Content: []core.ContentBlock{
					core.TextContent{Type: "text", Text: "Tool call interrupted by user"},
				},
				IsError: true,
			})
			added++
		}
	}
	return added
}

// ─── Run / RunContinue ──────────────────────────────────────────────────────

// Run starts a new agent run with the given prompts.
func (a *Agent) Run(ctx context.Context, prompts ...core.Message) ([]core.Message, error) {
	a.mu.Lock()
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

	stateFollowUp := a.state.GetFollowUpMessages
	config.GetFollowUpMessages = func() []core.Message {
		a.mu.Lock()
		msgs := followUp
		followUp = nil
		a.mu.Unlock()
		if stateFollowUp != nil {
			msgs = append(msgs, stateFollowUp()...)
		}
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

	stateFollowUp := a.state.GetFollowUpMessages
	config.GetFollowUpMessages = func() []core.Message {
		a.mu.Lock()
		msgs := followUp
		followUp = nil
		a.mu.Unlock()
		if stateFollowUp != nil {
			msgs = append(msgs, stateFollowUp()...)
		}
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

// ─── Internal helpers ───────────────────────────────────────────────────────

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
		ProviderStreamFn:    a.state.ProviderStreamFn,
		GetFollowUpMessages: a.state.GetFollowUpMessages,
		Compaction:          a.compaction,
		Hooks:               a.state.Hooks,
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
