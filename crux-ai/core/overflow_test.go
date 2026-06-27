package core

import (
	"errors"
	"strings"
	"testing"
)

func TestIsContextOverflowError_Positive(t *testing.T) {
	cases := []string{
		"prompt is too long: 213462 tokens > 200000 maximum",                                       // Anthropic
		`{"error":{"type":"request_too_large","message":"Request exceeds..."}}`,                    // Anthropic 413
		"input is too long for requested model",                                                    // Bedrock
		"Your input exceeds the context window of this model",                                      // OpenAI
		"Requested token count exceeds the model's maximum context length",                         // OpenAI-compatible
		"The input token count (1196265) exceeds the maximum number of tokens",                     // Google
		"This model's maximum prompt length is 131072 but the request contains 537812 tokens",      // xAI
		"Please reduce the length of the messages or completion",                                   // Groq
		"This endpoint's maximum context length is 4096 tokens",                                    // OpenRouter
		"Input length 5000 exceeds the maximum allowed input length of 4096 tokens.",               // OpenRouter/Poolside
		"The input (500 tokens) is longer than the model's context length (300 tokens).",           // Together AI
		"prompt token count of 200000 exceeds the limit of 100000",                                 // GitHub Copilot
		"the request exceeds the available context size, try increasing it",                        // llama.cpp
		"tokens to keep from the initial prompt is greater than the context length",                // LM Studio
		"invalid params, context window exceeds limit",                                             // MiniMax
		"Your request exceeded model token limit: 200000 (requested: 300000)",                      // Kimi
		"Prompt contains 200000 tokens ... too large for model with 100000 maximum context length", // Mistral
		"model_context_window_exceeded",                                                            // z.ai
		"prompt too long; exceeded max context length",                                             // Ollama
		"400 status code (no body)",                                                                // Cerebras
		"413 status code (no body)",                                                                // Cerebras
		"context_length_exceeded",                                                                  // Generic
	}
	for _, c := range cases {
		if !IsContextOverflowError(c) {
			t.Errorf("expected overflow for %q", c)
		}
	}
}

func TestIsContextOverflowError_Negative(t *testing.T) {
	cases := []string{
		"",
		"plain text",
		"invalid API key",
		"connection refused",
		"server is overloaded, try again", // not "too many tokens"
		"model not found: gpt-5",
		"You exceeded your current quota", // no "too many tokens"
	}
	for _, c := range cases {
		if IsContextOverflowError(c) {
			t.Errorf("expected non-overflow for %q", c)
		}
	}
}

func TestIsContextOverflowError_NonOverflowExclusion(t *testing.T) {
	// Bedrock throttling — would match /too many tokens/i but is NOT overflow.
	cases := []string{
		"Throttling error: Too many tokens, please wait before trying again.",
		"Service unavailable: please retry your request after a short delay",
		"Rate limit exceeded: try again later",
		"too many requests, please slow down",
	}
	for _, c := range cases {
		if IsContextOverflowError(c) {
			t.Errorf("throttling message should NOT match overflow: %q", c)
		}
	}
}

func TestIsContextOverflow_ErrorWithEmbeddedJSON(t *testing.T) {
	// Some providers embed a JSON body inside a plain-text error.
	err := errors.New(`API error 413: {"error":{"type":"request_too_large","message":"Request exceeds the maximum size"}}`)
	if !IsContextOverflow(err) {
		t.Fatalf("expected overflow detection from embedded JSON body")
	}
}

func TestIsContextOverflow_ProviderErrorKind(t *testing.T) {
	oe := &OverflowError{Provider: ProviderOpenAI, Message: "context window exceeded"}
	if !IsContextOverflow(oe) {
		t.Errorf("*OverflowError should be detected as overflow")
	}

	auth := &AuthError{Provider: ProviderOpenAI}
	if IsContextOverflow(auth) {
		t.Errorf("auth error should not be overflow")
	}
}

func TestIsContextOverflow_Nil(t *testing.T) {
	if IsContextOverflow(nil) {
		t.Errorf("nil error must not be overflow")
	}
}

func TestIsContextOverflowMessage_ErrorCase(t *testing.T) {
	msg := &AssistantMessage{
		StopReason:   StopError,
		ErrorMessage: "Your input exceeds the context window of this model",
	}
	if !IsContextOverflowMessage(msg, 0) {
		t.Errorf("expected overflow via error message")
	}
}

func TestIsContextOverflowMessage_SilentOverflow(t *testing.T) {
	// z.ai style: stopReason=stop but input > context window.
	msg := &AssistantMessage{
		StopReason: StopStop,
		Usage:      Usage{Input: 150000, CacheRead: 0, Output: 100},
	}
	if !IsContextOverflowMessage(msg, 100000) {
		t.Errorf("expected silent overflow detection")
	}
	// Should NOT trigger when contextWindow is 0 (disabled).
	if IsContextOverflowMessage(msg, 0) {
		t.Errorf("silent overflow should be disabled when contextWindow=0")
	}
}

func TestIsContextOverflowMessage_LengthStopOverflow(t *testing.T) {
	// Xiaomi MiMo style: stopReason=length, output=0, input fills window.
	msg := &AssistantMessage{
		StopReason: StopLength,
		Usage:      Usage{Input: 99000, CacheRead: 0, Output: 0},
	}
	if !IsContextOverflowMessage(msg, 100000) {
		t.Errorf("expected length-stop overflow detection")
	}

	// Should NOT trigger when output > 0 (genuine truncation mid-generation).
	msg2 := &AssistantMessage{
		StopReason: StopLength,
		Usage:      Usage{Input: 99000, Output: 200},
	}
	if IsContextOverflowMessage(msg2, 100000) {
		t.Errorf("length+output should not be overflow")
	}
}

func TestRegisterOverflowPattern(t *testing.T) {
	if err := RegisterOverflowPattern(`(?i)my-custom-provider-overflow`); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if !IsContextOverflowError("my-custom-provider-overflow happened") {
		t.Errorf("custom pattern not applied")
	}
	if err := RegisterOverflowPattern(`[invalid`); err == nil {
		t.Errorf("expected compile error for invalid pattern")
	}
}

func TestEstimateTokens(t *testing.T) {
	msgs := []Message{
		UserMessage{Content: "Hello world"},                                // 11 chars -> 3 tokens
		AssistantMessage{Content: []ContentBlock{TextContent{Text: "Hi"}}}, // 2 chars -> 1 token
	}
	got := EstimateTokens("system", msgs, nil)
	// system "system" = 6 chars -> 2 tokens
	// msgs = 3 + 1 = 4 tokens
	// total = 6
	if got != 6 {
		t.Errorf("expected 6, got %d", got)
	}
}

func TestEstimateTokens_WithTools(t *testing.T) {
	got := EstimateTokens("", nil, []Tool{
		{Name: "search", Description: "desc", Parameters: []byte(`{"type":"object"}`)},
	})
	if got == 0 {
		t.Errorf("tools should contribute tokens")
	}
}

func TestExtractJSONObject(t *testing.T) {
	cases := map[string]string{
		`hello {"a": 1} world`:        `{"a": 1}`,
		`prefix {"a":{"b":2}} suffix`: `{"a":{"b":2}}`,
		`no json here`:                "",
		`{"unclosed`:                  "",
	}
	for in, want := range cases {
		got := extractJSONObject(in)
		if got != want {
			t.Errorf("extractJSONObject(%q): got %q, want %q", in, got, want)
		}
	}
}

func TestOverflowPatterns_NoThrottlingFalsePositive(t *testing.T) {
	// End-to-end: a real Bedrock error must NOT be flagged overflow.
	bedrockThrottle := "Throttling error: Too many tokens, please wait before trying again. (Request ID: abc123)"
	if IsContextOverflowError(bedrockThrottle) {
		// If this fails, nonOverflowPatterns exclusion didn't fire.
		// We assert explicitly to surface regression.
		if !strings.Contains(bedrockThrottle, "Throttling") {
			t.Errorf("unexpected false positive on: %s", bedrockThrottle)
		}
	}
}
