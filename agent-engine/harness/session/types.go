// Package session provides conversation session persistence and management.
package session

import (
	"fmt"
	"time"

	core "github.com/hycjack/crux-ai/core"
)

// --- Error types ---

// ErrorCode is a stable, backend-independent error code.
type ErrorCode string

const (
	ErrAborted       ErrorCode = "aborted"
	ErrNotFound      ErrorCode = "not_found"
	ErrPermission    ErrorCode = "permission_denied"
	ErrInvalid       ErrorCode = "invalid"
	ErrTimeout       ErrorCode = "timeout"
	ErrStorage       ErrorCode = "storage"
	ErrSummarization ErrorCode = "summarization_failed"
	ErrInvalidSession ErrorCode = "invalid_session"
	ErrUnknown       ErrorCode = "unknown"
)

// HarnessError is the base error type for harness operations.
type HarnessError struct {
	Code    ErrorCode
	Message string
	Path    string
	Err     error
}

func (e *HarnessError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Path, e.Message)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *HarnessError) Unwrap() error { return e.Err }

// --- Session Tree Entry ---

// EntryType identifies the kind of session tree entry.
type EntryType string

const (
	EntryMessage        EntryType = "message"
	EntryCustomMessage  EntryType = "custom_message"
	EntryBranchSummary  EntryType = "branch_summary"
	EntryCompaction     EntryType = "compaction"
	EntryModelChange    EntryType = "model_change"
	EntryThinkingChange EntryType = "thinking_level_change"
	EntrySessionInfo    EntryType = "session_info"
	EntryLabel          EntryType = "label"
)

// SessionTreeEntry is a single entry in the session tree.
type SessionTreeEntry struct {
	ID        string    `json:"id"`
	Type      EntryType `json:"type"`
	Timestamp time.Time `json:"timestamp"`

	// For EntryMessage
	Message core.Message `json:"message,omitempty"`

	// For EntryCustomMessage
	CustomType string `json:"customType,omitempty"`
	Content    any    `json:"content,omitempty"`
	Display    bool   `json:"display,omitempty"`
	Details    any    `json:"details,omitempty"`

	// For EntryBranchSummary
	Summary string `json:"summary,omitempty"`
	FromID  string `json:"fromId,omitempty"`

	// For EntryCompaction
	CompactionSummary string `json:"compactionSummary,omitempty"`
	TokensBefore      int    `json:"tokensBefore,omitempty"`
	FirstKeptEntryID  string `json:"firstKeptEntryId,omitempty"`

	// For EntryModelChange
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"modelId,omitempty"`

	// For EntryThinkingChange
	ThinkingLevel string `json:"thinkingLevel,omitempty"`

	// For EntrySessionInfo
	SessionID   string `json:"sessionId,omitempty"`
	Description string `json:"description,omitempty"`
}

// SessionContext is the rebuilt context from session entries.
type SessionContext struct {
	Messages      []core.Message
	ThinkingLevel string
	Model         *SessionModel
}

// SessionModel represents the active model in a session.
type SessionModel struct {
	Provider string
	ModelID  string
}

// SessionMetadata contains session-level metadata including token usage.
type SessionMetadata struct {
	SessionID       string    `json:"sessionId"`
	Description     string    `json:"description,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	LastActiveAt    time.Time `json:"lastActiveAt"`
	TotalInputTokens int64    `json:"totalInputTokens"`
	TotalOutputTokens int64   `json:"totalOutputTokens"`
	TotalTokens     int64    `json:"totalTokens"`
	MessageCount    int      `json:"messageCount"`
	CompactionCount int      `json:"compactionCount"`
}

// --- Session Storage interface ---

// SessionStorage defines the persistence interface for sessions.
type SessionStorage interface {
	Append(entries []SessionTreeEntry) error
	ReadAll() ([]SessionTreeEntry, error)
	Close() error
}

// GenerateID generates a unique entry ID.
func GenerateID() string {
	return time.Now().Format("20060102150405.000000000")
}
