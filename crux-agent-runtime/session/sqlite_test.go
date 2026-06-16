package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hycjack/crux-ai/core"
)

func TestSQLiteStorage(t *testing.T) {
	// Create temp database.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}
	defer storage.Close()

	// Write entries.
	entries := []SessionTreeEntry{
		NewUserMessageEntry("Hello"),
		NewAssistantMessageEntry(core.AssistantMessage{
			Role:    core.MessageRoleAssistant,
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Hi!"}},
		}),
		NewToolResultEntry("call-1", "get_weather", []core.ContentBlock{
			core.TextContent{Type: "text", Text: "Sunny, 25°C"},
		}, false),
	}
	if err := storage.Append(entries); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Read back.
	got, err := storage.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 entries, got %d", len(got))
	}

	// Verify entry types.
	if got[0].Type != EntryUserMessage {
		t.Errorf("expected EntryUserMessage, got %q", got[0].Type)
	}
	if got[1].Type != EntryAssistantMessage {
		t.Errorf("expected EntryAssistantMessage, got %q", got[1].Type)
	}
	if got[2].Type != EntryToolResult {
		t.Errorf("expected EntryToolResult, got %q", got[2].Type)
	}

	// Verify message data can be parsed.
	msgs := got[0].GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
}

func TestSQLiteStorage_MultipleAppend(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}
	defer storage.Close()

	// First append.
	if err := storage.Append([]SessionTreeEntry{
		NewUserMessageEntry("First message"),
	}); err != nil {
		t.Fatalf("First append failed: %v", err)
	}

	// Second append.
	if err := storage.Append([]SessionTreeEntry{
		NewUserMessageEntry("Second message"),
	}); err != nil {
		t.Fatalf("Second append failed: %v", err)
	}

	// Read all.
	got, err := storage.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 entries, got %d", len(got))
	}
}

func TestSQLiteStorage_WithSessionInfo(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}
	defer storage.Close()

	// Append with session ID.
	entry := SessionTreeEntry{
		Type:      EntrySessionInfo,
		SessionID: "my-session-123",
	}
	if err := storage.Append([]SessionTreeEntry{entry}); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Read back.
	got, err := storage.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].SessionID != "my-session-123" {
		t.Errorf("expected session ID 'my-session-123', got %q", got[0].SessionID)
	}
}

func TestSQLiteStorage_WithModelChange(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}
	defer storage.Close()

	entry := NewModelChangeEntry("openai", "gpt-4o")
	if err := storage.Append([]SessionTreeEntry{entry}); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	got, err := storage.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if got[0].Provider != "openai" || got[0].ModelID != "gpt-4o" {
		t.Errorf("unexpected provider/model: %s/%s", got[0].Provider, got[0].ModelID)
	}
}

func TestSQLiteStorage_WithCompaction(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}
	defer storage.Close()

	entry := NewCompactionEntry("Summary of the conversation")
	if err := storage.Append([]SessionTreeEntry{entry}); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	got, err := storage.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if got[0].CompactionSummary != "Summary of the conversation" {
		t.Errorf("expected summary, got %q", got[0].CompactionSummary)
	}
}

func TestSQLiteStorage_WithMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}
	defer storage.Close()

	entry := SessionTreeEntry{
		Type: EntryMetadata,
		Metadata: map[string]any{
			"key1": "value1",
			"key2": 42,
		},
	}
	if err := storage.Append([]SessionTreeEntry{entry}); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	got, err := storage.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if got[0].Metadata["key1"] != "value1" {
		t.Errorf("expected metadata key1=value1, got %v", got[0].Metadata["key1"])
	}
}

func TestSQLiteStorage_Close(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}

	if err := storage.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestSQLiteStorage_InvalidPath(t *testing.T) {
	// Try to open in a non-existent directory.
	_, err := NewSQLiteStorage("/nonexistent/path/test.db")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestSQLiteStorage_EmptyDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}
	defer storage.Close()

	got, err := storage.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

func TestSQLiteStorage_FileCreated(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}
	defer storage.Close()

	// Check file exists.
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("expected database file to be created")
	}
}

func TestSQLiteStorage_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	storage, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}
	defer storage.Close()

	// Build a realistic session.
	entries := []SessionTreeEntry{
		NewSystemPromptEntry("You are a helpful assistant."),
		NewModelChangeEntry("anthropic", "claude-4"),
		NewUserMessageEntry("What's the weather like?"),
		NewAssistantMessageEntry(core.AssistantMessage{
			Role: core.MessageRoleAssistant,
			Content: []core.ContentBlock{
				core.TextContent{Type: "text", Text: "I'll check the weather for you."},
			},
		}),
		NewToolResultEntry("call-1", "get_weather", []core.ContentBlock{
			core.TextContent{Type: "text", Text: "Sunny, 25°C"},
		}, false),
		NewAssistantMessageEntry(core.AssistantMessage{
			Role: core.MessageRoleAssistant,
			Content: []core.ContentBlock{
				core.TextContent{Type: "text", Text: "It's sunny and 25°C!"},
			},
		}),
	}

	if err := storage.Append(entries); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Read back and verify.
	got, err := storage.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if len(got) != 6 {
		t.Errorf("expected 6 entries, got %d", len(got))
	}

	// Verify we can build context from the entries.
	// Manually create a session and build context.
	sess, err := NewSession(storage)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	ctx := sess.BuildContext()
	if ctx.SystemPrompt != "You are a helpful assistant." {
		t.Errorf("expected system prompt, got %q", ctx.SystemPrompt)
	}
	if ctx.Model == nil {
		t.Fatal("expected model to be set")
	}
	if ctx.Model.Provider != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %q", ctx.Model.Provider)
	}
	// Messages: user + assistant + tool_result + assistant = 4
	if len(ctx.Messages) != 4 {
		t.Errorf("expected 4 messages, got %d", len(ctx.Messages))
	}
}
