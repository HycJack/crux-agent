// Package agent provides a thin wrapper around agent-engine/engine.Agent
// for use in the TUI. It re-exports engine types so that the UI layer
// imports a single local package instead of engine directly.
//
// The wiring mirrors agent-engine/cmd/demo-agent: the caller resolves a
// core.Model up front (via ai.GetModel / an "openai-compatible" virtual
// provider), passes core.SimpleStreamOptions (API key, temperature, headers),
// and the engine's built-in StreamFn streams through crux-ai's compat/router.
// Context compaction uses defaults.NewMessageCounter (tiktoken-based) instead
// of the previous crude character estimate.
package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hycjack/agent-engine/defaults"
	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/crux-ai/ai"
	core "github.com/hycjack/crux-ai/core"
)

// ─── Type aliases ──────────────────────────────────────────────────────────

// AgentEvent re-exports engine.AgentEvent so the UI layer handles engine
// event types directly.
type AgentEvent = engine.AgentEvent

// EventMessageUpdate re-exports for convenience.
type EventMessageUpdate = engine.EventMessageUpdate

// EventToolExecStart re-exports for convenience.
type EventToolExecStart = engine.EventToolExecStart

// EventToolExecEnd re-exports for convenience.
type EventToolExecEnd = engine.EventToolExecEnd

// EventTurnStart re-exports for convenience.
type EventTurnStart = engine.EventTurnStart

// EventTurnEnd re-exports for convenience.
type EventTurnEnd = engine.EventTurnEnd

// AgentTool re-exports engine.AgentTool.
type AgentTool = engine.AgentTool

// AgentToolResult re-exports engine.AgentToolResult.
type AgentToolResult = engine.AgentToolResult

// ToolExecuteFunc re-exports engine.ToolExecuteFunc.
type ToolExecuteFunc = engine.ToolExecuteFunc

// StreamFn describes the LLM provider streaming function.
type StreamFn = engine.StreamFn

// ─── Model resolution ──────────────────────────────────────────────────────

// OpenAICompatibleProvider is the TUI's virtual provider id. When the
// configured provider equals this value, the agent skips ai.GetModel's
// KnownProvider lookup and builds a core.Model that speaks the OpenAI
// protocol (APIOpenAICompletions) against an arbitrary BASE_URL.
const OpenAICompatibleProvider = "openai-compatible"

// ResolveModel turns a (provider, modelID, baseURL) triple into a core.Model.
// Known providers go through ai.GetModel; the "openai-compatible" virtual
// provider is built on the spot so any OpenAI-compatible gateway works.
//
// Mirrors demo-agent's resolveModel.
func ResolveModel(provider, modelID, baseURL string) (core.Model, error) {
	if provider == OpenAICompatibleProvider {
		if modelID == "" {
			return core.Model{}, errors.New("openai-compatible requires a model id (AI_MODEL)")
		}
		return core.Model{
			ID:       modelID,
			Name:     modelID,
			API:      core.APIOpenAICompletions,
			Provider: core.ProviderOpenAI,
			BaseURL:  baseURL,
			Input:    []core.Modality{core.ModalityText},
		}, nil
	}
	m, err := ai.GetModel(core.KnownProvider(provider), modelID)
	if err != nil {
		return core.Model{}, err
	}
	if baseURL != "" {
		m.BaseURL = baseURL
	}
	return m, nil
}

// ─── Agent ─────────────────────────────────────────────────────────────────

// Agent is a thin wrapper around engine.Agent that provides the same
// public API the TUI expects.
type Agent struct {
	inner *engine.Agent

	// Cached config used for token estimation in ContextInfo.
	modelID      string
	systemPrompt string
	tools        []AgentTool
}

// AgentConfig holds all configuration for creating a new Agent.
//
// Model is a fully-resolved core.Model (from ai.GetModel or the
// openai-compatible mapping). SimpleStreamOptions carries the API key,
// temperature, max tokens and per-request headers.
type AgentConfig struct {
	Model               core.Model
	SystemPrompt        string
	Tools               []AgentTool
	Headers             map[string]string
	APIKey              string
	Temperature         float64
	MaxTokens           int
	StreamFn            StreamFn
	CompactionMaxTokens int // soft context budget; 0 = no compaction
}

// New creates a new Agent wrapping engine.Agent.
func New(cfg AgentConfig) *Agent {
	state := engine.AgentState{
		Model:         cfg.Model,
		SystemPrompt:  cfg.SystemPrompt,
		Tools:         cfg.Tools,
		StreamFn:      cfg.StreamFn,
		ToolExecution: engine.ToolExecSequential,
		SimpleStreamOptions: core.SimpleStreamOptions{
			StreamOptions: core.StreamOptions{
				APIKey:  cfg.APIKey,
				Headers: cfg.Headers,
			},
		},
	}
	if cfg.MaxTokens > 0 {
		mt := cfg.MaxTokens
		state.SimpleStreamOptions.MaxTokens = &mt
	}
	if cfg.Temperature > 0 {
		t := cfg.Temperature
		state.SimpleStreamOptions.Temperature = &t
	}

	opts := engine.AgentOptions{InitialState: &state}

	if cfg.CompactionMaxTokens > 0 {
		// tiktoken-based token counting via defaults.MessageCounter, matching
		// demo-agent's compaction setup.
		opts.Compaction = engine.CompactionConfig{
			MaxTokens:     cfg.CompactionMaxTokens,
			ReserveTokens: 4096,
			TokenCounter: func(systemPrompt string, messages []core.Message, tools []core.Tool) int {
				mc, err := defaults.NewMessageCounter(cfg.Model.ID)
				if err != nil {
					return estimateTokens(systemPrompt, messages)
				}
				return mc.EstimateRequestTokens(systemPrompt, messages, tools).Total
			},
			Compactor: func(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
				compacted := compactMessages(msgs)
				return compacted, len(compacted) < len(msgs), nil
			},
		}
	}

	return &Agent{
		inner:        engine.New(opts),
		modelID:      cfg.Model.ID,
		systemPrompt: cfg.SystemPrompt,
		tools:        cfg.Tools,
	}
}

// Subscribe registers a listener for agent events.
func (a *Agent) Subscribe(fn func(AgentEvent)) {
	a.inner.Subscribe(fn)
}

// Run starts (or resumes) the agent with a user prompt.
func (a *Agent) Run(ctx context.Context, prompt string) ([]core.Message, error) {
	msg := core.UserMessage{
		Role:      core.MessageRoleUser,
		Content:   prompt,
		Timestamp: time.Now(),
	}
	return a.inner.Run(ctx, msg)
}

// RunContinue resumes the agent from its current state.
func (a *Agent) RunContinue(ctx context.Context) ([]core.Message, error) {
	return a.inner.RunContinue(ctx)
}

// Abort cancels the current run.
func (a *Agent) Abort() {
	a.inner.Abort()
}

// Reset clears the message history.
func (a *Agent) Reset() {
	a.inner.Reset()
}

// SetModel updates the model used by the agent.
func (a *Agent) SetModel(model core.Model) {
	a.inner.SetModel(model)
	a.modelID = model.ID
}

// SetTools updates the tool list used by the agent.
func (a *Agent) SetTools(tools []AgentTool) {
	a.inner.SetTools(tools)
	a.tools = tools
}

// SetSystemPrompt updates the system prompt used by the agent.
func (a *Agent) SetSystemPrompt(prompt string) {
	a.inner.SetSystemPrompt(prompt)
	a.systemPrompt = prompt
}

// SetMessages replaces the message history (used after compaction).
func (a *Agent) SetMessages(msgs []core.Message) {
	a.inner.SetMessages(msgs)
}

// Compact forces context compaction by applying the sliding-window compactor
// to the current message history (if present). If compaction is not configured
// it is a no-op. Mirrors demo-agent's compaction callback.
func (a *Agent) Compact() {
	if a.inner.Compaction().Compactor == nil {
		return
	}
	msgs := a.inner.Messages()
	compacted, _, err := a.inner.Compaction().Compactor(context.Background(), msgs)
	if err == nil && len(compacted) < len(msgs) {
		a.inner.SetMessages(compacted)
	}
}

// MessageCount returns the number of messages in history.
func (a *Agent) MessageCount() int {
	return len(a.inner.Messages())
}

// ContextInfo returns a formatted string with context usage info (estimated
// tokens in the current message history). Returns "" if no messages are present
// or token counting is unavailable.
func (a *Agent) ContextInfo() string {
	msgs := a.inner.Messages()
	if len(msgs) == 0 {
		return ""
	}

	tools := make([]core.Tool, len(a.tools))
	for i, t := range a.tools {
		tools[i] = core.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}

	total := estimateTokens(a.systemPrompt, msgs)
	if mc, err := defaults.NewMessageCounter(a.modelID); err == nil {
		total = mc.EstimateRequestTokens(a.systemPrompt, msgs, tools).Total
	}

	return fmt.Sprintf("~%dk tokens", (total+500)/1000)
}

// ─── Compaction helpers ────────────────────────────────────────────────────

// estimateTokens is a lightweight fallback used only when tiktoken cannot
// initialise. Mirrors the previous character-based estimate.
func estimateTokens(systemPrompt string, messages []core.Message) int {
	const tokensPerChar = 0.25
	total := int(float64(len(systemPrompt)) * tokensPerChar)
	for _, msg := range messages {
		switch m := msg.(type) {
		case core.UserMessage:
			if s, ok := m.Content.(string); ok {
				total += int(float64(len(s)) * tokensPerChar)
			}
		case core.AssistantMessage:
			total += int(float64(len(textOfBlocks(m.Content))) * tokensPerChar)
		case core.ToolResultMessage:
			total += int(float64(len(textOfBlocks(m.Content))) * tokensPerChar)
		}
		total += 4
	}
	return total
}

func textOfBlocks(blocks []core.ContentBlock) string {
	var sb []byte
	for _, b := range blocks {
		if t, ok := b.(core.TextContent); ok {
			sb = append(sb, t.Text...)
		}
	}
	return string(sb)
}

func compactMessages(messages []core.Message) []core.Message {
	if len(messages) <= 10 {
		return messages
	}
	keep := 8
	preserved := make([]core.Message, 0, keep+2)
	if len(messages) > 2 {
		preserved = append(preserved, messages[0])
	}
	start := len(messages) - keep
	if start < 0 {
		start = 0
	}
	preserved = append(preserved, messages[start:]...)
	return preserved
}

// ─── Helper for tool result creation ───────────────────────────────────────

// ToolResult creates a successful tool result.
func ToolResult(text string) AgentToolResult {
	return AgentToolResult{
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: text}},
	}
}

// ToolError creates an error tool result.
func ToolError(text string) AgentToolResult {
	return AgentToolResult{
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: text}},
		IsError: true,
	}
}
