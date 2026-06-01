package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	_ "crux-ai/providers"

	"crux-agent-chat/agent"
	"crux-agent-chat/config"
	"crux-agent-chat/ui"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		ui.PrintError("Configuration error: %v", err)
		fmt.Println("\nCreate a .env file with your API key, for example:")
		fmt.Println("  ANTHROPIC_API_KEY=sk-ant-...")
		fmt.Println("  OPENAI_API_KEY=sk-...")
		fmt.Println("  DEEPSEEK_API_KEY=sk-...")
		os.Exit(1)
	}

	ui.PrintBanner(string(cfg.Provider), cfg.ModelID)

	// Create coding agent
	a := agent.NewCodingAgent(cfg)
	a.Subscribe(ui.Subscriber())

	// REPL loop
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0), 1024*1024) // 1MB buffer

	for {
		fmt.Print(ui.FormatUserPrompt())

		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Handle commands
		if strings.HasPrefix(input, "/") {
			switch strings.ToLower(input) {
			case "/quit", "/exit":
				fmt.Println("Goodbye! 👋")
				return
			case "/clear":
				// Fix #1: Create a fresh agent to truly clear history
				a = agent.NewCodingAgent(cfg)
				a.Subscribe(ui.Subscriber())
				ui.PrintInfo("Conversation cleared.")
				continue
			case "/help":
				ui.PrintHelp()
				continue
			case "/tools":
				ui.PrintTools()
				continue
			default:
				ui.PrintError("Unknown command: %s (type /help for help)", input)
				continue
			}
		}

		ui.PrintSeparator()

		_, err := agent.RunOnce(ctx, a, input)
		if err != nil {
			ui.PrintError("Agent error: %v", err)
		}

		fmt.Println()
	}
}
