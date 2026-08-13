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
const defaultSystemPrompt = `You are a helpful coding assistant. You have access to the following tools:

- read_file: Read a file with line numbers. Supports offset/limit.
- write_file: Create or overwrite a file. Creates parent directories.
- edit_file: Search-and-replace edit (old_text must be unique in the file).
- list_files: List directory contents (supports recursive, show_hidden).
- bash: Execute a shell command.
- glob: List files matching a glob pattern (e.g. "**/*.go").
- grep: Search file contents for a pattern (substring or regex).
- web_fetch: Fetch a URL and return its content as plain text.

Rules:
- Use tools to inspect and modify files. Do not guess file contents.
- Chain multiple tool calls when a task needs multiple steps (e.g. glob → read → edit → verify).
- After using tools, briefly summarize what you did and the results.
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

	// Detect provider and API key. An explicit AI_PROVIDER wins, otherwise
	// infer from which provider API key is present on the environment.
	cfg.ProviderName, cfg.APIKey = detectProvider()
	if cfg.ProviderName == "" {
		return nil, fmt.Errorf("no AI API key found. Set one of: OPENAI_API_KEY, ANTHROPIC_API_KEY, DEEPSEEK_API_KEY, GOOGLE_API_KEY, XAI_API_KEY, GROQ_API_KEY, MISTRAL_API_KEY — or use AI_PROVIDER=openai-compatible with OPENAI_API_KEY + AI_BASE_URL")
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
	// Explicit AI_PROVIDER override (mirrors demo-agent).
	if p := os.Getenv("AI_PROVIDER"); p != "" {
		switch p {
		case "openai-compatible":
			return "openai-compatible", os.Getenv("OPENAI_API_KEY")
		case "openai", "anthropic", "google", "deepseek", "xai", "groq", "mistral":
			return p, apiKeyFor(p)
		default:
			return p, os.Getenv(strings.ToUpper(p) + "_API_KEY")
		}
	}
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
	// pre-check: if AI_BASE_URL is set, default to openai-compatible
	if os.Getenv("AI_BASE_URL") != "" {
		if key := os.Getenv("OPENAI_API_KEY"); key != "" {
			return "openai-compatible", key
		}
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

func apiKeyFor(p string) string {
	switch p {
	case "openai":
		return os.Getenv("OPENAI_API_KEY")
	case "anthropic":
		return os.Getenv("ANTHROPIC_API_KEY")
	case "google":
		if k := os.Getenv("GOOGLE_API_KEY"); k != "" {
			return k
		}
		return os.Getenv("GEMINI_API_KEY")
	case "deepseek":
		return os.Getenv("DEEPSEEK_API_KEY")
	case "xai":
		return os.Getenv("XAI_API_KEY")
	case "groq":
		return os.Getenv("GROQ_API_KEY")
	case "mistral":
		return os.Getenv("MISTRAL_API_KEY")
	default:
		return os.Getenv(strings.ToUpper(p) + "_API_KEY")
	}
}

func setDefaults(cfg *tuiConfig) {
	if cfg.ModelID != "" {
		return
	}
	// Default model per provider (metadata is resolved via ai.GetModel at
	// agent construction time, so we only need the model id here).
	switch cfg.ProviderName {
	case "anthropic":
		cfg.ModelID = "claude-sonnet-4-20250514"
	case "openai":
		cfg.ModelID = "gpt-4o"
	case "deepseek":
		cfg.ModelID = "deepseek-chat"
	case "google":
		cfg.ModelID = "gemini-2.5-flash-preview-05-20"
	case "xai":
		cfg.ModelID = "grok-3-mini"
	case "groq":
		cfg.ModelID = "llama-3.3-70b-versatile"
	case "mistral":
		cfg.ModelID = "mistral-large-latest"
	case "openai-compatible":
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.openai.com/v1"
		}
	default:
		cfg.ModelID = "gpt-4o"
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
