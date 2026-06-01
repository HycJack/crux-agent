// Package agent wires up the coding agent with tools and configuration.
package agent

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"crux-agent-chat/config"
	"crux-agent-chat/tools"
	agentruntime "crux-agent-runtime/agent"
	"crux-ai/core"
)

// NewCodingAgent creates a fully configured coding agent from the config.
func NewCodingAgent(cfg *config.Config) *agentruntime.Agent {
	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = config.DefaultSystemPrompt
	}

	wd, _ := os.Getwd()
	systemPrompt += fmt.Sprintf("\n\nCurrent working directory: %s", wd)
	systemPrompt += fmt.Sprintf("\nCurrent time: %s", time.Now().Format(time.RFC3339))
	systemPrompt += fmt.Sprintf("\nOperating system: %s/%s", runtime.GOOS, runtime.GOARCH)

	model := cfg.GetModel()
	model.Headers = make(map[string]string)

	agentTools := tools.AllTools()

	return agentruntime.New(agentruntime.AgentOptions{
		InitialState: &agentruntime.AgentState{
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
		},
	})
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
