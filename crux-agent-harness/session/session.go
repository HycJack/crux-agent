package session

import (
	"sync"

	core "crux-ai/core"
)

// Session manages a conversation session as a tree of entries.
type Session struct {
	mu      sync.RWMutex
	storage SessionStorage
	entries []SessionTreeEntry
	id      string
}

// NewSession creates a new session with the given storage backend.
func NewSession(storage SessionStorage) (*Session, error) {
	s := &Session{storage: storage}

	entries, err := storage.ReadAll()
	if err != nil {
		return nil, &HarnessError{Code: ErrStorage, Message: "failed to load session", Err: err}
	}
	s.entries = entries

	for _, e := range entries {
		if e.Type == EntrySessionInfo && e.SessionID != "" {
			s.id = e.SessionID
			break
		}
	}

	return s, nil
}

// ID returns the session ID.
func (s *Session) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// Append adds entries to the session and persists them.
func (s *Session) Append(entries ...SessionTreeEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.storage != nil {
		if err := s.storage.Append(entries); err != nil {
			return &HarnessError{Code: ErrStorage, Message: "failed to append entries", Err: err}
		}
	}
	for _, e := range entries {
		if e.Type == EntrySessionInfo && e.SessionID != "" {
			s.id = e.SessionID
		}
	}
	s.entries = append(s.entries, entries...)
	return nil
}

// Entries returns a copy of all entries.
func (s *Session) Entries() []SessionTreeEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]SessionTreeEntry, len(s.entries))
	copy(result, s.entries)
	return result
}

// BuildContext rebuilds the conversation context from session entries.
func (s *Session) BuildContext() SessionContext {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return BuildSessionContext(s.entries)
}

// Close closes the session and its storage.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storage != nil {
		return s.storage.Close()
	}
	return nil
}

// BuildSessionContext rebuilds context from a list of entries.
func BuildSessionContext(entries []SessionTreeEntry) SessionContext {
	var thinkingLevel string
	var model *SessionModel
	compactionIdx := -1

	for i, entry := range entries {
		switch entry.Type {
		case EntryThinkingChange:
			thinkingLevel = entry.ThinkingLevel
		case EntryModelChange:
			model = &SessionModel{Provider: entry.Provider, ModelID: entry.ModelID}
		case EntryCompaction:
			compactionIdx = i
		}
	}

	var messages []core.Message

	if compactionIdx >= 0 {
		compaction := entries[compactionIdx]
		messages = append(messages, core.UserMessage{
			Role:    "user",
			Content: compactionPrefix + compaction.CompactionSummary + compactionSuffix,
		})
		for i := compactionIdx + 1; i < len(entries); i++ {
			if msg := entryToMessage(entries[i]); msg != nil {
				messages = append(messages, msg)
			}
		}
	} else {
		for _, entry := range entries {
			if msg := entryToMessage(entry); msg != nil {
				messages = append(messages, msg)
			}
		}
	}

	return SessionContext{
		Messages:      messages,
		ThinkingLevel: thinkingLevel,
		Model:         model,
	}
}

func entryToMessage(entry SessionTreeEntry) core.Message {
	switch entry.Type {
	case EntryMessage:
		// Filter out tool result messages to avoid orphaned tool call references
		if _, ok := entry.Message.(core.ToolResultMessage); ok {
			return nil
		}
		return entry.Message
	case EntryCustomMessage:
		return core.UserMessage{
			Role:    "user",
			Content: "[" + entry.CustomType + "] " + stringifyAny(entry.Content),
		}
	case EntryBranchSummary:
		return core.UserMessage{
			Role:    "user",
			Content: branchPrefix + entry.Summary + branchSuffix,
		}
	default:
		return nil
	}
}

func stringifyAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

const (
	compactionPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	compactionSuffix = "\n</summary>"
	branchPrefix     = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
	branchSuffix     = "\n</summary>"
)

// --- In-Memory Storage ---

// MemoryStorage is an in-memory session storage backend.
type MemoryStorage struct {
	mu      sync.RWMutex
	entries []SessionTreeEntry
}

// NewMemoryStorage creates a new in-memory storage.
func NewMemoryStorage() *MemoryStorage { return &MemoryStorage{} }

func (m *MemoryStorage) Append(entries []SessionTreeEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entries...)
	return nil
}

func (m *MemoryStorage) ReadAll() ([]SessionTreeEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]SessionTreeEntry, len(m.entries))
	copy(result, m.entries)
	return result, nil
}

func (m *MemoryStorage) Close() error { return nil }
