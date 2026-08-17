package defaults

import (
	"context"
	"testing"

	"github.com/hycjack/agent-engine/ctx"
	"github.com/hycjack/crux-kernel/plugin"
	core "github.com/hycjack/crux-ai/core"
)

// ─── TestContextPipelinePlugin：Compaction 真实生效（滑窗保留首尾）─────────

func TestContextPipelinePluginCompaction(t *testing.T) {
	pipeline, err := NewContextPipeline(core.Model{ID: "gpt-4o-mini"}, 128000)
	if err != nil {
		t.Fatalf("NewContextPipeline: %v", err)
	}

	x := ctx.New()
	if err := BundleDefault(x, BundleOpts{Pipeline: pipeline}); err != nil {
		t.Fatalf("BundleDefault: %v", err)
	}

	h := x.Hooks()
	if h.Compaction.Compactor == nil {
		t.Fatal("Compaction.Compactor 未挂载")
	}

	// 造超过 minMessagesToKeep+2 的消息，验证滑窗压缩保留首尾
	var msgs []core.Message
	for i := 0; i < 20; i++ {
		msgs = append(msgs, core.UserMessage{Role: "user", Content: "msg"})
	}
	newMsgs, changed, err := h.Compaction.Compactor(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Compactor: %v", err)
	}
	if !changed {
		t.Fatal("20 条消息应触发压缩 changed=true")
	}
	if len(newMsgs) >= len(msgs) {
		t.Fatalf("压缩后应更短, got %d -> %d", len(msgs), len(newMsgs))
	}
}

// ─── TestApprovalPlugin：Block 规则真实拦截 BeforeToolCall ────────────────

func TestApprovalPluginBlock(t *testing.T) {
	gate := NewApprovalGate()
	gate.AddRule(RuleNameContains("danger", plugin.ApprovalBlock, "blocked"))

	x := ctx.New()
	if err := BundleDefault(x, BundleOpts{Approval: gate}); err != nil {
		t.Fatalf("BundleDefault: %v", err)
	}

	h := x.Hooks()
	if h.BeforeToolCall == nil {
		t.Fatal("BeforeToolCall 未挂载")
	}

	block := h.BeforeToolCall(plugin.BeforeToolCallCtx{
		ToolCall: core.ToolCall{
			Name: "danger_cmd",
			ID:   "call-1",
			Arguments: []byte("{}"),
		},
	})
	if block == nil || !block.Block {
		t.Fatalf("danger 工具应被拦截, got %+v", block)
	}

	// 默认放行其他工具
	allow := h.BeforeToolCall(plugin.BeforeToolCallCtx{
		ToolCall: core.ToolCall{Name: "safe_cmd", ID: "call-2", Arguments: []byte("{}")},
	})
	if allow != nil {
		t.Fatalf("safe 工具应放行, got %+v", allow)
	}
}

// ─── TestSessionAndMemoryPlugins：服务注册 + 生命周期 ─────────────────────

func TestSessionMemoryServiceAndLifecycle(t *testing.T) {
	dir := t.TempDir()
	mem, err := NewMemory(dir + "/mem.json")
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	sess, err := NewJSONLSession(dir + "/session.jsonl")
	if err != nil {
		t.Fatalf("NewJSONLSession: %v", err)
	}

	x := ctx.New()
	if err := BundleDefault(x, BundleOpts{Session: sess, Mem: mem}); err != nil {
		t.Fatalf("BundleDefault: %v", err)
	}

	// 服务可被发现
	var m *Memory
	if err := x.Get(&m); err != nil || m == nil {
		t.Fatalf("Memory 服务未注册: %v", err)
	}
	var s *JSONLSession
	if err := x.Get(&s); err != nil || s == nil {
		t.Fatalf("Session 服务未注册: %v", err)
	}

	// Dispose 触发逆向清理（本实现 Session 的 Mount 返回 Close）
	if err := x.Dispose(); err != nil {
		t.Fatalf("Dispose: %v", err)
	}
}
