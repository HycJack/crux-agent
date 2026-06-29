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
	WorkingDir     string `json:"workingDir"`
	TTSEnabled     bool   `json:"ttsEnabled"`
	TTSVoice       string `json:"ttsVoice"`
	LastActiveConv string `json:"lastActiveConv,omitempty"`
	AutoLearn      bool   `json:"autoLearn,omitempty"`
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
	ID             string                   `json:"id"`
	Role           string                   `json:"role"`
	Content        string                   `json:"content"`
	Timestamp      string                   `json:"timestamp"`
	Thinking       string                   `json:"thinking,omitempty"`
	ToolCalls      []PersistedToolCall      `json:"toolCalls,omitempty"`
	ToolExecutions []PersistedToolExecution `json:"toolExecutions,omitempty"`
}

// PersistedConversation mirrors the frontend Conversation type.
type PersistedConversation struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Messages  []PersistedMessage `json:"messages"`
	Timestamp string             `json:"timestamp"`
}

// PersistedConversationMeta is the lightweight metadata stored in the
// conversation index. It contains only the fields needed by the sidebar
// (id, title, timestamp) but no messages.
type PersistedConversationMeta struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Timestamp string `json:"timestamp"`
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
	// Apply auto-learn setting
	if a.learner != nil || s.AutoLearn {
		a.SetAutoLearnEnabled(s.AutoLearn)
	}
	return saveJSON("settings.json", s)
}

// conversationDir returns the per-conversation file directory.
func conversationDir() (string, error) {
	base, err := appDataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create conversations dir: %w", err)
	}
	return dir, nil
}

// conversationPath returns the file path for a single conversation.
func conversationPath(id string) (string, error) {
	dir, err := conversationDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".json"), nil
}

// indexPath returns the path to the conversation index file.
func indexPath() (string, error) {
	dir, err := appDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "conversations.json"), nil
}

// LoadConversations reads all persisted conversations. In the current
// storage layout, this rebuilds the full list by reading the index and
// then loading each conversation's message file. Returns an empty slice
// on a clean install.
func (a *App) LoadConversations() ([]PersistedConversation, error) {
	// Step 1: read the index (metadata only)
	idxPath, err := indexPath()
	if err != nil {
		return nil, err
	}
	var index []PersistedConversationMeta
	b, errI := os.ReadFile(idxPath)
	if errors.Is(errI, fs.ErrNotExist) {
		// No index file — try legacy single-file conversations.json
		return loadLegacyConversations()
	}
	if errI != nil {
		return nil, errI
	}
	if err := json.Unmarshal(b, &index); err != nil {
		return nil, err
	}
	// Step 2: load each conversation's full data from per-conversation files
	return loadFromIndex(index), nil
}

// SaveConversations writes the conversation index + per-conversation
// files to disk. The index file contains only metadata; each conversation's
// messages go into separate files under conversations/.
func (a *App) SaveConversations(convs []PersistedConversation) error {
	if convs == nil {
		convs = []PersistedConversation{}
	}

	// Build and save the index (metadata only).
	index := make([]PersistedConversationMeta, len(convs))
	for i, c := range convs {
		index[i] = PersistedConversationMeta{
			ID:        c.ID,
			Title:     c.Title,
			Timestamp: c.Timestamp,
		}
	}
	if err := saveJSON("conversations.json", index); err != nil {
		return err
	}

	// Save each conversation's messages into its own file.
	for _, c := range convs {
		if err := saveConversationMessages(c); err != nil {
			return err
		}
	}
	return nil
}

// saveConversationMessages writes just the messages of a single
// conversation (plus its ID/title/timestamp as context) to its own file.
func saveConversationMessages(conv PersistedConversation) error {
	p, err := conversationPath(conv.ID)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(conv, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// loadLegacyConversations handles migration from the old single-file
// conversations.json format. If found, it migrates to the new format
// (index + per-conversation files) and returns the full data.
func loadLegacyConversations() ([]PersistedConversation, error) {
	dir, err := appDataDir()
	if err != nil {
		return nil, err
	}
	oldFile := filepath.Join(dir, "conversations.json")

	// Read the raw file to determine its format.
	raw, err := os.ReadFile(oldFile)
	if err != nil {
		// No file at all — clean install
		return nil, nil
	}

	// Try to parse as the new index format first.
	var metaIndex []PersistedConversationMeta
	if err := json.Unmarshal(raw, &metaIndex); err == nil && len(metaIndex) == 0 {
		// Valid empty index — no conversations, no need to migrate
		return nil, nil
	}

	// If it parsed as meta index but there are entries, check if per-conversation
	// files exist. If they do, this is already the new format — just load normally.
	if len(metaIndex) > 0 {
		firstPath, _ := conversationPath(metaIndex[0].ID)
		if firstPath != "" {
			if _, err := os.Stat(firstPath); err == nil {
				// Already migrated — load using the new approach
				return loadFromIndex(metaIndex), nil
			}
		}
	}

	// Not parsed as meta index or no per-conversation files: try old format.
	var convs []PersistedConversation
	if err := json.Unmarshal(raw, &convs); err != nil {
		// Also doesn't match old format — corrupt or unknown
		return nil, nil
	}
	if len(convs) == 0 {
		return convs, nil
	}

	// Backup old file before migration
	_ = os.WriteFile(oldFile+".bak.legacy", raw, 0o600)

	index := make([]PersistedConversationMeta, len(convs))
	for i, c := range convs {
		index[i] = PersistedConversationMeta{
			ID:        c.ID,
			Title:     c.Title,
			Timestamp: c.Timestamp,
		}
		_ = saveConversationMessages(c)
	}

	// Write the new-style index (overwrites the old monolithic file)
	if err := saveJSON("conversations.json", index); err != nil {
		return nil, err
	}

	return convs, nil
}

// loadFromIndex loads full conversations from an existing meta index.
func loadFromIndex(index []PersistedConversationMeta) []PersistedConversation {
	out := make([]PersistedConversation, 0, len(index))
	for _, meta := range index {
		p, errP := conversationPath(meta.ID)
		if errP != nil {
			continue
		}
		b, errR := os.ReadFile(p)
		if errR != nil {
			continue
		}
		conv := PersistedConversation{
			ID:        meta.ID,
			Title:     meta.Title,
			Timestamp: meta.Timestamp,
		}
		if err := json.Unmarshal(b, &conv); err != nil {
			continue
		}
		out = append(out, conv)
	}
	return out
}
