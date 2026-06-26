package core

// =============================================================================
// Stream option clamping and reasoning-budget adjustment.
//
// Reference: pi-mono packages/ai/src/api/simple-options.ts
//
// Two related concerns:
//  1. Clamp the caller's MaxTokens so it never exceeds
//     (contextWindow - estimatedInput - safetyMargin).
//  2. When reasoning is enabled, expand maxTokens to fit the configured
//     thinking budget for the chosen level.
// =============================================================================

// contextSafetyTokens is the buffer reserved for the model's own
// tool-call/format overhead above the user's prompt estimate. 4K matches
// pi-mono's default.
const contextSafetyTokens = 4096

// minMaxTokens guards against a 0/negative cap.
const minMaxTokens = 1

// thinkingBudgetsDefault mirrors pi-mono's default per-level budgets. These
// are tokens reserved for the model's thinking block; they add to the
// visible maxTokens when reasoning is enabled.
var thinkingBudgetsDefault = map[string]int{
	"minimal": 1024,
	"low":     2048,
	"medium":  8192,
	"high":    16384,
}

// ClampMaxTokensToContext reduces maxTokens so the model can fit:
//   maxTokens + estimatedInput + safety <= contextWindow
//
// If model.ContextWindow is <= 0 the caller is unconstrained; we just
// ensure maxTokens >= 1.
func ClampMaxTokensToContext(model Model, ctx Context, maxTokens int) int {
	if model.ContextWindow <= 0 {
		if maxTokens < minMaxTokens {
			return minMaxTokens
		}
		return maxTokens
	}
	estimated := EstimateContextTokens(ctx)
	available := model.ContextWindow - estimated - contextSafetyTokens
	if available < minMaxTokens {
		available = minMaxTokens
	}
	if maxTokens > available {
		return available
	}
	if maxTokens < minMaxTokens {
		return minMaxTokens
	}
	return maxTokens
}

// ClampReasoning drops unsupported thinking levels. "xhigh" is not
// recognized by any current provider, so it is mapped to "high".
// Returns nil for the empty level.
func ClampReasoning(level ThinkingLevel) *ThinkingLevel {
	if level == "" {
		return nil
	}
	if level == "xhigh" {
		clamped := ThinkingLevel("high")
		return &clamped
	}
	return &level
}

// AdjustMaxTokensForThinking computes the right (maxTokens, thinkingBudget)
// pair for a given reasoning level.
//
// baseMaxTokens nil means "use the model cap"; otherwise it is honored as
// the caller's hard ceiling on output.
//
// If the model cap is too tight to fit both the budget and the minimum
// output budget (1024), the thinking budget is shrunk to fit.
func AdjustMaxTokensForThinking(
	baseMaxTokens *int,
	modelMaxTokens int,
	level ThinkingLevel,
	customBudgets map[string]int,
) (maxTokens int, thinkingBudget int) {
	if level == "" {
		// No reasoning: just return the caller's max.
		if baseMaxTokens != nil {
			return *baseMaxTokens, 0
		}
		return modelMaxTokens, 0
	}
	minOutputTokens := 1024
	budgets := make(map[string]int, len(thinkingBudgetsDefault))
	for k, v := range thinkingBudgetsDefault {
		budgets[k] = v
	}
	for k, v := range customBudgets {
		budgets[k] = v
	}
	clampedLevel := level
	if clampedLevel == "xhigh" {
		clampedLevel = "high"
	}
	thinkingBudget = budgets[string(clampedLevel)]
	if thinkingBudget == 0 {
		thinkingBudget = budgets["medium"]
	}

	max := modelMaxTokens
	if baseMaxTokens != nil {
		max = *baseMaxTokens + thinkingBudget
		if max > modelMaxTokens && modelMaxTokens > 0 {
			max = modelMaxTokens
		}
	}
	if max <= thinkingBudget {
		thinkingBudget = max - minOutputTokens
		if thinkingBudget < 0 {
			thinkingBudget = 0
		}
	}
	return max, thinkingBudget
}

// EstimateContextTokens is a thin wrapper around EstimateTokens for
// the simple-options use case.
func EstimateContextTokens(ctx Context) int {
	return EstimateTokens(ctx.SystemPrompt, ctx.Messages, ctx.Tools)
}
