package ui

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// ProviderConfig holds the AI provider configuration.
type ProviderConfig struct {
	Name      string // provider name (e.g., "openai", "deepseek", "custom")
	Model     string
	APIKey    string
	BaseURL   string
	MaxTokens int
}

// tuiConfig holds the configuration for the TUI app.
type tuiConfig struct {
	ProviderName string // provider name for display
	ModelID      string
	APIKey       string
	BaseURL      string
	SystemPrompt string
	MaxTokens    int
	Temperature  float64
	QueryTimeout time.Duration
}

// defaultSystemPrompt is the default system prompt.
const defaultSystemPrompt = `You are a helpful coding assistant. You can:
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

// loadConfig loads configuration from .env file and environment variables.
func loadConfig() (*tuiConfig, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("loading .env: %w", err)
	}
	if err := godotenv.Load("../.env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("loading ../.env: %w", err)
	}

	cfg := &tuiConfig{
		MaxTokens:   4096,
		Temperature: 0.7,
	}

	// Detect provider and API key
	cfg.ProviderName, cfg.APIKey = detectProvider()
	if cfg.ProviderName == "" {
		return nil, fmt.Errorf("no AI API key found. Set one of: OPENAI_API_KEY, ANTHROPIC_API_KEY, DEEPSEEK_API_KEY, GOOGLE_API_KEY, XAI_API_KEY, GROQ_API_KEY, MISTRAL_API_KEY")
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

func detectProvider() (string, string) {
	type providerEntry struct {
		name    string
		envVars []string
	}
	providers := []providerEntry{
		{"openai", []string{"OPENAI_API_KEY"}},
		{"anthropic", []string{"ANTHROPIC_API_KEY"}},
		{"deepseek", []string{"DEEPSEEK_API_KEY"}},
		{"google", []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"}},
		{"xai", []string{"XAI_API_KEY"}},
		{"groq", []string{"GROQ_API_KEY"}},
		{"mistral", []string{"MISTRAL_API_KEY"}},
	}
	for _, p := range providers {
		for _, envVar := range p.envVars {
			if key := os.Getenv(envVar); key != "" {
				return p.name, key
			}
		}
	}
	return "", ""
}

func setDefaults(cfg *tuiConfig) {
	if cfg.ModelID != "" {
		return
	}
	switch cfg.ProviderName {
	case "anthropic":
		cfg.ModelID = "claude-sonnet-4-20250514"
	case "openai":
		cfg.ModelID = "gpt-4o"
	case "deepseek":
		cfg.ModelID = "deepseek-chat"
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.deepseek.com/v1"
		}
	case "google":
		cfg.ModelID = "gemini-2.5-flash-preview-05-20"
	case "xai":
		cfg.ModelID = "grok-3-mini"
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.x.ai/v1"
		}
	case "groq":
		cfg.ModelID = "llama-3.3-70b-versatile"
	case "mistral":
		cfg.ModelID = "mistral-large-latest"
	default:
		cfg.ModelID = "gpt-4o"
	}
}

// mustUseOpenAIProtocol returns true if the provider uses the OpenAI-compatible protocol.
func (c *tuiConfig) mustUseOpenAIProtocol() bool {
	// Most providers use OpenAI protocol. Anthropic and Google are the exceptions.
	switch c.ProviderName {
	case "anthropic", "google":
		return false
	default:
		return true
	}
}

// String returns a human-readable summary.
func (c *tuiConfig) String() string {
	masked := c.APIKey
	if len(masked) > 8 {
		masked = masked[:4] + "..." + masked[len(masked)-4:]
	}
	return strings.Join([]string{
		fmt.Sprintf("Provider:   %s", c.ProviderName),
		fmt.Sprintf("Model:      %s", c.ModelID),
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
