package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// PersistedSettings is the on-disk shape of the Settings panel.
// Mirrors the frontend Settings type 1:1; the Go side is the source of
// truth (the frontend reads it on mount and writes it on change).
type PersistedSettings struct {
	Provider       string `json:"provider"`
	APIKey         string `json:"apiKey"`
	BaseURL        string `json:"baseUrl"`
	Model          string `json:"model"`
	CustomModel    string `json:"customModel"`
	TTSEnabled     bool   `json:"ttsEnabled"`
	TTSVoice       string `json:"ttsVoice"`
	LastActiveConv string `json:"lastActiveConv,omitempty"`
}

// PersistedToolCall is the on-disk shape of the frontend ToolCall.
type PersistedToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// PersistedToolExecution is the on-disk shape of the frontend ToolExecution.
type PersistedToolExecution struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Result  string `json:"result,omitempty"`
	IsError bool   `json:"isError,omitempty"`
}

// PersistedMessage mirrors the frontend Message type.
type PersistedMessage struct {
	ID            string                  `json:"id"`
	Role          string                  `json:"role"`
	Content       string                  `json:"content"`
	Timestamp     string                  `json:"timestamp"`
	Thinking      string                  `json:"thinking,omitempty"`
	ToolCalls     []PersistedToolCall     `json:"toolCalls,omitempty"`
	ToolExecutions []PersistedToolExecution `json:"toolExecutions,omitempty"`
}

// PersistedConversation mirrors the frontend Conversation type.
type PersistedConversation struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Messages  []PersistedMessage `json:"messages"`
	Timestamp string             `json:"timestamp"`
}

// appDataDir returns the OS-conventional directory where Crux Agent
// stores its user data (settings, conversations, runtime state).
//
//	macOS:  ~/Library/Application Support/crux-agent
//	Linux:  ~/.config/crux-agent (or $XDG_CONFIG_HOME/crux-agent)
//	Win:    %APPDATA%/crux-agent
//
// The directory is created on first access.
func appDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	dir := filepath.Join(base, "crux-agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create app data dir: %w", err)
	}
	return dir, nil
}

// loadJSON reads a JSON file at <appDataDir>/name. A missing file is not
// an error — it returns (zero value, nil), so callers can default.
func loadJSON(name string, dst interface{}) error {
	dir, err := appDataDir()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// saveJSON atomically writes a JSON file at <appDataDir>/name. The
// write goes to <name>.tmp first, then is renamed — this avoids leaving
// a half-written file if the process is killed mid-write.
func saveJSON(name string, value interface{}) error {
	dir, err := appDataDir()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(dir, name)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// ---------------- App-bound methods (Wails) ----------------

// LoadSettings reads the persisted settings. Returns (nil, nil) on a
// clean install so the frontend can fall back to its defaults.
func (a *App) LoadSettings() (*PersistedSettings, error) {
	var s PersistedSettings
	if err := loadJSON("settings.json", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// SaveSettings writes the given settings to disk atomically.
func (a *App) SaveSettings(s PersistedSettings) error {
	return saveJSON("settings.json", s)
}

// LoadConversations reads the persisted conversation list. Returns
// (nil, nil) on a clean install so the frontend starts with an empty
// history rather than an error.
func (a *App) LoadConversations() ([]PersistedConversation, error) {
	var convs []PersistedConversation
	if err := loadJSON("conversations.json", &convs); err != nil {
		return nil, err
	}
	return convs, nil
}

// SaveConversations writes the conversation list to disk atomically.
func (a *App) SaveConversations(convs []PersistedConversation) error {
	if convs == nil {
		convs = []PersistedConversation{}
	}
	return saveJSON("conversations.json", convs)
}