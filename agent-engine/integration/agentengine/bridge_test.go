package agentengine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/crux-kernel/plugin"
	"github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-kernel/container"
)

// --- mocks ---

type mockApproval struct {
	result plugin.ApprovalResult
	reason string
	err    error
}

func (m *mockApproval) Evaluate(ctx context.Context, toolName, toolID string, args []byte) (plugin.ApprovalResult, string, error) {
	return m.result, m.reason, m.err
}

// --- FromContainer ---

func TestFromContainer(t *testing.T) {
	c := container.New()
	model := core.Model{ID: "test-model", Name: "Test"}
	tools := []engine.AgentTool{{Name: "bash"}}
	comp := engine.CompactionConfig{MaxTokens: 1000}

	c.Register(model)
	c.Register(tools)
	c.Register(comp)

	opts := FromContainer(c)

	if opts.InitialState == nil {
		t.Fatal("InitialState 不应为 nil")
	}
	if opts.InitialState.Model.ID != "test-model" {
		t.Fatalf("Model.ID 错误，got %s", opts.InitialState.Model.ID)
	}
	if len(opts.InitialState.Tools) != 1 || opts.InitialState.Tools[0].Name != "bash" {
		t.Fatalf("Tools 错误，got %+v", opts.InitialState.Tools)
	}
	if opts.Compaction.MaxTokens != 1000 {
		t.Fatalf("Compaction.MaxTokens 错误，got %d", opts.Compaction.MaxTokens)
	}
}

func TestFromContainer_Empty(t *testing.T) {
	c := container.New()
	opts := FromContainer(c)

	if opts.InitialState == nil {
		t.Fatal("InitialState 不应为 nil（即使容器为空）")
	}
	if opts.InitialState.Model.ID != "" {
		t.Fatalf("空容器 Model.ID 应为空，got %s", opts.InitialState.Model.ID)
	}
	if len(opts.InitialState.Tools) != 0 {
		t.Fatalf("空容器 Tools 应为空，got %+v", opts.InitialState.Tools)
	}
}

// --- ApplyApproval ---

func TestApplyApproval_Block(t *testing.T) {
	c := container.New()
	ap := &mockApproval{result: plugin.ApprovalBlock, reason: "blocked by test"}
	c.RegisterAs(ap, (*plugin.ApprovalPlugin)(nil))

	cfg := &engine.AgentLoopConfig{}
	if err := ApplyApproval(cfg, c, nil); err != nil {
		t.Fatalf("ApplyApproval 失败: %v", err)
	}
	if cfg.BeforeToolCall == nil {
		t.Fatal("BeforeToolCall 未设置")
	}

	block := cfg.BeforeToolCall(engine.BeforeToolCallContext{
		ToolCall: core.ToolCall{ID: "tc1", Name: "bash"},
		Args:     json.RawMessage(`{}`),
	})
	if block == nil || !block.Block {
		t.Fatal("ApprovalBlock 应返回阻塞")
	}
	if block.Reason != "blocked by test" {
		t.Fatalf("原因错误，got %s", block.Reason)
	}
}

func TestApplyApproval_Allow(t *testing.T) {
	c := container.New()
	ap := &mockApproval{result: plugin.ApprovalAllow}
	c.RegisterAs(ap, (*plugin.ApprovalPlugin)(nil))

	cfg := &engine.AgentLoopConfig{}
	if err := ApplyApproval(cfg, c, nil); err != nil {
		t.Fatalf("ApplyApproval 失败: %v", err)
	}

	block := cfg.BeforeToolCall(engine.BeforeToolCallContext{
		ToolCall: core.ToolCall{ID: "tc1", Name: "bash"},
		Args:     json.RawMessage(`{}`),
	})
	if block != nil {
		t.Fatalf("ApprovalAllow 应返回 nil，got %+v", block)
	}
}

func TestApplyApproval_Ask_NoHandler(t *testing.T) {
	c := container.New()
	ap := &mockApproval{result: plugin.ApprovalAsk}
	c.RegisterAs(ap, (*plugin.ApprovalPlugin)(nil))

	cfg := &engine.AgentLoopConfig{}
	_ = ApplyApproval(cfg, c, nil) // askHandler = nil

	block := cfg.BeforeToolCall(engine.BeforeToolCallContext{
		ToolCall: core.ToolCall{ID: "tc1", Name: "bash"},
		Args:     json.RawMessage(`{}`),
	})
	if block == nil || !block.Block {
		t.Fatal("ApprovalAsk 无 handler 应默认阻塞")
	}
}

func TestApplyApproval_Ask_WithHandler(t *testing.T) {
	c := container.New()
	ap := &mockApproval{result: plugin.ApprovalAsk}
	c.RegisterAs(ap, (*plugin.ApprovalPlugin)(nil))

	cfg := &engine.AgentLoopConfig{}
	askHandler := func(ctx context.Context, toolName, toolID string, args []byte) (plugin.ApprovalResult, string, error) {
		return plugin.ApprovalAllow, "user approved", nil
	}
	_ = ApplyApproval(cfg, c, askHandler)

	block := cfg.BeforeToolCall(engine.BeforeToolCallContext{
		ToolCall: core.ToolCall{ID: "tc1", Name: "bash"},
		Args:     json.RawMessage(`{}`),
	})
	if block != nil {
		t.Fatalf("AskHandler 放行后应返回 nil，got %+v", block)
	}
}

func TestApplyApproval_Error(t *testing.T) {
	c := container.New()
	ap := &mockApproval{err: errors.New("eval failed")}
	c.RegisterAs(ap, (*plugin.ApprovalPlugin)(nil))

	cfg := &engine.AgentLoopConfig{}
	_ = ApplyApproval(cfg, c, nil)

	block := cfg.BeforeToolCall(engine.BeforeToolCallContext{
		ToolCall: core.ToolCall{ID: "tc1", Name: "bash"},
		Args:     json.RawMessage(`{}`),
	})
	if block == nil || !block.Block {
		t.Fatal("Evaluate 出错应阻塞")
	}
}

func TestApplyApproval_NotFound(t *testing.T) {
	c := container.New()
	cfg := &engine.AgentLoopConfig{}
	err := ApplyApproval(cfg, c, nil)
	if err == nil {
		t.Fatal("未注册 ApprovalPlugin 应返回错误")
	}
}

// --- AgentEventType ---

func TestAgentEventType(t *testing.T) {
	tests := []struct {
		evt  engine.AgentEvent
		want string
	}{
		{engine.EventAgentStart{}, "agent_start"},
		{engine.EventAgentEnd{}, "agent_end"},
		{engine.EventTurnStart{}, "turn_start"},
		{engine.EventTurnEnd{}, "turn_end"},
		{engine.EventMessageStart{}, "message_start"},
		{engine.EventMessageEnd{}, "message_end"},
		{engine.EventToolExecStart{}, "tool_exec_start"},
		{engine.EventToolExecEnd{}, "tool_exec_end"},
		{engine.EventRetry{}, "retry"},
	}
	for _, tt := range tests {
		if got := AgentEventType(tt.evt); got != tt.want {
			t.Errorf("AgentEventType(%T) = %q, want %q", tt.evt, got, tt.want)
		}
	}
}

func TestWrapEvent(t *testing.T) {
	evt := engine.EventToolExecStart{ToolCallID: "tc1", ToolName: "bash"}
	wrapped := WrapEvent(evt)
	if wrapped.Type() != "tool_exec_start" {
		t.Fatalf("Type 错误，got %s", wrapped.Type())
	}
	if wrapped.Timestamp().IsZero() {
		t.Fatal("Timestamp 不应为零值")
	}
}
