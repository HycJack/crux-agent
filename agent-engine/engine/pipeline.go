package engine

import (
	"context"
	"fmt"
	"time"

	core "github.com/hycjack/crux-ai/core"
)

// ─── Stage interface ────────────────────────────────────────────────────────

// Stage defines an independent phase in the agent loop.
// This is the core building block of the Pipeline abstraction.
type Stage interface {
	// Name returns a human-readable name for this stage.
	Name() string

	// Run executes the stage. It receives the current RunState and returns
	// an updated RunState. Return an error to abort the entire pipeline.
	Run(ctx context.Context, state *RunState) (*RunState, error)
}

// ─── RunState ───────────────────────────────────────────────────────────────

// RunState carries the agent loop's mutable state across pipeline stages.
// It replaces the many local variables in the traditional runLoop/runInnerLoop.
type RunState struct {
	Messages     []core.Message
	SystemPrompt string
	Tools        []AgentTool

	TextBuffer string
	ToolCalls  []core.ToolCall
	StopReason core.StopReason
	Round      int
	MaxRounds  int
	Error      error

	// Metadata carries any extra data stages want to share.
	Metadata map[string]any
}

// ─── Hook ───────────────────────────────────────────────────────────────────

// Hook provides lifecycle callbacks around stage execution.
type Hook interface {
	// BeforeStage is called before each stage runs.
	BeforeStage(ctx context.Context, stageName string, state *RunState)

	// AfterStage is called after each stage runs.
	AfterStage(ctx context.Context, stageName string, state *RunState, err error)
}

// ─── Pipeline ───────────────────────────────────────────────────────────────

// Pipeline orchestrates multiple Stages in sequence with round-level looping.
//
// The pipeline runs all stages in order, then checks if it should continue
// to another round (when there are tool calls or steering messages). This
// replaces the runInnerLoop function for users who want custom stage ordering.
type Pipeline struct {
	stages    []Stage
	hooks     []Hook
	maxRounds int
}

// NewPipeline creates a pipeline with the given stages and options.
func NewPipeline(stages []Stage, opts ...PipelineOption) *Pipeline {
	p := &Pipeline{stages: stages, maxRounds: 50}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// PipelineOption configures a Pipeline.
type PipelineOption func(*Pipeline)

// WithHooks adds lifecycle hooks.
func WithHooks(hooks ...Hook) PipelineOption {
	return func(p *Pipeline) { p.hooks = append(p.hooks, hooks...) }
}

// WithMaxRounds sets the maximum number of rounds.
func WithMaxRounds(n int) PipelineOption {
	return func(p *Pipeline) { p.maxRounds = n }
}

// Run executes the pipeline until completion or error.
func (p *Pipeline) Run(ctx context.Context, initialState *RunState) (*RunState, error) {
	state := initialState
	if state == nil {
		state = &RunState{}
	}
	if state.MaxRounds <= 0 {
		state.MaxRounds = p.maxRounds
	}

	for state.Round < state.MaxRounds {
		if err := ctx.Err(); err != nil {
			return state, err
		}

		for _, stage := range p.stages {
			if err := ctx.Err(); err != nil {
				return state, err
			}

			for _, h := range p.hooks {
				h.BeforeStage(ctx, stage.Name(), state)
			}

			var err error
			state, err = stage.Run(ctx, state)

			for _, h := range p.hooks {
				h.AfterStage(ctx, stage.Name(), state, err)
			}

			if err != nil {
				state.Error = err
				return state, err
			}
		}

		if p.shouldStop(state) {
			break
		}
		state.Round++
	}

	if state.Round >= state.MaxRounds {
		state.Error = fmt.Errorf("agent: exceeded max rounds (%d)", state.MaxRounds)
	}
	return state, state.Error
}

func (p *Pipeline) shouldStop(state *RunState) bool {
	switch state.StopReason {
	case core.StopStop, core.StopError, core.StopAborted:
		return true
	}
	return len(state.ToolCalls) == 0
}

// ─── Built-in Stages ────────────────────────────────────────────────────────

// ContextCompactionStage runs pre-call compaction.
type ContextCompactionStage struct {
	Config *CompactionConfig
}

func (s *ContextCompactionStage) Name() string { return "compact" }

func (s *ContextCompactionStage) Run(ctx context.Context, state *RunState) (*RunState, error) {
	if s.Config == nil || s.Config.Compactor == nil {
		return state, nil
	}

	maxTokens := s.Config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 100000
	}
	reserveTokens := s.Config.ReserveTokens
	if reserveTokens < 0 {
		reserveTokens = 0
	}

	counter := s.Config.TokenCounter
	if counter == nil {
		counter = defaultTokenCounter
	}

	tokens := counter(state.SystemPrompt, state.Messages, toCoreTools(state.Tools))
	if tokens <= maxTokens-reserveTokens {
		return state, nil
	}

	prevTokens := tokens
	prevCount := len(state.Messages)
	newMsgs, changed, err := s.Config.Compactor(ctx, state.Messages)
	if err != nil || !changed {
		return state, nil
	}

	if s.Config.OnCompact != nil {
		newTokens := counter(state.SystemPrompt, newMsgs, toCoreTools(state.Tools))
		s.Config.OnCompact(prevTokens, newTokens, prevCount, len(newMsgs))
	}

	state.Messages = newMsgs
	return state, nil
}

// LLMInvocationStage calls the LLM and populates TextBuffer and ToolCalls.
type LLMInvocationStage struct {
	Config AgentLoopConfig
	Stream *AgentEventStream
}

func (s *LLMInvocationStage) Name() string { return "llm" }

func (s *LLMInvocationStage) Run(ctx context.Context, state *RunState) (*RunState, error) {
	llmMessages := convertToLLM(s.Config, state.Messages)
	opts := resolveStreamOptions(&s.Config)
	llmCtx := toContextMessages(llmMessages, s.Config.SystemPrompt, s.Config.Tools)

	llmStream, err := invokeStreamFn(ctx, s.Config, llmCtx, opts)
	if err != nil {
		if core.IsContextOverflow(err) {
			return s.retryWithCompaction(ctx, state, err)
		}
		return state, fmt.Errorf("agent: stream error: %w", err)
	}

	// Consume stream events
	partialMsg := core.AssistantMessage{
		Role:      core.MessageRoleAssistant,
		Timestamp: time.Now(),
	}
	if s.Stream != nil {
		s.Stream.Push(EventMessageStart{Message: partialMsg})
	}

	_, err = llmStream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		partialMsg = applyAssistantEvent(partialMsg, evt)
		if s.Stream != nil {
			s.Stream.Push(EventMessageUpdate{Message: partialMsg, AssistantEvent: evt})
		}
		return nil
	})
	if err != nil {
		if core.IsContextOverflow(err) {
			return s.retryWithCompaction(ctx, state, err)
		}
		return state, fmt.Errorf("agent: stream error: %w", err)
	}

	applyMessageToState(state, partialMsg)

	if s.Stream != nil {
		s.Stream.Push(EventMessageEnd{Message: partialMsg})
	}

	return state, nil
}

// retryWithCompaction attempts compaction-based recovery from a context overflow.
// On success, it updates state and emits EventMessageEnd; on failure it returns
// a wrapped error.
func (s *LLMInvocationStage) retryWithCompaction(ctx context.Context, state *RunState, cause error) (*RunState, error) {
	retried, retryErr := retryWithCompaction(ctx, s.Config, &state.Messages, s.Stream)
	if retryErr != nil {
		return state, fmt.Errorf("agent: stream error after compaction retry: %w (original: %w)", retryErr, cause)
	}
	applyMessageToState(state, retried)
	if s.Stream != nil {
		s.Stream.Push(EventMessageEnd{Message: retried})
	}
	return state, nil
}

// applyMessageToState copies an AssistantMessage's fields into RunState.
func applyMessageToState(state *RunState, msg core.AssistantMessage) {
	state.TextBuffer = messageText(msg)
	state.ToolCalls = extractToolCalls(msg)
	state.StopReason = msg.StopReason
	if state.Metadata == nil {
		state.Metadata = make(map[string]any)
	}
	state.Metadata["assistant_msg"] = msg
}

// ToolExecutionStage executes tool calls.
//
// IMPORTANT: This stage appends the assistant message to state.Messages BEFORE
// executing tools, so BeforeToolCall / AfterToolCall hooks see the complete
// message history (matching the semantics of runInnerLoop in loop.go).
// OutputStage is still available for custom pipelines that separate concerns.
type ToolExecutionStage struct {
	Config AgentLoopConfig
	Stream *AgentEventStream
}

func (s *ToolExecutionStage) Name() string { return "tools" }

func (s *ToolExecutionStage) Run(ctx context.Context, state *RunState) (*RunState, error) {
	assistantMsg, ok := state.Metadata["assistant_msg"].(core.AssistantMessage)
	if !ok {
		assistantMsg = core.AssistantMessage{
			Role: core.MessageRoleAssistant, StopReason: state.StopReason,
		}
	}

	// Append assistant message BEFORE tool execution so hooks see it.
	// Skip duplicate if already appended (e.g. OutputStage ran first).
	if !isLastMessage(state.Messages, assistantMsg) {
		state.Messages = append(state.Messages, assistantMsg)
	}

	if len(state.ToolCalls) == 0 {
		return state, nil
	}

	toolResults, shouldTerminate := executeToolCalls(ctx, s.Config, assistantMsg, state.ToolCalls, state.Messages, s.Stream)
	state.Messages = append(state.Messages, msgSlice(toolResults)...)
	if state.Metadata == nil {
		state.Metadata = make(map[string]any)
	}
	state.Metadata["tool_results"] = toolResults
	state.Metadata["should_terminate"] = shouldTerminate

	return state, nil
}

// isLastMessage checks whether msgs ends with the given assistant message.
func isLastMessage(msgs []core.Message, msg core.AssistantMessage) bool {
	if len(msgs) == 0 {
		return false
	}
	last, ok := msgs[len(msgs)-1].(core.AssistantMessage)
	if !ok {
		return false
	}
	return last.StopReason == msg.StopReason && len(last.Content) == len(msg.Content)
}

// OutputStage writes the assistant message back to the message list.
//
// NOTE: When used with ToolExecutionStage (the common case), the assistant
// message is already appended by ToolExecutionStage before tool execution.
// OutputStage is only needed in custom pipelines where the LLM call is
// separated from tool execution (e.g. compact -> llm -> output -> tools).
// To avoid duplicates, OutputStage checks if the message is already present.
type OutputStage struct{}

func (s *OutputStage) Name() string { return "output" }

func (s *OutputStage) Run(ctx context.Context, state *RunState) (*RunState, error) {
	assistant, ok := state.Metadata["assistant_msg"]
	if !ok {
		return state, nil
	}
	msg, ok := assistant.(core.AssistantMessage)
	if !ok {
		return state, nil
	}
	// Skip if already appended (dedup with ToolExecutionStage)
	if isLastMessage(state.Messages, msg) {
		return state, nil
	}
	state.Messages = append(state.Messages, msg)
	return state, nil
}

// messageText extracts the concatenated text content from an assistant message.
func messageText(msg core.AssistantMessage) string {
	var result string
	for _, block := range msg.Content {
		if tc, ok := block.(core.TextContent); ok {
			result += tc.Text
		}
	}
	return result
}
