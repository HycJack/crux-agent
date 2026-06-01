// Package agent wires up the coding agent with tools and configuration.
package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"crux-ai/core"
	runtime "crux-agent-runtime/agent"
	"crux-agent-chat/config"
	"crux-agent-chat/tools"
)

// NewCodingAgent creates a fully configured coding agent from the config.
func NewCodingAgent(cfg *config.Config) *runtime.Agent {
	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = config.DefaultSystemPrompt
	}

	wd, _ := os.Getwd()
	systemPrompt += fmt.Sprintf("\n\nCurrent working directory: %s", wd)

	model := cfg.GetModel()
	model.Headers = make(map[string]string)

	agentTools := tools.AllTools()

	return runtime.New(runtime.AgentOptions{
		InitialState: &runtime.AgentState{
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

// RunOnce runs a single user query through the agent.
func RunOnce(ctx context.Context, a *runtime.Agent, prompt string) ([]core.Message, error) {
	return a.Run(ctx, core.UserMessage{
		Role:      "user",
		Content:   prompt,
		Timestamp: time.Now(), // Fix #2: set timestamp
	})
}
