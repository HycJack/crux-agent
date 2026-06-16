package session

import (
	"testing"

	"github.com/hycjack/crux-ai/core"
)

func TestNewSession(t *testing.T) {
	storage := NewMemoryStorage()
	sess, err := NewSession(storage)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if sess.ID() != "" {
		t.Errorf("expected empty ID, got %q", sess.ID())
	}
}

func TestSession_SetID(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	if err := sess.SetID("test-session-123"); err != nil {
		t.Fatalf("SetID failed: %v", err)
	}
	if sess.ID() != "test-session-123" {
		t.Errorf("expected ID %q, got %q", "test-session-123", sess.ID())
	}
}

func TestSession_Append(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	entries := []SessionTreeEntry{
		NewUserMessageEntry("Hello"),
		NewAssistantMessageEntry(core.AssistantMessage{
			Role:    core.MessageRoleAssistant,
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Hi there!"}},
		}),
	}

	if err := sess.Append(entries...); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	got := sess.Entries()
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
}

func TestSession_BuildContext(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	sess.Append(NewSystemPromptEntry("You are a helpful assistant."))
	sess.Append(NewUserMessageEntry("What's 2+2?"))
	sess.Append(NewAssistantMessageEntry(core.AssistantMessage{
		Role:    core.MessageRoleAssistant,
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "4"}},
	}))

	ctx := sess.BuildContext()

	if ctx.SystemPrompt != "You are a helpful assistant." {
		t.Errorf("expected system prompt, got %q", ctx.SystemPrompt)
	}
	if len(ctx.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(ctx.Messages))
	}
}

func TestSession_BuildContext_WithCompaction(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)

	sess.Append(NewUserMessageEntry("Old message 1"))
	sess.Append(NewAssistantMessageEntry(core.AssistantMessage{
		Role:    core.MessageRoleAssistant,
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Old response 1"}},
	}))
	sess.Append(NewCompactionEntry("Summary of old conversation"))
	sess.Append(NewUserMessageEntry("New message"))

	ctx := sess.BuildContext()

	// Should have: summary + new message = 2
	if len(ctx.Messages) != 2 {
		t.Errorf("expected 2 messages after compaction, got %d", len(ctx.Messages))
	}
}

func TestSession_Close(t *testing.T) {
	storage := NewMemoryStorage()
	sess, _ := NewSession(storage)
	if err := sess.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestAgentSession(t *testing.T) {
	storage := NewMemoryStorage()
	agentSess, err := NewAgentSession(storage)
	if err != nil {
		t.Fatalf("NewAgentSession failed: %v", err)
	}
	defer agentSess.Close()

	ch := agentSess.Subscribe(10)
	defer agentSess.Unsubscribe(ch)

	sess := agentSess.Session()
	sess.Append(NewUserMessageEntry("Test message"))

	entries := sess.Entries()
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestJSONLStorage(t *testing.T) {
	tmpFile := t.TempDir() + "/test.jsonl"

	storage, err := NewJSONLStorage(tmpFile)
	if err != nil {
		t.Fatalf("NewJSONLStorage failed: %v", err)
	}
	defer storage.Close()

	entries := []SessionTreeEntry{
		NewUserMessageEntry("Hello"),
		NewAssistantMessageEntry(core.AssistantMessage{
			Role:    core.MessageRoleAssistant,
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Hi!"}},
		}),
	}
	if err := storage.Append(entries); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	got, err := storage.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 entries, got %d", len(got))
	}
}

func TestHelperFunctions(t *testing.T) {
	entry := NewUserMessageEntry("test")
	if entry.Type != EntryUserMessage {
		t.Errorf("expected EntryUserMessage, got %q", entry.Type)
	}
}

func TestBuildSessionContext_ThinkingAndModel(t *testing.T) {
	entries := []SessionTreeEntry{
		{Type: EntryThinkingChange, ThinkingLevel: "high"},
		{Type: EntryModelChange, Provider: "anthropic", ModelID: "claude-4"},
		NewUserMessageEntry("hello"),
	}

	ctx := BuildSessionContext(entries)

	if ctx.ThinkingLevel != "high" {
		t.Errorf("expected thinking level 'high', got %q", ctx.ThinkingLevel)
	}
	if ctx.Model == nil {
		t.Fatal("expected model to be set")
	}
}
