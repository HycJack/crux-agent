// Command testagent tests the coding agent with a simple echo tool.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"crux-agent-chat/config"
	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/crux-ai/core"
	_ "github.com/hycjack/crux-ai/providers"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	echoTool := engine.AgentTool{
		Name:        "echo",
		Description: "Echoes back the input text",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		Execute: func(ctx context.Context, id string, params json.RawMessage, onUpdate func(json.RawMessage)) (engine.AgentToolResult, error) {
			var args struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(params, &args); err != nil {
				return engine.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "invalid args: " + err.Error()}},
					IsError: true,
				}, nil
			}
			return engine.AgentToolResult{
				Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Echo: " + args.Text}},
			}, nil
		},
	}

	a := engine.New(engine.AgentOptions{
		InitialState: &engine.AgentState{
			Model:        cfg.GetModel(),
			SystemPrompt: "You are a helpful assistant. When asked to echo, use the echo tool. Always summarize tool results in a short sentence.",
			Tools:        []engine.AgentTool{echoTool},
			GetApiKey:    func() string { return cfg.APIKey },
			SimpleStreamOptions: core.SimpleStreamOptions{
				StreamOptions: core.StreamOptions{APIKey: cfg.APIKey},
			},
		},
	})

	turnCount := 0
	a.Subscribe(func(evt engine.AgentEvent) {
		switch e := evt.(type) {
		case engine.EventTurnStart:
			turnCount++
			fmt.Fprintf(os.Stderr, "\n=== TURN %d ===\n", turnCount)
		case engine.EventMessageUpdate:
			switch evt := e.AssistantEvent.(type) {
			case core.EventStart:
				fmt.Fprintf(os.Stderr, "[llm:start] api=%s model=%s\n", evt.API, evt.Model)
			case core.EventTextDelta:
				fmt.Print(evt.Delta)
			case core.EventThinkingDelta:
				fmt.Fprintf(os.Stderr, "[think]%s", evt.Delta)
			case core.EventDone:
				fmt.Fprintf(os.Stderr, "\n[llm:done] stop=%s blocks=%d\n", evt.Message.StopReason, len(evt.Message.Content))
			case core.EventError:
				fmt.Fprintf(os.Stderr, "\n[llm:ERROR] %s\n", evt.ErrorMessage)
			}
		case engine.EventToolExecStart:
			fmt.Fprintf(os.Stderr, "[tool:start] %s\n", e.ToolName)
		case engine.EventToolExecEnd:
			s := "✓"
			if e.IsError {
				s = "✗"
			}
			fmt.Fprintf(os.Stderr, "[tool:end] %s %s\n", s, e.ToolName)
		case engine.EventTurnEnd:
			fmt.Fprintf(os.Stderr, "=== END TURN %d (toolResults=%d) ===\n", turnCount, len(e.ToolResults))
		}
	})

	fmt.Fprintf(os.Stderr, "Config: %s\n", cfg.String())
	fmt.Fprintf(os.Stderr, "Test: echo tool call\n")
	fmt.Print("\nAgent: ")

	result, err := a.Run(ctx,
		core.UserMessage{Role: "user", Content: "Please echo: hello world", Timestamp: time.Now()},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n❌ Error: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "\n\n--- Final Messages ---\n")
	for i, m := range result {
		switch msg := m.(type) {
		case core.UserMessage:
			fmt.Fprintf(os.Stderr, "[%d] User: %v\n", i, msg.Content)
		case core.AssistantMessage:
			text := ""
			for _, b := range msg.Content {
				if tc, ok := b.(core.TextContent); ok {
					text += tc.Text
				}
				if tc, ok := b.(core.ToolCall); ok {
					text += fmt.Sprintf(" [tool:%s]", tc.Name)
				}
			}
			errMsg := ""
			if msg.ErrorMessage != "" {
				errMsg = fmt.Sprintf(" err=%q", msg.ErrorMessage)
			}
			fmt.Fprintf(os.Stderr, "[%d] Assistant(stop=%s): %q%s\n", i, msg.StopReason, text, errMsg)
		case core.ToolResultMessage:
			fmt.Fprintf(os.Stderr, "[%d] ToolResult[%s]\n", i, msg.ToolName)
		}
	}
}
