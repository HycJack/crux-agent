// Command xiaomi-demo is a minimal end-to-end demo of the Xiaomi
// MiMo provider wired into crux-ai. It reads XIAOMI_API_KEY from
// the environment, calls the bundled mimo-v2.5-pro model with a
// Chinese greeting, and prints the response.
//
// Run:
//
//	XIAOMI_API_KEY=tp-xxx go run ./cmd/xiaomi-demo
//
// Or with a custom prompt:
//
//	XIAOMI_API_KEY=tp-xxx go run ./cmd/xiaomi-demo -prompt "用三句话介绍北京"
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/llm"
	"github.com/hycjack/crux-ai/providers/compat"
	"github.com/hycjack/crux-ai/providers/xiaomi"
)

func main() {
	prompt := flag.String("prompt", "用一句话介绍你自己。", "user message to send to Xiaomi MiMo")
	modelID := flag.String("model", "mimo-v2.5-pro", "model id (default: mimo-v2.5-pro)")
	flag.Parse()

	apiKey := os.Getenv("XIAOMI_API_KEY")
	if apiKey == "" {
		log.Fatal("XIAOMI_API_KEY not set; export it before running")
	}

	// Register the Xiaomi provider on the compat router, then register
	// the compat router against the OpenAI-completions API.
	compatRouter := compat.NewRouter()
	compatRouter.Register(xiaomi.New())

	core.RegisterProvider(core.APIOpenAICompletions, compatRouter)

	// Construct the model.
	model := core.Model{
		ID:       *modelID,
		Provider: core.ProviderXiaomi,
		API:      core.APIOpenAICompletions,
		BaseURL:  "https://token-plan-cn.xiaomimimo.com/v1",
	}

	// Build the request.
	messages := []core.Message{
		core.UserMessage{
			Role:      core.MessageRoleUser,
			Content:   *prompt,
			Timestamp: time.Now(),
		},
	}

	// Call the LLM.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	result, err := llm.Complete(ctx, model, messages, core.StreamOptions{
		APIKey: apiKey,
	})
	if err != nil {
		log.Fatalf("Complete: %v", err)
	}
	elapsed := time.Since(start)

	fmt.Println("────────────────────────────────────────")
	fmt.Printf("model:     %s (%s)\n", model.ID, model.Provider)
	fmt.Printf("base_url:  %s\n", model.BaseURL)
	fmt.Printf("prompt:    %s\n", *prompt)
	fmt.Printf("latency:   %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("tokens:    prompt=%d completion=%d total=%d\n",
		result.Usage.Input, result.Usage.Output, result.Usage.TotalTokens)
	fmt.Println("────────────────────────────────────────")
	fmt.Println(result.Content)
	fmt.Println("────────────────────────────────────────")
}
