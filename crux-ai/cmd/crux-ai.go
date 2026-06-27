// crux-ai is a simple CLI demonstrating the crux-ai library.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hycjack/crux-ai/ai"
	"github.com/hycjack/crux-ai/core"

	// Force provider registration via init()
	_ "github.com/hycjack/crux-ai/providers"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	switch os.Args[1] {
	case "models":
		handleModels()
	case "providers":
		handleProviders()
	case "complete":
		if len(os.Args) < 4 {
			fmt.Fprintf(os.Stderr, "Usage: crux-ai complete <provider> <model> [prompt]\n")
			os.Exit(1)
		}
		prompt := "Hello!"
		if len(os.Args) > 4 {
			prompt = os.Args[4]
		}
		handleComplete(ctx, os.Args[2], os.Args[3], prompt)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: crux-ai <command>")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  models                              List available models")
	fmt.Println("  providers                           List registered API providers")
	fmt.Println("  complete <provider> <model> [prompt] Run a completion")
}

func handleModels() {
	for _, provider := range ai.GetProviders() {
		fmt.Printf("── %s ──\n", provider)
		for _, m := range ai.GetModels(provider) {
			fmt.Printf("  %-40s %s\n", m.ID, m.Name)
		}
	}
}

func handleProviders() {
	for _, api := range core.GetRegisteredProviders() {
		fmt.Printf("  %s\n", api)
	}
}

func handleComplete(ctx context.Context, providerID, modelID, prompt string) {
	provider := core.KnownProvider(providerID)
	model, err := ai.GetModel(provider, modelID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	msgs := []core.Message{
		core.UserMessage{Role: "user", Content: prompt, Timestamp: time.Now()},
	}

	stream, err := ai.StreamSimple(ctx, model, msgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	result, err := stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		switch e := evt.(type) {
		case core.EventTextDelta:
			fmt.Print(e.Delta)
		case core.EventThinkingDelta:
			fmt.Fprintf(os.Stderr, "[thinking] %s", e.Delta)
		}
		return nil
	})

	fmt.Println()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\nTokens: %d in / %d out\n", result.Usage.Input, result.Usage.Output)
}
