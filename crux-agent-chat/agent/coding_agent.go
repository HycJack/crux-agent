// Package agent wires up the coding agent with tools and configuration.
package agent

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"crux-agent-chat/config"
	"crux-agent-chat/harness"
	"crux-agent-chat/tools"
	"crux-agent-harness/approval"
	agentruntime "crux-agent-runtime/agent"
	"crux-ai/core"
)

// Options configures NewCodingAgentWithHarness.
type Options struct {
	Config  *config.Config
	Harness *harness.Harness
}

// NewCodingAgent creates a fully configured coding agent from the config.
func NewCodingAgent(cfg *config.Config) *agentruntime.Agent {
	return NewCodingAgentWithHarness(Options{Config: cfg})
}

// NewCodingAgentWithHarness creates a coding agent that integrates with
// the harness (approval, skills, context, session, token tracking).
//
// If opts.Harness is nil, falls back to the standalone behavior
// (no approval, no skills, no compaction).
func NewCodingAgentWithHarness(opts Options) *agentruntime.Agent {
	cfg := opts.Config
	h := opts.Harness

	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = config.DefaultSystemPrompt
	}

	wd, _ := os.Getwd()
	systemPrompt += fmt.Sprintf("\n\nCurrent working directory: %s", wd)
	systemPrompt += fmt.Sprintf("\nCurrent time: %s", time.Now().Format(time.RFC3339))
	systemPrompt += fmt.Sprintf("\nOperating system: %s/%s", runtime.GOOS, runtime.GOARCH)

	if h != nil {
		// Append a skills section so the model knows about the
		// specialized instructions it can use.
		systemPrompt = h.AppendSkillsToPrompt(systemPrompt)
	}

	model := cfg.GetModel()
	model.Headers = make(map[string]string)

	agentTools := tools.AllTools()

	state := &agentruntime.AgentState{
		Model:        model,
		SystemPrompt: systemPrompt,
		Tools:        agentTools,
		GetApiKey: func() string {
			return cfg.APIKey
		},
		SimpleStreamOptions: core.SimpleStreamOptions{
			StreamOptions: core.StreamOptions{
				APIKey: cfg.APIKey,
			},
		},
	}

	if h != nil {
		state.BeforeToolCall = func(ctx agentruntime.BeforeToolCallContext) *agentruntime.ToolCallBlock {
			return approvalHook(h, ctx)
		}
	}

	return agentruntime.New(agentruntime.AgentOptions{InitialState: state})
}

// approvalHook bridges the harness's approval gate to the agent's
// BeforeToolCall hook. It blocks tools when the gate says so, or
// delegates the ask to the harness (which prompts on stdin).
func approvalHook(h *harness.Harness, ctx agentruntime.BeforeToolCallContext) *agentruntime.ToolCallBlock {
	req := approval.Request{
		ToolName: ctx.ToolCall.Name,
		ToolID:   ctx.ToolCall.ID,
		Args:     ctx.Args,
	}
	res := h.EvaluateApproval(req)
	switch res.Decision {
	case approval.DecisionBlock:
		return &agentruntime.ToolCallBlock{Block: true, Reason: res.Reason}
	case approval.DecisionAsk:
		if !h.AskUser(req) {
			return &agentruntime.ToolCallBlock{Block: true, Reason: "user denied"}
		}
	}
	return nil
}

// RunOnce runs a single user query through the agent. The prompt may be a
// plain string (text-only) or a slice of core.ContentBlock (multimodal,
// e.g. text + image attachments).
func RunOnce(ctx context.Context, a *agentruntime.Agent, prompt any) ([]core.Message, error) {
	return a.Run(ctx, core.UserMessage{
		Role:      "user",
		Content:   prompt,
		Timestamp: time.Now(), // Fix #2: set timestamp
	})
}
