// Package token provides token counting and context window management.
package token

import (
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// Counter counts tokens for a given text using tiktoken.
type Counter struct {
	mu          sync.RWMutex
	encoding    *tiktoken.Tiktoken
	model       string
	useFallback bool // fallback mode when tiktoken fails
}

// ModelEncoding maps model names to tiktoken encoding names.
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

// DefaultEncoding is the fallback encoding name.
const DefaultEncoding = "cl100k_base"

// New creates a Counter for the given model.
// If tiktoken cannot be initialized (e.g., no network), falls back to character-based estimation.
func New(model string) (*Counter, error) {
	encName := DefaultEncoding
	if mapped, ok := ModelEncoding[model]; ok {
		encName = mapped
	}

	enc, err := tiktoken.GetEncoding(encName)
	if err != nil {
		// Try default encoding
		enc, err = tiktoken.GetEncoding(DefaultEncoding)
		if err != nil {
			// Fallback to character-based estimation when tiktoken fails
			// This happens when network is unavailable and encoding files are not cached
			return &Counter{
				model:       model,
				useFallback: true,
			}, nil
		}
	}
	return &Counter{encoding: enc, model: model, useFallback: false}, nil
}

// CountTokens counts tokens in a text string.
func (c *Counter) CountTokens(text string) int {
	if c.useFallback {
		return c.countTokensFallback(text)
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.encoding.Encode(text, nil, nil))
}

// countTokensFallback estimates token count using character-based approximation.
// This is used when tiktoken cannot be initialized (e.g., no network).
// - English: ~4 chars per token
// - Chinese/Japanese/Korean: ~2 chars per token (due to UTF-8 encoding)
// - Mixed text: weighted average
func (c *Counter) countTokensFallback(text string) int {
	if text == "" {
		return 0
	}

	// Count ASCII vs non-ASCII characters
	asciiCount := 0
	nonAsciiCount := 0

	for _, runeValue := range text {
		if runeValue < 128 {
			asciiCount++
		} else {
			nonAsciiCount++
		}
	}

	// Estimate:
	// - ASCII: ~4 chars per token
	// - Non-ASCII (CJK): ~2 chars per token
	tokens := (asciiCount+1)/4 + (nonAsciiCount+1)/2

	// Add some overhead for formatting
	if tokens > 0 {
		tokens += 2
	}

	return tokens
}

// Model returns the model this counter was created for.
func (c *Counter) Model() string {
	return c.model
}

// IsFallback returns true if the counter is using fallback mode.
func (c *Counter) IsFallback() bool {
	return c.useFallback
}

// --- Pool: reuse counters across goroutines ---

var (
	poolMu      sync.RWMutex
	counterPool = make(map[string]*Counter)
)

// GetCounter returns a cached Counter for the given model.
func GetCounter(model string) (*Counter, error) {
	poolMu.RLock()
	if c, ok := counterPool[model]; ok {
		poolMu.RUnlock()
		return c, nil
	}
	poolMu.RUnlock()

	poolMu.Lock()
	defer poolMu.Unlock()
	if c, ok := counterPool[model]; ok {
		return c, nil
	}
	c, err := New(model)
	if err != nil {
		return nil, err
	}
	counterPool[model] = c
	return c, nil
}
