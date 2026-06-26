package openai

import (
	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/providers/compat"
)

// CompletionsOptions holds OpenAI Completions-specific options.
type CompletionsOptions struct {
	ToolChoice      any    `json:"toolChoice,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

// NewCompat returns an OpenAI Completions APIProvider registration helper
// that callers can chain into a compat.Router.
//
// All OpenAI-compatible providers (openai, xiaomi, glm, deepseek, kimi,
// moonshot, ...) share the same compat engine and dispatch on model.Provider.
func NewCompat() compat.Config {
	return compat.Config{
		Provider:       core.ProviderOpenAI,
		DefaultBaseURL: "https://api.openai.com/v1",
	}
}
