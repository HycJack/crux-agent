package session

import (
	"context"
	"fmt"
	"testing"

	"github.com/hycjack/crux-ai/core"
)

func TestSession_Fork(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	// Add some messages
	sess.Append(NewUserMessageEntry("Hello"))
	sess.Append(NewAssistantMessageEntry(core.AssistantMessage{
		Role:    core.MessageRoleAssistant,
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Hi!"}},
	}))
	sess.Append(NewUserMessageEntry("How are you?"))

	// Fork
	config := DefaultBranchConfig()
	branch, err := sess.Fork(context.Background(), "test-branch", config)
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	if branch.Name != "test-branch" {
		t.Errorf("expected branch name 'test-branch', got %q", branch.Name)
	}
	if branch.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(branch.Messages) < 3 {
		t.Errorf("expected at least 3 messages, got %d", len(branch.Messages))
	}
}

func TestSession_Fork_WithSummary(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	sess.Append(NewUserMessageEntry("I want to optimize this function"))
	sess.Append(NewAssistantMessageEntry(core.AssistantMessage{
		Role:    core.MessageRoleAssistant,
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Here's how..."}},
	}))

	config := BranchConfig{
		MaxBranches: 10,
		AutoSummary: true,
		SummaryFunc: func(ctx context.Context, title string, msgs []SessionTreeEntry) (string, error) {
			return "Custom summary for " + title, nil
		},
	}

	branch, err := sess.Fork(context.Background(), "optimization", config)
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	if branch.Summary != "Custom summary for optimization" {
		t.Errorf("expected custom summary, got %q", branch.Summary)
	}
}

func TestSession_SwitchTo(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	sess.Append(NewUserMessageEntry("Main branch message"))

	// Fork
	config := DefaultBranchConfig()
	branch, _ := sess.Fork(context.Background(), "branch-1", config)

	// Add message to current branch
	sess.Append(NewUserMessageEntry("After fork"))

	// Switch to branch
	err := sess.SwitchTo(branch.ID)
	if err != nil {
		t.Fatalf("SwitchTo failed: %v", err)
	}

	entries := sess.Entries()
	// Branch should have the messages from before fork + summary
	if len(entries) < 2 {
		t.Errorf("expected at least 2 entries, got %d", len(entries))
	}
}

func TestSession_SwitchTo_Invalid(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	err := sess.SwitchTo("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent branch")
	}
}

func TestSession_Branches(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	sess.Append(NewUserMessageEntry("Message"))

	config := DefaultBranchConfig()
	sess.Fork(context.Background(), "branch-1", config)
	sess.Fork(context.Background(), "branch-2", config)

	branches := sess.Branches()
	if len(branches) != 2 {
		t.Errorf("expected 2 branches, got %d", len(branches))
	}
}

func TestSession_DeleteBranch(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	sess.Append(NewUserMessageEntry("Message"))

	config := DefaultBranchConfig()
	branch, _ := sess.Fork(context.Background(), "branch-1", config)

	// Delete branch
	err := sess.DeleteBranch(branch.ID)
	if err != nil {
		t.Fatalf("DeleteBranch failed: %v", err)
	}

	branches := sess.Branches()
	if len(branches) != 0 {
		t.Errorf("expected 0 branches, got %d", len(branches))
	}
}

func TestSession_DeleteBranch_Current(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	sess.Append(NewUserMessageEntry("Message"))

	config := DefaultBranchConfig()
	branch, _ := sess.Fork(context.Background(), "branch-1", config)

	// Switch to branch
	sess.SwitchTo(branch.ID)

	// Try to delete current branch
	err := sess.DeleteBranch(branch.ID)
	if err == nil {
		t.Error("expected error when deleting current branch")
	}
}

func TestSession_Fork_MaxBranches(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	sess.Append(NewUserMessageEntry("Message"))

	config := BranchConfig{
		MaxBranches: 2,
		AutoSummary: false,
	}

	sess.Fork(context.Background(), "branch-1", config)
	sess.Fork(context.Background(), "branch-2", config)

	// Third fork should fail
	_, err := sess.Fork(context.Background(), "branch-3", config)
	if err == nil {
		t.Error("expected error when exceeding max branches")
	}
}

func TestTruncateSummary(t *testing.T) {
	msgs := []SessionTreeEntry{
		NewUserMessageEntry("How do I optimize this?"),
		NewAssistantMessageEntry(core.AssistantMessage{
			Role:    core.MessageRoleAssistant,
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Try using caching..."}},
		}),
		NewUserMessageEntry("Let me try that"),
	}

	summary := TruncateSummary(msgs, 5)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if !containsString(summary, "Branch summary") {
		t.Error("expected 'Branch summary' in output")
	}
}

func TestTruncateSummary_Empty(t *testing.T) {
	summary := TruncateSummary(nil, 5)
	if summary != "" {
		t.Errorf("expected empty summary, got %q", summary)
	}
}

func TestLLMSummary_NilFunc(t *testing.T) {
	msgs := []SessionTreeEntry{
		NewUserMessageEntry("test"),
	}

	summary, err := LLMSummary(context.Background(), nil, "test", msgs)
	if err != nil {
		t.Fatalf("LLMSummary failed: %v", err)
	}
	if summary == "" {
		t.Error("expected non-empty summary from truncation fallback")
	}
}

func TestLLMSummary_WithFunc(t *testing.T) {
	completeFn := func(ctx context.Context, prompt string) (string, error) {
		return "LLM generated summary", nil
	}

	msgs := []SessionTreeEntry{
		NewUserMessageEntry("test"),
	}

	summary, err := LLMSummary(context.Background(), completeFn, "test", msgs)
	if err != nil {
		t.Fatalf("LLMSummary failed: %v", err)
	}
	if summary != "LLM generated summary" {
		t.Errorf("expected 'LLM generated summary', got %q", summary)
	}
}

func TestLLMSummary_Error(t *testing.T) {
	completeFn := func(ctx context.Context, prompt string) (string, error) {
		return "", fmt.Errorf("LLM error")
	}

	msgs := []SessionTreeEntry{
		NewUserMessageEntry("test"),
	}

	// Should degrade to truncation
	summary, err := LLMSummary(context.Background(), completeFn, "test", msgs)
	if err != nil {
		t.Fatalf("LLMSummary should degrade gracefully: %v", err)
	}
	if summary == "" {
		t.Error("expected non-empty summary from fallback")
	}
}

func TestAppendSummaryMessage(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	err := AppendSummaryMessage(sess, "Test summary")
	if err != nil {
		t.Fatalf("AppendSummaryMessage failed: %v", err)
	}

	entries := sess.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Type != EntrySystemPrompt {
		t.Errorf("expected EntrySystemPrompt, got %q", entries[0].Type)
	}
}

func TestAppendSummaryMessage_Empty(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	err := AppendSummaryMessage(sess, "")
	if err != nil {
		t.Fatalf("AppendSummaryMessage failed: %v", err)
	}

	entries := sess.Entries()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && findSubstring(s, sub) >= 0
}

func findSubstring(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
