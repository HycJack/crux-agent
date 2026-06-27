package bedrock

import (
	"encoding/json"
	"strings"
	"testing"

	core "github.com/hycjack/crux-ai/core"
)

func emptyModel() core.Model {
	return core.Model{ID: "anthropic.claude-3-5-sonnet", Provider: "bedrock", API: "bedrock-converse-stream"}
}
func emptyContext() core.Context          { return core.Context{} }
func emptyStreamOpts() core.StreamOptions { return core.StreamOptions{} }

func TestBuildBedrockBody_ThinkingBudget_PrefersDirectField(t *testing.T) {
	body, err := buildBedrockBody(emptyModel(), emptyContext(), emptyStreamOpts(), Options{
		Reasoning:      true,
		ThinkingBudget: 4096,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("expected thinking config, got %T", body["thinking"])
	}
	if got := thinking["budgetTokens"]; got != 4096 {
		t.Errorf("budget: got %v, want 4096", got)
	}
}

func TestBuildBedrockBody_ThinkingBudget_FallsBackToMap(t *testing.T) {
	body, err := buildBedrockBody(emptyModel(), emptyContext(), emptyStreamOpts(), Options{
		Reasoning: true,
		ThinkingBudgets: map[string]int{
			"high": 8192,
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	thinking := body["thinking"].(map[string]any)
	if got := thinking["budgetTokens"]; got != 8192 {
		t.Errorf("fallback map: got %v, want 8192", got)
	}
}

func TestBuildBedrockBody_ThinkingBudget_DirectWinsOverMap(t *testing.T) {
	body, err := buildBedrockBody(emptyModel(), emptyContext(), emptyStreamOpts(), Options{
		Reasoning:      true,
		ThinkingBudget: 1024,
		ThinkingBudgets: map[string]int{
			"high": 99999, // would have been picked by old code path
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	thinking := body["thinking"].(map[string]any)
	if got := thinking["budgetTokens"]; got != 1024 {
		t.Errorf("direct field should win, got %v", got)
	}
}

func TestBuildBedrockBody_NoThinking_NotInBody(t *testing.T) {
	body, err := buildBedrockBody(emptyModel(), emptyContext(), emptyStreamOpts(), Options{
		Reasoning: false,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, has := body["thinking"]; has {
		t.Errorf("thinking key should be absent when Reasoning=false")
	}
}

// TestConvertAssistantContent_PreservesThinkingSignature guards the
// cross-provider (Anthropic → Bedrock) replay path. Without the fix,
// a thinking block's signature was silently dropped, which the downstream
// model rejects on the next turn.
func TestConvertAssistantContent_PreservesThinkingSignature(t *testing.T) {
	blocks := []core.ContentBlock{
		core.ThinkingContent{
			Type:              "thinking",
			Thinking:          "deep thought",
			ThinkingSignature: "sig-abc-123",
		},
	}
	out := convertAssistantContent(blocks)
	if len(out) != 1 {
		t.Fatalf("expected 1 block, got %d", len(out))
	}
	m, ok := out[0].(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", out[0])
	}
	thinking, ok := m["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("expected thinking map, got %T", m["thinking"])
	}
	if thinking["thinking"] != "deep thought" {
		t.Errorf("thinking.text: got %v", thinking["thinking"])
	}
	if thinking["signature"] != "sig-abc-123" {
		t.Errorf("thinking.signature: got %v (must be preserved)", thinking["signature"])
	}

	// Empty signature must NOT add the key (cleaner JSON output).
	blocks2 := []core.ContentBlock{
		core.ThinkingContent{Type: "thinking", Thinking: "no sig"},
	}
	out2 := convertAssistantContent(blocks2)
	thinking2 := out2[0].(map[string]any)["thinking"].(map[string]any)
	if _, has := thinking2["signature"]; has {
		t.Errorf("empty signature should not be serialised")
	}

	// Round-trip via JSON to confirm the field makes it through marshalling.
	raw, _ := json.Marshal(thinking)
	if !strings.Contains(string(raw), "sig-abc-123") {
		t.Errorf("signature missing from JSON output: %s", raw)
	}
}

func TestResolveThinkingBudget_NoLevel(t *testing.T) {
	if got := resolveThinkingBudget("", nil); got != 0 {
		t.Errorf("empty level -> 0, got %d", got)
	}
}

func TestResolveThinkingBudget_DefaultsPerLevel(t *testing.T) {
	cases := []struct {
		level core.ThinkingLevel
		want  int
	}{
		{core.ThinkingMinimal, 1024},
		{core.ThinkingLow, 2048},
		{core.ThinkingMedium, 8192},
		{core.ThinkingHigh, 16384},
		{core.ThinkingXHigh, 16384}, // xhigh clamps to high
	}
	for _, c := range cases {
		got := resolveThinkingBudget(c.level, nil)
		if got != c.want {
			t.Errorf("%s: got %d, want %d", c.level, got, c.want)
		}
	}
}

func TestResolveThinkingBudget_OverrideWins(t *testing.T) {
	overrides := map[string]int{"high": 99999}
	got := resolveThinkingBudget(core.ThinkingHigh, overrides)
	if got != 99999 {
		t.Errorf("override: got %d, want 99999", got)
	}
	// Unrelated level still falls through to default.
	got = resolveThinkingBudget(core.ThinkingMedium, overrides)
	if got != 8192 {
		t.Errorf("unrelated level: got %d, want 8192 (default)", got)
	}
}

func TestResolveThinkingBudget_EmptyMapFallsBackToDefault(t *testing.T) {
	// Caller explicitly passes an empty map: this is the
	// "give me a default" case.
	got := resolveThinkingBudget(core.ThinkingLow, map[string]int{})
	if got != 2048 {
		t.Errorf("empty map: got %d, want 2048 (default)", got)
	}
}
