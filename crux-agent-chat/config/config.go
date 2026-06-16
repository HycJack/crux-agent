// Package config loads AI configuration from environment variables and .env files.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hycjack/crux-ai/core"

	"github.com/joho/godotenv"
)

// Config holds the AI provider configuration.
type Config struct {
	Provider     core.KnownProvider
	ModelID      string
	APIKey       string
	BaseURL      string
	SystemPrompt string
	MaxTokens    int
	Temperature  float64
	QueryTimeout time.Duration
}

// Load loads configuration from .env file and environment variables.
func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("loading .env: %w", err)
	}
	if err := godotenv.Load("../.env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("loading ../.env: %w", err)
	}

	cfg := &Config{
		MaxTokens:   4096,
		Temperature: 0.7,
	}

	cfg.Provider, cfg.APIKey = detectProvider()
	if cfg.Provider == "" {
		return nil, fmt.Errorf("no AI API key found. Set one of: ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_API_KEY, DEEPSEEK_API_KEY")
	}

	cfg.ModelID = os.Getenv("AI_MODEL")
	cfg.BaseURL = os.Getenv("AI_BASE_URL")
	cfg.SystemPrompt = os.Getenv("AI_SYSTEM_PROMPT")
	if v := os.Getenv("AI_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid AI_MAX_TOKENS=%q: %w", v, err)
		}
		if n <= 0 {
			return nil, fmt.Errorf("AI_MAX_TOKENS must be > 0, got %d", n)
		}
		cfg.MaxTokens = n
	}
	if v := os.Getenv("AI_TEMPERATURE"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid AI_TEMPERATURE=%q: %w", v, err)
		}
		cfg.Temperature = f
	}
	if v := os.Getenv("AI_QUERY_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid AI_QUERY_TIMEOUT=%q: %w", v, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("AI_QUERY_TIMEOUT must be > 0, got %s", d)
		}
		cfg.QueryTimeout = d
	} else {
		cfg.QueryTimeout = 10 * time.Minute
	}

	setDefaults(cfg)

	return cfg, nil
}

func detectProvider() (core.KnownProvider, string) {
	providers := []struct {
		provider core.KnownProvider
		envVars  []string
	}{
		{core.ProviderAnthropic, []string{"ANTHROPIC_API_KEY"}},
		{core.ProviderOpenAI, []string{"OPENAI_API_KEY"}},
		{core.ProviderDeepSeek, []string{"DEEPSEEK_API_KEY"}},
		{core.ProviderGoogle, []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"}},
		{core.ProviderXAI, []string{"XAI_API_KEY"}},
		{core.ProviderGroq, []string{"GROQ_API_KEY"}},
		{core.ProviderMistral, []string{"MISTRAL_API_KEY"}},
	}
	for _, p := range providers {
		for _, envVar := range p.envVars {
			if key := os.Getenv(envVar); key != "" {
				return p.provider, key
			}
		}
	}
	return "", ""
}

func setDefaults(cfg *Config) {
	if cfg.ModelID != "" {
		return
	}
	switch cfg.Provider {
	case core.ProviderAnthropic:
		cfg.ModelID = "claude-sonnet-4-20250514"
	case core.ProviderOpenAI:
		cfg.ModelID = "gpt-4o"
	case core.ProviderDeepSeek:
		cfg.ModelID = "deepseek-chat"
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.deepseek.com/v1"
		}
	case core.ProviderGoogle:
		cfg.ModelID = "gemini-2.5-flash-preview-05-20"
	case core.ProviderXAI:
		cfg.ModelID = "grok-3-mini"
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.x.ai/v1"
		}
	case core.ProviderGroq:
		cfg.ModelID = "llama-3.3-70b-versatile"
	case core.ProviderMistral:
		cfg.ModelID = "mistral-large-latest"
	}
}

// DefaultSystemPrompt is the default system prompt for the coding agent.
const DefaultSystemPrompt = `You are a helpful coding assistant. You can:
- Read and write files on the user's machine
- Execute shell commands
- List directory contents
- Edit files with precise search-and-replace operations

Rules:
- After using tools, always summarize what you did and the results.
- Chain multiple tool calls together when a task requires multiple steps (e.g. read → edit → verify).
- Explain what you plan to do before using tools.
- Be careful with destructive operations.
- If a task is complete, say so clearly.`

// GetModel returns the core.Model for this config.
// When a custom BaseURL is set, always use OpenAI Completions API (most providers are compatible).
func (c *Config) GetModel() core.Model {
	api := c.detectAPI()
	return core.Model{
		ID:            c.ModelID,
		Name:          c.ModelID,
		API:           api,
		Provider:      c.Provider,
		BaseURL:       c.BaseURL,
		ContextWindow: c.contextWindow(),
		MaxTokens:     c.MaxTokens,
	}
}

func (c *Config) contextWindow() int {
	switch c.Provider {
	case core.ProviderAnthropic:
		return 200000
	case core.ProviderGoogle:
		return 1000000
	case core.ProviderDeepSeek:
		return 1000000
	case core.ProviderXAI:
		return 128000
	case core.ProviderGroq:
		return 128000
	case core.ProviderMistral:
		return 128000
	default:
		return 128000
	}
}

func (c *Config) detectAPI() core.KnownAPI {
	// If user set a custom base URL, it's almost certainly OpenAI-compatible
	if c.BaseURL != "" {
		return core.APIOpenAICompletions
	}
	switch c.Provider {
	case core.ProviderAnthropic:
		return core.APIAnthropicMessages
	case core.ProviderGoogle:
		return core.APIGoogleGenerative
	case core.ProviderGoogleVertex:
		return core.APIGoogleVertex
	case core.ProviderMistral:
		return core.APIMistralConversations
	case core.ProviderAmazonBedrock:
		return core.APIBedrockConverse
	default:
		return core.APIOpenAICompletions
	}
}

// APIKey returns the API key.
func (c *Config) GetAPIKey() string {
	return c.APIKey
}

// ProviderName returns the provider name as a string.
func (c *Config) GetProvider() core.KnownProvider {
	return c.Provider
}

// GetModelID returns the model ID.
func (c *Config) GetModelID() string {
	return c.ModelID
}

// String returns a human-readable summary of the config.
func (c *Config) String() string {
	masked := c.APIKey
	if len(masked) > 8 {
		masked = masked[:4] + "..." + masked[len(masked)-4:]
	}
	return strings.Join([]string{
		fmt.Sprintf("Provider:   %s", c.Provider),
		fmt.Sprintf("Model:      %s", c.ModelID),
		fmt.Sprintf("API:        %s", c.detectAPI()),
		fmt.Sprintf("API Key:    %s", masked),
		fmt.Sprintf("Base URL:   %s", orDefault(c.BaseURL, "(default)")),
	}, "\n")
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
