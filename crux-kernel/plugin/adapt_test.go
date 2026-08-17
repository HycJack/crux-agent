package plugin

import (
	"context"
	"testing"

	core "github.com/hycjack/crux-ai/core"
)

// fakeApproval 是 ApprovalPlugin 的测试桩。
type fakeApproval struct {
	result ApprovalResult
	reason string
	err    error
}

func (f *fakeApproval) Evaluate(_ context.Context, toolName, toolID string, _ []byte) (ApprovalResult, string, error) {
	return f.result, f.reason, f.err
}

func TestMapApproval_Allow(t *testing.T) {
	h := MapApproval(&fakeApproval{result: ApprovalAllow}, nil)
	block := h(BeforeToolCallCtx{ToolCall: core.ToolCall{Name: "ls", ID: "1"}})
	if block != nil {
		t.Fatalf("expected nil block, got %+v", block)
	}
}

func TestMapApproval_Block(t *testing.T) {
	h := MapApproval(&fakeApproval{result: ApprovalBlock, reason: "denied"}, nil)
	block := h(BeforeToolCallCtx{ToolCall: core.ToolCall{Name: "rm", ID: "2"}})
	if block == nil || !block.Block || block.Reason != "denied" {
		t.Fatalf("expected block with reason 'denied', got %+v", block)
	}
}

func TestMapApproval_Ask_DefaultBlock(t *testing.T) {
	h := MapApproval(&fakeApproval{result: ApprovalAsk}, nil)
	block := h(BeforeToolCallCtx{ToolCall: core.ToolCall{Name: "curl", ID: "3"}})
	if block == nil || !block.Block {
		t.Fatalf("ask with nil handler should default-block, got %+v", block)
	}
}

func TestMapApproval_Ask_HandlerAllow(t *testing.T) {
	h := MapApproval(&fakeApproval{result: ApprovalAsk}, func(_ context.Context, _, _ string, _ []byte) (ApprovalResult, string, error) {
		return ApprovalAllow, "", nil
	})
	block := h(BeforeToolCallCtx{ToolCall: core.ToolCall{Name: "curl", ID: "4"}})
	if block != nil {
		t.Fatalf("ask with allowing handler should pass, got %+v", block)
	}
}

func TestMapApproval_Error(t *testing.T) {
	h := MapApproval(&fakeApproval{err: context.DeadlineExceeded}, nil)
	block := h(BeforeToolCallCtx{ToolCall: core.ToolCall{Name: "x", ID: "5"}})
	if block == nil || !block.Block {
		t.Fatalf("evaluate error should block, got %+v", block)
	}
}

// fakeContext 是 ContextPlugin 的测试桩。
type fakeContext struct {
	msgs []core.Message
	comp int
}

func (f *fakeContext) AddMessage(m core.Message) error { f.msgs = append(f.msgs, m); return nil }
func (f *fakeContext) GetMessages() []core.Message     { return f.msgs }
func (f *fakeContext) IsNearLimit(float64) bool        { return true }
func (f *fakeContext) Compact(context.Context) error   { f.comp++; return nil }
func (f *fakeContext) GetStats() ContextStats {
	return ContextStats{MaxTokens: 100000, MessageCount: len(f.msgs)}
}

func TestMapContext(t *testing.T) {
	fc := &fakeContext{}
	transform, comp := MapContext(fc, nil)
	if transform == nil {
		t.Fatal("transform should be non-nil")
	}
	// IsNearLimit=true → Compact called once
	if fc.comp != 0 {
		t.Fatalf("unexpected compaction count %d", fc.comp)
	}
	_ = transform([]core.Message{})
	if fc.comp != 1 {
		t.Fatalf("expected compaction on near-limit, got %d", fc.comp)
	}
	if comp.Compactor != nil {
		t.Fatal("nil compactor should leave Compaction.Compactor nil")
	}
	// 未提供 compactor 时，CompactionHooks 保持零值（MaxTokens 也不设置）
	if comp.MaxTokens != 0 {
		t.Fatalf("nil compactor should not set MaxTokens, got %d", comp.MaxTokens)
	}

	// 提供了 compactor 时，MaxTokens 从 ContextStats 读取
	_, comp2 := MapContext(fc, func(context.Context, []core.Message) ([]core.Message, bool, error) { return nil, false, nil })
	if comp2.Compactor == nil {
		t.Fatal("compactor should be set")
	}
	if comp2.MaxTokens != 100000 {
		t.Fatalf("expected MaxTokens=100000, got %d", comp2.MaxTokens)
	}
}
