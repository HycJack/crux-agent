package main

import (
	"path/filepath"
	"testing"
)

// withTestDataDir redirects os.UserConfigDir() to a per-test temp dir
// so the tests don't try to write to the real ~/Library/Application
// Support (which the sandboxed CI environment forbids).
func withTestDataDir(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// macOS / Windows fall back to os.UserConfigDir() unconditionally;
	// HOME is the only cross-platform knob that affects the path on
	// Linux too, so we set both to be safe.
	t.Setenv("HOME", t.TempDir())
}

func TestAppDataDir_Creates(t *testing.T) {
	withTestDataDir(t)
	dir, err := appDataDir()
	if err != nil {
		t.Fatalf("appDataDir: %v", err)
	}
	if filepath.Base(dir) != "crux-agent" {
		t.Errorf("expected base name 'crux-agent', got %q", filepath.Base(dir))
	}
}

func TestLoadJSON_Missing(t *testing.T) {
	withTestDataDir(t)
	var s PersistedSettings
	if err := loadJSON("does-not-exist.json", &s); err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
}

func TestSaveAndLoadSettings(t *testing.T) {
	withTestDataDir(t)
	want := PersistedSettings{
		Provider:       "openai",
		APIKey:         "sk-test",
		BaseURL:        "https://api.openai.com/v1",
		Model:          "gpt-4o",
		CustomModel:    "",
		TTSEnabled:     true,
		TTSVoice:       "zh-CN",
		LastActiveConv: "abc-123",
	}
	if err := saveJSON("settings.json", want); err != nil {
		t.Fatalf("saveJSON: %v", err)
	}
	var got PersistedSettings
	if err := loadJSON("settings.json", &got); err != nil {
		t.Fatalf("loadJSON: %v", err)
	}
	if got != want {
		t.Errorf("roundtrip mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestSaveAndLoadConversations(t *testing.T) {
	withTestDataDir(t)
	convs := []PersistedConversation{
		{
			ID:    "conv-1",
			Title: "Hello",
			Messages: []PersistedMessage{
				{ID: "m1", Role: "user", Content: "hi", Timestamp: "10:00"},
				{ID: "m2", Role: "assistant", Content: "hello", Timestamp: "10:01"},
			},
			Timestamp: "2024-01-01",
		},
	}
	if err := saveJSON("conversations.json", convs); err != nil {
		t.Fatalf("saveJSON: %v", err)
	}
	var got []PersistedConversation
	if err := loadJSON("conversations.json", &got); err != nil {
		t.Fatalf("loadJSON: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(got))
	}
	if got[0].Title != "Hello" || len(got[0].Messages) != 2 {
		t.Errorf("roundtrip mismatch: %+v", got[0])
	}
}
