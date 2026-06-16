// Demo: shows how to use crux-agent-runtime with crux-ai providers.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hycjack/crux-ai/core"

	"crux-agent-runtime/agent"

	// Register all built-in providers
	_ "github.com/hycjack/crux-ai/providers"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	model, err := getTestModel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "No API key found: %v\n", err)
		os.Exit(1)
	}

	echoTool := agent.AgentTool{
		Name:        "echo",
		Description: "Echoes back the input text",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		Execute: func(ctx context.Context, id string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
			var args struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(params, &args); err != nil {
				return agent.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "invalid args: " + err.Error()}},
					IsError: true,
				}, nil
			}
			return agent.AgentToolResult{
				Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Echo: " + args.Text}},
			}, nil
		},
	}

	a := agent.New(agent.AgentOptions{
		InitialState: &agent.AgentState{
			Model:        model,
			SystemPrompt: "You are a helpful assistant. Use the echo tool when asked to echo something.",
			Tools:        []agent.AgentTool{echoTool},
		},
	})

	a.Subscribe(func(evt agent.AgentEvent) {
		switch e := evt.(type) {
		case agent.EventMessageUpdate:
			if td, ok := e.AssistantEvent.(core.EventTextDelta); ok {
				fmt.Print(td.Delta)
			}
		case agent.EventToolExecStart:
			fmt.Fprintf(os.Stderr, "\n[tool:start] %s(%s)\n", e.ToolName, string(e.Args))
		case agent.EventToolExecEnd:
			fmt.Fprintf(os.Stderr, "[tool:end]   %s error=%v\n", e.ToolName, e.IsError)
		}
	})

	fmt.Println("Agent run starting...")
	result, err := a.Run(ctx,
		core.UserMessage{Role: "user", Content: "Please echo hello world", Timestamp: time.Now()},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n\nDone. Messages: %d\n", len(result))
}

func getTestModel() (core.Model, error) {
	providers := []struct {
		provider core.KnownProvider
		modelID  string
	}{
		{core.ProviderAnthropic, "claude-sonnet-4-20250514"},
		{core.ProviderOpenAI, "gpt-4o-mini"},
		{core.ProviderGoogle, "gemini-2.0-flash"},
	}
	for _, p := range providers {
		if core.GetEnvAPIKey(p.provider) != "" {
			return core.Model{
				ID:       p.modelID,
				Provider: p.provider,
				API:      getAPI(p.provider),
			}, nil
		}
	}
	return core.Model{}, fmt.Errorf("no API key found for any provider")
}

func getAPI(p core.KnownProvider) core.KnownAPI {
	switch p {
	case core.ProviderAnthropic:
		return core.APIAnthropicMessages
	case core.ProviderOpenAI:
		return core.APIOpenAICompletions
	case core.ProviderGoogle:
		return core.APIGoogleGenerative
	default:
		return core.APIOpenAICompletions
	}
}
