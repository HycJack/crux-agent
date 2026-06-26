package core

// =============================================================================
// OpenAI prompt cache key utilities.
//
// Reference: pi-mono packages/ai/src/api/openai-prompt-cache.ts
//
// The OpenAI Responses API accepts a `prompt_cache_key` to associate
// subsequent requests with the same cache prefix. The accepted value is
// at most 64 characters; longer keys are silently truncated by the API,
// which is almost never what callers want. ClampOpenAIPromptCacheKey
// makes that truncation explicit and UTF-8 safe.
// =============================================================================

// OpenAIPromptCacheKeyMaxLength is the documented limit for the
// prompt_cache_key field on the OpenAI Responses API.
const OpenAIPromptCacheKeyMaxLength = 64

// ClampOpenAIPromptCacheKey truncates key to OpenAIPromptCacheKeyMaxLength
// runes (not bytes) when it would otherwise exceed the limit. Returns
// nil for nil/empty input to preserve the "no key" semantics.
func ClampOpenAIPromptCacheKey(key *string) *string {
	if key == nil || *key == "" {
		return nil
	}
	runes := []rune(*key)
	if len(runes) <= OpenAIPromptCacheKeyMaxLength {
		out := *key
		return &out
	}
	clamped := string(runes[:OpenAIPromptCacheKeyMaxLength])
	return &clamped
}
