package core

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// overflowPatterns is the curated library of regex patterns that match
// "context overflow" error messages from major LLM providers.
//
// Curated verbatim from pi-mono packages/ai/src/utils/overflow.ts.
// Order is not significant — matching is set semantics.
//
// Reliable (returns explicit error):
//   - Anthropic:        "prompt is too long", "request_too_large"
//   - OpenAI (Completions / Responses):
//     "exceeds the context window", "maximum context length"
//   - Google Gemini:    "input token count.*exceeds the maximum"
//   - xAI (Grok):       "maximum prompt length is N"
//   - Groq:             "reduce the length of the messages"
//   - Mistral:          "too large for model with N maximum context length"
//   - Bedrock:          "input is too long for requested model"
//   - OpenRouter:       "maximum context length is N tokens"
//   - OpenRouter/Poolside:
//     "exceeds the maximum allowed input length of N tokens"
//   - Together AI:      "input (N tokens) is longer than the model's context length"
//   - GitHub Copilot:   "exceeds the limit of N"
//   - llama.cpp:        "exceeds the available context size"
//   - LM Studio:        "greater than the context length"
//   - Cerebras:         "^(400|413) status code (no body)"
//   - Kimi For Coding:  "exceeded model token limit"
//   - z.ai:             "model_context_window_exceeded"
//   - Ollama:           "prompt too long; exceeded context length"
var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)prompt is too long`),
	regexp.MustCompile(`(?i)request_too_large`),
	regexp.MustCompile(`(?i)input is too long for requested model`),
	regexp.MustCompile(`(?i)exceeds the context window`),
	regexp.MustCompile(`(?i)exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))?`),
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),
	regexp.MustCompile(`(?i)reduce the length of the messages`),
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),
	regexp.MustCompile(`(?i)exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`),
	regexp.MustCompile(`(?i)input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)`),
	regexp.MustCompile(`(?i)exceeds the limit of \d+`),
	regexp.MustCompile(`(?i)exceeds the available context size`),
	regexp.MustCompile(`(?i)greater than the context length`),
	regexp.MustCompile(`(?i)context window exceeds limit`),
	regexp.MustCompile(`(?i)exceeded model token limit`),
	regexp.MustCompile(`(?i)too large for model with \d+ maximum context length`),
	regexp.MustCompile(`(?i)model_context_window_exceeded`),
	regexp.MustCompile(`(?i)prompt too long; exceeded (?:max )?context length`),
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),
	regexp.MustCompile(`(?i)too many tokens`),
	regexp.MustCompile(`(?i)token limit exceeded`),
	regexp.MustCompile(`(?i)^4(?:00|13)\s*(?:status code)?\s*\(no body\)`),
}

// nonOverflowPatterns explicitly excludes matches that overlap with
// overflowPatterns but are not context-overflow errors.
//
// The classic example: AWS Bedrock formats "ThrottlingException: Too many
// tokens, please wait before trying again." which would match
// /too many tokens/i without this exclusion.
var nonOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(Throttling error|Service unavailable):`),
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)too many requests`),
}

var extraOverflowPatterns []*regexp.Regexp

// RegisterOverflowPattern adds a custom overflow-detection regex.
//
// Useful for self-hosted OpenAI-compatible providers whose error text
// does not match any built-in pattern. Compiled at registration time;
// returns an error if the pattern is invalid.
func RegisterOverflowPattern(pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	extraOverflowPatterns = append(extraOverflowPatterns, re)
	return nil
}

// IsContextOverflowError returns true if the provided error string
// matches any overflow pattern AND does not match a non-overflow
// exclusion pattern.
//
// Use this when you have an error string from an HTTP API and want to
// know whether the cause is "input too large". Callers should prefer
// IsContextOverflow when the error carries typed metadata.
func IsContextOverflowError(errStr string) bool {
	if errStr == "" {
		return false
	}
	// Exclusions first (throttling, rate-limit) so we don't false-positive
	// on messages like "Throttling: Too many tokens, please wait".
	for _, p := range nonOverflowPatterns {
		if p.MatchString(errStr) {
			return false
		}
	}
	for _, p := range overflowPatterns {
		if p.MatchString(errStr) {
			return true
		}
	}
	for _, p := range extraOverflowPatterns {
		if p.MatchString(errStr) {
			return true
		}
	}
	return false
}

// IsContextOverflow returns true if the error indicates context overflow.
// It inspects *OverflowError, plain error messages, and embedded
// HTTP response bodies.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}

	// Typed *OverflowError match — the new active error path.
	var oe *OverflowError
	if errors.As(err, &oe) {
		return true
	}

	// Inspect embedded JSON body if present (some providers embed JSON
	// in plain-text error messages like "API error 413: {\"error\":...}").
	msg := err.Error()
	if IsContextOverflowError(msg) {
		return true
	}
	if obj := extractJSONObject(msg); obj != "" {
		if IsContextOverflowError(obj) {
			return true
		}
	}
	return false
}

// IsContextOverflowMessage implements pi-mono's three-case overflow check:
//
//  1. Error-based overflow: stopReason == "error" + errorMessage matches
//     any overflow pattern (excluding non-overflow throttling matches).
//  2. Silent overflow (z.ai style): a successful response whose
//     usage.input + usage.cacheRead exceeds the context window.
//  3. Length-stop overflow (Xiaomi MiMo style): server truncates oversized
//     input to fit context window, then returns stopReason "length"
//     with output == 0 and input + cacheRead filling the window.
//
// Pass contextWindow > 0 to enable cases 2 and 3.
func IsContextOverflowMessage(msg *AssistantMessage, contextWindow int) bool {
	if msg == nil {
		return false
	}

	// Case 1: Error-based overflow.
	if msg.StopReason == StopError && msg.ErrorMessage != "" {
		if IsContextOverflowError(msg.ErrorMessage) {
			return true
		}
	}

	// Case 2: Silent overflow (z.ai style) — successful but exceeds window.
	if contextWindow > 0 && msg.StopReason == StopStop {
		inputTokens := msg.Usage.Input + msg.Usage.CacheRead
		if inputTokens > contextWindow {
			return true
		}
	}

	// Case 3: Length-stop overflow (Xiaomi MiMo style) — server truncated
	// to fit window, no room left for output.
	if contextWindow > 0 && msg.StopReason == StopLength && msg.Usage.Output == 0 {
		inputTokens := msg.Usage.Input + msg.Usage.CacheRead
		if inputTokens >= contextWindow*99/100 {
			return true
		}
	}

	return false
}

// extractJSONObject finds the first balanced {...} substring in s.
// Returns "" if none found. Lightweight; not a full JSON parser.
func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	for start >= 0 {
		depth := 0
		for i := start; i < len(s); i++ {
			switch s[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					candidate := s[start : i+1]
					if json.Valid([]byte(candidate)) {
						return candidate
					}
					return ""
				}
			}
		}
		next := strings.IndexByte(s[start+1:], '{')
		if next < 0 {
			return ""
		}
		start = start + 1 + next
	}
	return ""
}

// --- Token estimation (chars/4 heuristic) ---

const (
	charsPerToken       = 4
	estimatedImageChars = 4800 // roughly 1200 tokens at chars/4
)

// EstimateTokens provides a fast, dependency-free token estimate for a
// context. For exact counts, callers should plug in a model-specific
// tokenizer.
func EstimateTokens(systemPrompt string, messages []Message, tools []Tool) int {
	total := 0
	if systemPrompt != "" {
		total += (len(systemPrompt) + charsPerToken - 1) / charsPerToken
	}
	for _, m := range messages {
		total += estimateMessage(m)
	}
	for _, t := range tools {
		total += (len(t.Name) + charsPerToken - 1) / charsPerToken
		total += (len(t.Description) + charsPerToken - 1) / charsPerToken
		if len(t.Parameters) > 0 {
			total += (len(t.Parameters) + charsPerToken - 1) / charsPerToken
		}
	}
	return total
}

func estimateMessage(m Message) int {
	switch mm := m.(type) {
	case UserMessage:
		switch c := mm.Content.(type) {
		case string:
			return (len(c) + charsPerToken - 1) / charsPerToken
		case []ContentBlock:
			total := 0
			for _, b := range c {
				total += estimateBlock(b)
			}
			return total
		}
	case AssistantMessage:
		total := 0
		for _, b := range mm.Content {
			total += estimateBlock(b)
		}
		return total
	case ToolResultMessage:
		total := 0
		for _, b := range mm.Content {
			total += estimateBlock(b)
		}
		return total
	}
	return 0
}

func estimateBlock(b ContentBlock) int {
	switch bb := b.(type) {
	case TextContent:
		return (len(bb.Text) + charsPerToken - 1) / charsPerToken
	case ThinkingContent:
		return (len(bb.Thinking) + charsPerToken - 1) / charsPerToken
	case ImageContent:
		// ~1200 tokens typical for an image at low-res.
		return estimatedImageChars / charsPerToken
	case ToolCall:
		return (len(bb.Name)+len(bb.Arguments)+charsPerToken-1)/charsPerToken + 4
	}
	return 0
}
