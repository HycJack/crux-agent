package harnessreg

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hycjack/agent-engine/harness/approval"
	"github.com/hycjack/agent-engine/harness/checkpoint"
	ctxm "github.com/hycjack/agent-engine/harness/context"
	"github.com/hycjack/agent-engine/harness/session"

	"github.com/hycjack/crux-kernel/plugin"
	"github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-kernel/container"
)

// --- RegisterApproval ---

func TestRegisterApproval_Default(t *testing.T) {
	c := container.New()
	if err := RegisterApproval(c, ApprovalConfig{}); err != nil {
		t.Fatalf("RegisterApproval 失败: %v", err)
	}

	var gate *approval.Gate
	if err := c.Get(&gate); err != nil {
		t.Fatalf("Get *approval.Gate 失败: %v", err)
	}

	// DangerousTools 默认匹配 bash → Ask
	result := gate.Evaluate(approval.Request{ToolName: "bash", Args: json.RawMessage(`{}`)})
	if result.Decision != approval.DecisionAsk {
		t.Fatalf("bash 应触发 Ask，got %v", result.Decision)
	}

	// 未匹配的 tool → default Allow
	result = gate.Evaluate(approval.Request{ToolName: "read_file", Args: json.RawMessage(`{}`)})
	if result.Decision != approval.DecisionAllow {
		t.Fatalf("未匹配规则应默认 Allow，got %v", result.Decision)
	}
}

func TestRegisterApproval_WithGate(t *testing.T) {
	c := container.New()
	gate := approval.NewStrict()
	if err := RegisterApproval(c, ApprovalConfig{Gate: gate}); err != nil {
		t.Fatalf("RegisterApproval 失败: %v", err)
	}

	var got *approval.Gate
	_ = c.Get(&got)
	if got != gate {
		t.Fatal("注册的 Gate 应为传入的实例")
	}

	result := gate.Evaluate(approval.Request{ToolName: "any", Args: json.RawMessage(`{}`)})
	if result.Decision != approval.DecisionBlock {
		t.Fatalf("Strict 默认应 Block，got %v", result.Decision)
	}
}

// --- RegisterContext ---

func TestRegisterContext(t *testing.T) {
	c := container.New()
	cfg := ctxm.PipelineConfig{
		Model:               core.Model{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
		Budget:              ctxm.DefaultBudget(128000),
		CompactionThreshold: 0.9,
		MinMessagesToKeep:   10,
	}
	if err := RegisterContext(c, cfg); err != nil {
		t.Fatalf("RegisterContext 失败: %v", err)
	}

	var p *ctxm.Pipeline
	if err := c.Get(&p); err != nil {
		t.Fatalf("Get *ctxm.Pipeline 失败: %v", err)
	}
	if p == nil {
		t.Fatal("Pipeline 为 nil")
	}
}

func TestRegisterContext_FallbackModel(t *testing.T) {
	c := container.New()
	cfg := ctxm.PipelineConfig{
		// 未知 model.ID，token.New 会 fallback 但不报错
		Model: core.Model{ID: "unknown-xxx-yyy", Name: "Unknown"},
	}
	// token.New 在 tiktoken 不可用时进入 fallback 模式，不返回错误
	if err := RegisterContext(c, cfg); err != nil {
		t.Fatalf("fallback 模式应能创建 Pipeline，got: %v", err)
	}

	var p *ctxm.Pipeline
	if err := c.Get(&p); err != nil {
		t.Fatalf("Get *ctxm.Pipeline 失败: %v", err)
	}
}

// --- RegisterSession ---

func TestRegisterSession(t *testing.T) {
	c := container.New()
	tmpDir := filepath.Join(os.TempDir(), "harnessreg-test-session")
	defer os.RemoveAll(tmpDir)

	if err := RegisterSession(c, tmpDir); err != nil {
		t.Fatalf("RegisterSession 失败: %v", err)
	}

	var mgr *session.SessionManager
	if err := c.Get(&mgr); err != nil {
		t.Fatalf("Get *session.SessionManager 失败: %v", err)
	}
	if mgr == nil {
		t.Fatal("SessionManager 为 nil")
	}
}

func TestRegisterSession_InvalidDir(t *testing.T) {
	c := container.New()
	// 用一个无法创建的路径（在文件上建目录）
	tmpFile := filepath.Join(os.TempDir(), "harnessreg-test-file")
	os.WriteFile(tmpFile, []byte("x"), 0644)
	defer os.Remove(tmpFile)

	err := RegisterSession(c, tmpFile)
	if err == nil {
		t.Fatal("无效目录应返回错误")
	}
}

// --- RegisterCheckpoint ---

func TestRegisterCheckpoint(t *testing.T) {
	c := container.New()
	if err := RegisterCheckpoint(c); err != nil {
		t.Fatalf("RegisterCheckpoint 失败: %v", err)
	}

	var store *checkpoint.Store
	if err := c.Get(&store); err != nil {
		t.Fatalf("Get *checkpoint.Store 失败: %v", err)
	}
	if store == nil {
		t.Fatal("Store 为 nil")
	}
}

// --- RegisterApprovalAsPlugin ---

func TestRegisterApprovalAsPlugin(t *testing.T) {
	c := container.New()
	if err := RegisterApprovalAsPlugin(c, ApprovalConfig{}); err != nil {
		t.Fatalf("RegisterApprovalAsPlugin 失败: %v", err)
	}

	// 1. 按具体类型查询原始 Gate（用于 AddRule）
	var gate *approval.Gate
	if err := c.Get(&gate); err != nil {
		t.Fatalf("按 *approval.Gate 查询失败: %v", err)
	}

	// 2. 按接口类型查询 ApprovalPlugin
	var ap plugin.ApprovalPlugin
	if err := c.Get(&ap); err != nil {
		t.Fatalf("按 plugin.ApprovalPlugin 查询失败: %v", err)
	}

	// 3. 调用接口方法，验证适配正确
	result, reason, err := ap.Evaluate(context.Background(), "bash", "tc1", []byte(`{}`))
	if err != nil {
		t.Fatalf("Evaluate 出错: %v", err)
	}
	if result != plugin.ApprovalAsk {
		t.Fatalf("bash 应触发 ApprovalAsk，got %v", result)
	}
	if reason == "" {
		t.Fatal("reason 不应为空")
	}

	// 4. 验证 Allow 路径
	result2, _, _ := ap.Evaluate(context.Background(), "read_file", "tc2", []byte(`{}`))
	if result2 != plugin.ApprovalAllow {
		t.Fatalf("read_file 应 ApprovalAllow，got %v", result2)
	}
}

func TestRegisterApprovalAsPlugin_DirectAddRule(t *testing.T) {
	c := container.New()
	// 用空 preset 注册（无规则，default Allow）
	if err := RegisterApprovalAsPlugin(c, ApprovalConfig{Preset: ApprovalAllowByDefault}); err != nil {
		t.Fatalf("RegisterApprovalAsPlugin 失败: %v", err)
	}

	var gate *approval.Gate
	_ = c.Get(&gate)

	// 动态添加规则（这是第一条规则，first-match-wins）
	gate.AddRule(approval.Rule{
		Name:    "block_all_writes",
		Match:   approval.MatchByName("write_file"),
		Approve: approval.DecisionBlock,
		Reason:  "writes blocked at runtime",
	})

	var ap plugin.ApprovalPlugin
	_ = c.Get(&ap)

	// write_file 现在应被阻塞
	result, reason, _ := ap.Evaluate(context.Background(), "write_file", "tc1", []byte(`{}`))
	if result != plugin.ApprovalBlock {
		t.Fatalf("write_file 应 ApprovalBlock，got %v", result)
	}
	if reason != "writes blocked at runtime" {
		t.Fatalf("reason 错误，got %q", reason)
	}

	// read_file 没匹配规则 → default Allow
	result2, _, _ := ap.Evaluate(context.Background(), "read_file", "tc2", []byte(`{}`))
	if result2 != plugin.ApprovalAllow {
		t.Fatalf("read_file 应 ApprovalAllow，got %v", result2)
	}
}
