package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/hycjack/crux-ai/core"

	"crux-agent-runtime/memory"
)

// loadEnv loads key=value pairs from a .env-style file into the
// environment. It does not override existing environment variables.
func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		os.Setenv(key, val)
	}
}

// getTestModel returns the first model whose provider has an API key
// configured in the environment. Order matters: the first match wins.
func getTestModel() (core.Model, error) {
	// Each entry is (provider, modelID, API style). Add new providers here.
	candidates := []struct {
		provider core.KnownProvider
		modelID  string
		api      core.KnownAPI
	}{
		{core.ProviderAnthropic, "claude-sonnet-4-20250514", core.APIAnthropicMessages},
		{core.ProviderOpenAI, "gpt-4o-mini", core.APIOpenAICompletions},
		{core.ProviderGoogle, "gemini-2.0-flash", core.APIGoogleGenerative},
		{core.ProviderDeepSeek, "deepseek-chat", core.APIOpenAICompletions},
		{core.ProviderKimi, "kimi-k2.5", core.APIOpenAICompletions},
		{core.ProviderXiaomi, "mimo-v2.5-pro", core.APIOpenAICompletions},
		{core.ProviderGLM, "glm-4-plus", core.APIOpenAICompletions},
	}
	for _, p := range candidates {
		if key := core.GetEnvAPIKey(p.provider); key != "" {
			return core.Model{ID: p.modelID, Provider: p.provider, API: p.api}, nil
		}
	}
	return core.Model{}, fmt.Errorf("no API key found for any provider")
}

// buildSystemPrompt renders the system prompt: a stable intro, the
// current long-term memory (if any), and a usage hint for tools.
func buildSystemPrompt(mem *memory.Memory) string {
	var sb strings.Builder
	sb.WriteString("You are a helpful assistant with access to tools and long-term memory.\n\n")

	if memContent := mem.FormatForPrompt(); memContent != "" {
		sb.WriteString(memContent)
		sb.WriteString("\n")
	}

	sb.WriteString("Use the available tools to help the user. Be concise and friendly.")
	return sb.String()
}