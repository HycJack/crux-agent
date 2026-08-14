package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SessionMeta holds the metadata for a single session.
type SessionMeta struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"createdAt"`
	LastActiveAt time.Time `json:"lastActiveAt"`
	Description  string    `json:"description,omitempty"`
	MessageCount int       `json:"messageCount"`
	TokenCount   int64     `json:"tokenCount,omitempty"`
}

// SessionManager manages multiple sessions and their metadata.
type SessionManager struct {
	sessionsDir string
	metaFile    string
}

// NewSessionManager creates a session manager in the given directory.
// It creates the directory if needed and loads existing metadata.
func NewSessionManager(sessionsDir string) (*SessionManager, error) {
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return nil, fmt.Errorf("session: create sessions dir: %w", err)
	}
	m := &SessionManager{
		sessionsDir: sessionsDir,
		metaFile:    filepath.Join(sessionsDir, "sessions.json"),
	}
	return m, nil
}

// NextSessionID returns a new unique session ID based on the timestamp.
func NextSessionID() string {
	return "session_" + time.Now().Format("20060102_150405_000")
}

// SessionPath returns the jsonl file path for the given session ID.
func (m *SessionManager) SessionPath(sessionID string) string {
	return filepath.Join(m.sessionsDir, sessionID+".jsonl")
}

// NewSession creates a new session with a unique ID, writes a SessionInfo entry,
// and records metadata.
func (m *SessionManager) NewSession(description string) (*Session, error) {
	id := NextSessionID()
	path := m.SessionPath(id)

	store, err := NewJSONLStorage(path)
	if err != nil {
		return nil, fmt.Errorf("session: create storage: %w", err)
	}

	// Write the SessionInfo entry as the first record.
	sessionInfo := SessionTreeEntry{
		ID:          GenerateID(),
		Type:        EntrySessionInfo,
		Timestamp:   time.Now(),
		SessionID:   id,
		Description: description,
	}
	if err := store.Append([]SessionTreeEntry{sessionInfo}); err != nil {
		store.Close()
		return nil, fmt.Errorf("session: write session info: %w", err)
	}

	sess, err := NewSession(store)
	if err != nil {
		return nil, err
	}

	// Record metadata.
	meta := SessionMeta{
		ID:           id,
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
		Description:  description,
	}
	if err := m.recordMeta(meta); err != nil {
		// Non-fatal: metadata recording is best-effort.
		fmt.Fprintf(os.Stderr, "session: warn: record meta: %v\n", err)
	}

	return sess, nil
}

// OpenSession opens an existing session by ID.
func (m *SessionManager) OpenSession(sessionID string) (*Session, error) {
	path := m.SessionPath(sessionID)
	store, err := NewJSONLStorage(path)
	if err != nil {
		return nil, fmt.Errorf("session: open %s: %w", sessionID, err)
	}
	return NewSession(store)
}

// ListSessions returns all session metadata, sorted by last active time (newest first).
func (m *SessionManager) ListSessions() ([]SessionMeta, error) {
	metas, err := m.loadMetas()
	if err != nil {
		return nil, err
	}
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].LastActiveAt.After(metas[j].LastActiveAt)
	})
	return metas, nil
}

// UpdateMeta updates or inserts a session's metadata.
func (m *SessionManager) UpdateMeta(id string, fn func(m *SessionMeta)) error {
	metas, err := m.loadMetas()
	if err != nil {
		return err
	}
	found := false
	for i := range metas {
		if metas[i].ID == id {
			fn(&metas[i])
			found = true
			break
		}
	}
	if !found {
		meta := SessionMeta{ID: id, CreatedAt: time.Now()}
		fn(&meta)
		metas = append(metas, meta)
	}
	return m.writeMetas(metas)
}

func (m *SessionManager) recordMeta(meta SessionMeta) error {
	metas, err := m.loadMetas()
	if err != nil {
		return err
	}
	// Replace existing entry with same ID, or append.
	found := false
	for i := range metas {
		if metas[i].ID == meta.ID {
			metas[i] = meta
			found = true
			break
		}
	}
	if !found {
		metas = append(metas, meta)
	}
	return m.writeMetas(metas)
}

func (m *SessionManager) loadMetas() ([]SessionMeta, error) {
	data, err := os.ReadFile(m.metaFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var metas []SessionMeta
	if err := json.Unmarshal(data, &metas); err != nil {
		return nil, err
	}
	return metas, nil
}

func (m *SessionManager) writeMetas(metas []SessionMeta) error {
	data, err := json.MarshalIndent(metas, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.metaFile, data, 0644)
}
