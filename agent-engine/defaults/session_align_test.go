package defaults

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hycjack/crux-kernel/plugin"
	core "github.com/hycjack/crux-ai/core"
)

// TestSessionAlign_RoundTrip verifies the runtime-aligned session capabilities:
// model + system prompt entries survive a persist -> reopen -> BuildContext cycle.
func TestSessionAlign_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	// Write a session: model change, system prompt, then two messages.
	sess, err := NewJSONLSession(path)
	if err != nil {
		t.Fatalf("NewJSONLSession: %v", err)
	}
	_ = sess.Append(
		NewModelChangeEntry("", "openai", "gpt-4o"),
		NewSystemPromptEntry("", "You are a coding assistant."),
		NewMessageEntry("", core.UserMessage{Role: "user", Content: "hello"}),
		NewMessageEntry("", core.AssistantMessage{Role: "assistant", Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "hi there"}}}),
	)
	if err := sess.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen and rebuild context.
	sess2, err := NewJSONLSession(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer sess2.Close()

	ctx := sess2.BuildContext()
	if ctx.SystemPrompt != "You are a coding assistant." {
		t.Errorf("SystemPrompt = %q, want the persisted prompt", ctx.SystemPrompt)
	}
	if ctx.Model == nil || ctx.Model.Provider != "openai" || ctx.Model.ModelID != "gpt-4o" {
		t.Errorf("Model = %+v, want openai/gpt-4o", ctx.Model)
	}
	if len(ctx.Messages) != 2 {
		t.Fatalf("Messages = %d, want 2", len(ctx.Messages))
	}
	if _, ok := ctx.Messages[0].(core.UserMessage); !ok {
		t.Errorf("messages[0] type = %T, want UserMessage", ctx.Messages[0])
	}
	if _, ok := ctx.Messages[1].(core.AssistantMessage); !ok {
		t.Errorf("messages[1] type = %T, want AssistantMessage", ctx.Messages[1])
	}
}

// TestSessionAlign_CompactionReplay verifies that a compaction entry replaces
// the referenced message IDs with a summary during BuildContext replay.
func TestSessionAlign_CompactionReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	sess, err := NewJSONLSession(path)
	if err != nil {
		t.Fatalf("NewJSONLSession: %v", err)
	}

	m1 := NewMessageEntry("m1", core.UserMessage{Role: "user", Content: "first"})
	m2 := NewMessageEntry("m2", core.AssistantMessage{Role: "assistant", Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "second"}}})
	m3 := NewMessageEntry("m3", core.UserMessage{Role: "user", Content: "third"})
	_ = sess.Append(m1, m2, m3)

	// Compact m1,m2 into a summary.
	_ = sess.Append(NewCompactionEntry("c1", "summary text", []string{"m1", "m2"}, 100))

	ctx := sess.BuildContext()
	// m3 should remain, m1/m2 replaced with one summary message.
	if len(ctx.Messages) != 2 {
		t.Fatalf("Messages = %d, want 2 (summary + m3)", len(ctx.Messages))
	}
	found := false
	for _, m := range ctx.Messages {
		if um, ok := m.(core.UserMessage); ok && um.Content == "summary text" {
			found = true
		}
	}
	if !found {
		t.Errorf("compaction summary message not found in rebuilt context: %+v", ctx.Messages)
	}
	_ = sess.Close()
}

// TestSessionAlign_JSONShape verifies the persisted JSONL uses snake_case keys
// expected by the runtime-aligned session format and survives reparse.
func TestSessionAlign_JSONShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	sess, err := NewJSONLSession(path)
	if err != nil {
		t.Fatalf("NewJSONLSession: %v", err)
	}
	_ = sess.Append(NewSystemPromptEntry("", "prompt-here"))
	_ = sess.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var line map[string]json.RawMessage
	if err := json.Unmarshal(data, &line); err != nil {
		t.Fatalf("unmarshal line: %v", err)
	}
	var typ string
	if err := json.Unmarshal(line["type"], &typ); err != nil {
		t.Fatalf("type: %v", err)
	}
	if typ != string(plugin.EntrySystemPrompt) {
		t.Errorf("type = %q, want %q", typ, plugin.EntrySystemPrompt)
	}
	if _, ok := line["metadata"]; !ok {
		t.Errorf("metadata field missing for system prompt entry")
	}
}
