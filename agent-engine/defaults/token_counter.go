package defaults

import (
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// Counter counts tokens for a given text using tiktoken.
//
// If tiktoken fails to initialize (e.g., no network), it transparently
// falls back to character-based estimation.
type Counter struct {
	mu          sync.RWMutex
	encoding    *tiktoken.Tiktoken
	model       string
	useFallback bool
}

// ModelEncoding maps model names to tiktoken encoding names.
// Add new models here as they become supported.
var ModelEncoding = map[string]string{
	"gpt-4o":                         "o200k_base",
	"gpt-4o-mini":                    "o200k_base",
	"o3":                             "o200k_base",
	"o3-mini":                        "o200k_base",
	"o4-mini":                        "o200k_base",
	"gpt-4":                          "cl100k_base",
	"gpt-4-turbo":                    "cl100k_base",
	"gpt-3.5-turbo":                  "cl100k_base",
	"claude-sonnet-4-20250514":       "cl100k_base",
	"claude-opus-4-20250514":         "cl100k_base",
	"claude-3-5-haiku-20241022":      "cl100k_base",
	"gemini-2.5-pro-preview-05-06":   "cl100k_base",
	"gemini-2.5-flash-preview-05-20": "cl100k_base",
	"gemini-2.0-flash":               "cl100k_base",
	"deepseek-chat":                  "cl100k_base",
	"deepseek-reasoner":              "cl100k_base",
}

// DefaultEncoding is the fallback encoding name when the model is unknown.
const DefaultEncoding = "cl100k_base"

// NewCounter creates a Counter for the given model.
// Falls back to character-based estimation if tiktoken cannot initialize.
func NewCounter(model string) (*Counter, error) {
	encName := DefaultEncoding
	if mapped, ok := ModelEncoding[model]; ok {
		encName = mapped
	}

	enc, err := tiktoken.GetEncoding(encName)
	if err != nil {
		enc, err = tiktoken.GetEncoding(DefaultEncoding)
		if err != nil {
			return &Counter{model: model, useFallback: true}, nil
		}
	}
	return &Counter{encoding: enc, model: model}, nil
}

// CountTokens counts tokens in a text string.
func (c *Counter) CountTokens(text string) int {
	if c.useFallback {
		return countTokensFallback(text)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.encoding.Encode(text, nil, nil))
}

// Model returns the model this counter was created for.
func (c *Counter) Model() string { return c.model }

// IsFallback returns true if the counter is using fallback mode.
func (c *Counter) IsFallback() bool { return c.useFallback }

// countTokensFallback estimates token count using character-based approximation.
// - ASCII: ~4 chars per token
// - Non-ASCII (CJK): ~2 chars per token
func countTokensFallback(text string) int {
	if text == "" {
		return 0
	}
	asciiCount := 0
	nonAsciiCount := 0
	for _, r := range text {
		if r < 128 {
			asciiCount++
		} else {
			nonAsciiCount++
		}
	}
	tokens := (asciiCount+1)/4 + (nonAsciiCount+1)/2
	if tokens > 0 {
		tokens += 2
	}
	return tokens
}

// ─── Counter pool: reuse across goroutines ──────────────────────────────────

var (
	counterPoolMu sync.RWMutex
	counterPool   = make(map[string]*Counter)
)

// GetCounter returns a cached Counter for the given model.
func GetCounter(model string) (*Counter, error) {
	counterPoolMu.RLock()
	if c, ok := counterPool[model]; ok {
		counterPoolMu.RUnlock()
		return c, nil
	}
	counterPoolMu.RUnlock()

	counterPoolMu.Lock()
	defer counterPoolMu.Unlock()
	if c, ok := counterPool[model]; ok {
		return c, nil
	}
	c, err := NewCounter(model)
	if err != nil {
		return nil, err
	}
	counterPool[model] = c
	return c, nil
}
