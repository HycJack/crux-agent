package core

import (
	"strings"
	"testing"
)

func TestClampMaxTokensToContext(t *testing.T) {
	// No context window: just floor at min.
	if got := ClampMaxTokensToContext(Model{}, Context{}, 0); got != 1 {
		t.Errorf("min floor: got %d", got)
	}
	if got := ClampMaxTokensToContext(Model{}, Context{}, 100); got != 100 {
		t.Errorf("passthrough: got %d", got)
	}

	// 8K context, 1K prompt estimate, 4K safety -> max output = 3K.
	model := Model{ContextWindow: 8000}
	ctx := Context{SystemPrompt: strings.Repeat("a", 4000)} // ~1K tokens
	if got := ClampMaxTokensToContext(model, ctx, 10000); got > 3001 {
		// Allow 1-token rounding.
		t.Errorf("over-cap not clamped: got %d, want <= ~3000", got)
	}
}

func TestClampReasoning(t *testing.T) {
	if ClampReasoning("") != nil {
		t.Errorf("empty should return nil")
	}
	got := ClampReasoning("xhigh")
	if got == nil || *got != "high" {
		t.Errorf("xhigh should clamp to high, got %v", got)
	}
	got = ClampReasoning("medium")
	if got == nil || *got != "medium" {
		t.Errorf("medium should pass through, got %v", got)
	}
}

func TestAdjustMaxTokensForThinking(t *testing.T) {
	// No reasoning: no expansion.
	max, budget := AdjustMaxTokensForThinking(nil, 4096, "", nil)
	if max != 4096 || budget != 0 {
		t.Errorf("no-reasoning: got max=%d budget=%d", max, budget)
	}

	// Reasoning medium: budget 8192, no caller cap so max = modelMax (4096).
	// When modelMax is tight, the budget must shrink to leave 1024 for output.
	max, budget = AdjustMaxTokensForThinking(nil, 4096, "medium", nil)
	if max != 4096 {
		t.Errorf("max: got %d, want 4096", max)
	}
	wantShrunkBudget := 4096 - 1024 // 3072: max - minOutputTokens
	if budget != wantShrunkBudget {
		t.Errorf("medium budget: got %d, want %d (max-minOutput)", budget, wantShrunkBudget)
	}

	// Loose modelMax keeps the default budget.
	max, budget = AdjustMaxTokensForThinking(nil, 32000, "medium", nil)
	if max != 32000 {
		t.Errorf("max: got %d, want 32000", max)
	}
	if budget != 8192 {
		t.Errorf("default medium budget: got %d, want 8192", budget)
	}

	// Caller cap 2048 + budget 8192 = 10240 capped at modelMax 8192.
	customCap := 2048
	max, _ = AdjustMaxTokensForThinking(&customCap, 8192, "high", nil)
	if max != 8192 {
		t.Errorf("caller cap + budget should cap at modelMax, got %d", max)
	}

	// xhigh clamps to high.
	_, budget = AdjustMaxTokensForThinking(nil, 100000, "xhigh", nil)
	if budget != thinkingBudgetsDefault["high"] {
		t.Errorf("xhigh should fall back to high budget (%d), got %d", thinkingBudgetsDefault["high"], budget)
	}

	// Custom budget override.
	custom := map[string]int{"medium": 4096}
	_, budget = AdjustMaxTokensForThinking(nil, 100000, "medium", custom)
	if budget != 4096 {
		t.Errorf("custom budget: got %d", budget)
	}
}
