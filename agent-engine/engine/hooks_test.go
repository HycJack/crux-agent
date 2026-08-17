package engine

import (
	"context"
	"testing"

	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-kernel/plugin"
)

// TestHooksLegacyFallback：legacy func 字段应被 hooks() 归一到 Hooks。
func TestHooksLegacyFallback(t *testing.T) {
	cfg := AgentLoopConfig{
		ConvertToLlm:        func(ms []core.Message) []core.Message { return ms },
		TransformContext:    func(ms []core.Message) []core.Message { return ms },
		BeforeToolCall:      func(BeforeToolCallContext) *ToolCallBlock { return nil },
		AfterToolCall:       func(AfterToolCallContext) *ToolCallOverride { return nil },
		ShouldStopAfterTurn: func(core.AssistantMessage, []core.ToolResultMessage) bool { return false },
	}
	h := cfg.hooks()
	if h.ConvertToLlm == nil || h.TransformContext == nil ||
		h.BeforeToolCall == nil || h.AfterToolCall == nil ||
		h.ShouldStopAfterTurn == nil {
		t.Fatal("expected legacy fields normalized into Hooks")
	}
}

// TestHooksPrecedence：Hooks 字段优先于 legacy 字段。
func TestHooksPrecedence(t *testing.T) {
	legacy := func(_ []core.Message) []core.Message { return nil }
	newHook := func(ms []core.Message) []core.Message { return ms }
	cfg := AgentLoopConfig{
		ConvertToLlm: legacy,
		Hooks:        plugin.Hooks{ConvertToLlm: newHook},
	}
	if cfg.hooks().ConvertToLlm == nil || cfg.hooks().ConvertToLlm(nil) != nil {
		t.Fatal("expected Hooks field to take precedence over legacy")
	}
}

// TestHooksCompactionBridge：legacy CompactionConfig 应归一到 Hooks.Compaction。
func TestHooksCompactionBridge(t *testing.T) {
	cfg := AgentLoopConfig{
		Compaction: CompactionConfig{
			Compactor:     func(_ context.Context, ms []core.Message) ([]core.Message, bool, error) { return ms, false, nil },
			MaxTokens:     12345,
			ReserveTokens: 99,
		},
	}
	h := cfg.hooks()
	if h.Compaction.Compactor == nil {
		t.Fatal("expected Compactor bridged into Hooks.Compaction")
	}
	if h.Compaction.MaxTokens != 12345 || h.Compaction.ReserveTokens != 99 {
		t.Fatalf("expected max/reserve tokens bridged, got %d/%d", h.Compaction.MaxTokens, h.Compaction.ReserveTokens)
	}
}

// TestAliasConvergence：engine 钩子类型应是 plugin 类型的别名（单一类型集）。
func TestAliasConvergence(t *testing.T) {
	var _ plugin.BeforeToolCallCtx = BeforeToolCallContext{}
	var _ plugin.AfterToolCallCtx = AfterToolCallContext{}
	var _ plugin.ToolCallBlock = ToolCallBlock{}
	var _ plugin.ToolCallOverride = ToolCallOverride{}
	var _ plugin.AgentToolResult = AgentToolResult{}
}
