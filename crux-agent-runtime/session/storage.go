package session

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/hycjack/crux-ai/core"
)

// JSONLStorage is a JSON Lines file-based session storage.
// || JSONL 文件存储后端
type JSONLStorage struct {
	mu       sync.Mutex
	filePath string
	file     *os.File
}

// NewJSONLStorage creates a new JSONL storage backend.
func NewJSONLStorage(filePath string) (*JSONLStorage, error) {
	s := &JSONLStorage{filePath: filePath}

	// Open or create file.
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, &SessionError{Code: ErrStorage, Message: "failed to open file", Err: err}
	}
	s.file = f

	return s, nil
}

// ReadAll reads all entries from the JSONL file.
func (s *JSONLStorage) ReadAll() ([]SessionTreeEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Flush any pending writes.
	if err := s.file.Sync(); err != nil {
		return nil, &SessionError{Code: ErrStorage, Message: "failed to sync", Err: err}
	}

	// Open a new file handle for reading to avoid conflicts with append mode.
	f, err := os.Open(s.filePath)
	if err != nil {
		return nil, &SessionError{Code: ErrStorage, Message: "failed to open for reading", Err: err}
	}
	defer f.Close()

	var entries []SessionTreeEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry SessionTreeEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// Skip malformed lines.
			continue
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// Append adds entries to the JSONL file.
func (s *JSONLStorage) Append(entries []SessionTreeEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	encoder := json.NewEncoder(s.file)
	for _, entry := range entries {
		if entry.Timestamp.IsZero() {
			entry.Timestamp = time.Now()
		}
		if err := encoder.Encode(entry); err != nil {
			return &SessionError{Code: ErrStorage, Message: "failed to write entry", Err: err}
		}
	}

	return s.file.Sync()
}

// Close closes the file.
func (s *JSONLStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}

// MemoryStorage is an in-memory session storage for testing.
// || 内存存储后端（测试用）
type MemoryStorage struct {
	mu      sync.RWMutex
	entries []SessionTreeEntry
}

// NewMemoryStorage creates a new memory storage backend.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{}
}

// ReadAll returns all entries.
func (s *MemoryStorage) ReadAll() ([]SessionTreeEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]SessionTreeEntry, len(s.entries))
	copy(result, s.entries)
	return result, nil
}

// Append adds entries to memory.
func (s *MemoryStorage) Append(entries []SessionTreeEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range entries {
		if entry.Timestamp.IsZero() {
			entry.Timestamp = time.Now()
		}
		s.entries = append(s.entries, entry)
	}
	return nil
}

// Close is a no-op for memory storage.
func (s *MemoryStorage) Close() error { return nil }

// Helper functions for creating entries.

// NewUserMessageEntry creates a user message entry.
func NewUserMessageEntry(content string) SessionTreeEntry {
	entry := SessionTreeEntry{
		Type:      EntryUserMessage,
		Timestamp: time.Now(),
	}
	msg := core.UserMessage{
		Role:      core.MessageRoleUser,
		Content:   content,
		Timestamp: time.Now(),
	}
	entry.SetMessage(msg)
	return entry
}

// NewAssistantMessageEntry creates an assistant message entry.
func NewAssistantMessageEntry(msg core.AssistantMessage) SessionTreeEntry {
	entry := SessionTreeEntry{
		Type:      EntryAssistantMessage,
		Timestamp: time.Now(),
	}
	entry.SetMessage(msg)
	return entry
}

// NewToolResultEntry creates a tool result entry.
func NewToolResultEntry(toolCallID, toolName string, content []core.ContentBlock, isError bool) SessionTreeEntry {
	entry := SessionTreeEntry{
		Type:      EntryToolResult,
		Timestamp: time.Now(),
	}
	msg := core.ToolResultMessage{
		Role:       core.MessageRoleTool,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Content:    content,
		IsError:    isError,
	}
	entry.SetMessage(msg)
	return entry
}

// NewSystemPromptEntry creates a system prompt entry.
func NewSystemPromptEntry(prompt string) SessionTreeEntry {
	return SessionTreeEntry{
		Type:      EntrySystemPrompt,
		Timestamp: time.Now(),
		Metadata:  map[string]any{"prompt": prompt},
	}
}

// NewModelChangeEntry creates a model change entry.
func NewModelChangeEntry(provider, modelID string) SessionTreeEntry {
	return SessionTreeEntry{
		Type:      EntryModelChange,
		Timestamp: time.Now(),
		Provider:  provider,
		ModelID:   modelID,
	}
}

// NewCompactionEntry creates a compaction entry.
func NewCompactionEntry(summary string) SessionTreeEntry {
	return SessionTreeEntry{
		Type:              EntryCompaction,
		Timestamp:         time.Now(),
		CompactionSummary: summary,
	}
}
