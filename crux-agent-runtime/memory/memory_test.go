package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	mem, err := New(path)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if mem.Size() != 0 {
		t.Errorf("expected 0 entries, got %d", mem.Size())
	}
}

func TestSetAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	mem, _ := New(path)
	mem.Set("user.name", "小明")

	val, ok := mem.Get("user.name")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if val != "小明" {
		t.Errorf("expected '小明', got %q", val)
	}
}

func TestSetWithCategory(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	mem, _ := New(path)
	mem.SetWithCategory("user.name", "小明", "user")

	items := mem.ListByCategory("user")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Key != "user.name" || items[0].Entry.Value != "小明" {
		t.Errorf("unexpected item: %v", items[0])
	}
}

func TestDelete(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	mem, _ := New(path)
	mem.Set("key", "value")
	mem.Delete("key")

	if mem.Has("key") {
		t.Error("expected key to be deleted")
	}
}

func TestHas(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	mem, _ := New(path)
	if mem.Has("missing") {
		t.Error("expected false for missing key")
	}
	mem.Set("exists", "yes")
	if !mem.Has("exists") {
		t.Error("expected true for existing key")
	}
}

func TestKeys(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	mem, _ := New(path)
	mem.Set("b", "1")
	mem.Set("a", "2")
	mem.Set("c", "3")

	keys := mem.Keys()
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	if keys[0] != "a" || keys[1] != "b" || keys[2] != "c" {
		t.Errorf("keys not sorted: %v", keys)
	}
}

func TestSize(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	mem, _ := New(path)
	if mem.Size() != 0 {
		t.Errorf("expected 0, got %d", mem.Size())
	}
	mem.Set("a", "1")
	mem.Set("b", "2")
	if mem.Size() != 2 {
		t.Errorf("expected 2, got %d", mem.Size())
	}
}

func TestHash(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	mem, _ := New(path)
	hash1 := mem.Hash()

	mem.Set("key", "value")
	hash2 := mem.Hash()

	if hash1 == hash2 {
		t.Error("hash should change after Set")
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	// Create and save.
	mem1, _ := New(path)
	mem1.Set("user.name", "小明")
	mem1.Set("user.lang", "zh-CN")
	if err := mem1.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load in new instance.
	mem2, err := New(path)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	val, ok := mem2.Get("user.name")
	if !ok || val != "小明" {
		t.Errorf("expected user.name=小明, got %q (exists=%v)", val, ok)
	}
	val, ok = mem2.Get("user.lang")
	if !ok || val != "zh-CN" {
		t.Errorf("expected user.lang=zh-CN, got %q (exists=%v)", val, ok)
	}
}

func TestLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	// Write raw JSON.
	data := []byte(`{"key":{"value":"test","createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:00:00Z"}}`)
	os.WriteFile(path, data, 0644)

	mem, _ := New(path)
	val, ok := mem.Get("key")
	if !ok || val != "test" {
		t.Errorf("expected key=test, got %q (exists=%v)", val, ok)
	}
}

func TestFormatForPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	mem, _ := New(path)
	mem.SetWithCategory("user.name", "小明", "user")
	mem.SetWithCategory("user.lang", "zh-CN", "user")
	mem.SetWithCategory("task.current", "开发", "task")

	prompt := mem.FormatForPrompt()
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	// Should contain all entries.
	if !contains(prompt, "user.name: 小明") {
		t.Error("missing user.name")
	}
	if !contains(prompt, "task.current: 开发") {
		t.Error("missing task.current")
	}
}

func TestFormatForPrompt_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	mem, _ := New(path)
	prompt := mem.FormatForPrompt()
	if prompt != "" {
		t.Errorf("expected empty prompt, got %q", prompt)
	}
}

func TestAtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	mem, _ := New(path)
	mem.Set("key", "value1")
	mem.Save()

	// Check no temp files left.
	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		if e.Name() != "test.json" {
			t.Errorf("unexpected file: %s", e.Name())
		}
	}
}

func contains(s, sub string) bool {
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
