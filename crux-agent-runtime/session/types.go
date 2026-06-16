// Package session provides persistent session management for agents.
//
// It combines:
//   - Persistent storage via SessionStorage interface
//   - Session tree entries for structured conversation history
//   - High-level AgentSession for runtime management
//   - Event subscription system
package session

import (
	"encoding/json"
	"time"

	"github.com/hycjack/crux-ai/core"
)

// EntryType represents the type of a session tree entry.
type EntryType string

const (
	EntryUserMessage     EntryType = "user_message"
	EntryAssistantMessage EntryType = "assistant_message"
	EntryToolResult      EntryType = "tool_result"
	EntrySystemPrompt    EntryType = "system_prompt"
	EntryModelChange     EntryType = "model_change"
	EntryCompaction      EntryType = "compaction"
	EntrySessionInfo     EntryType = "session_info"
	EntryThinkingChange  EntryType = "thinking_change"
	EntryMetadata        EntryType = "metadata"
)

// SessionTreeEntry is a single node in the session tree.
// || 会话树中的单个节点
type SessionTreeEntry struct {
	// Type identifies the entry type.
	Type EntryType `json:"type"`

	// Timestamp is when the entry was created.
	Timestamp time.Time `json:"timestamp"`

	// MessageData is the raw JSON for user/assistant/tool messages.
	// Use GetMessages() to get parsed messages.
	MessageData json.RawMessage `json:"message,omitempty"`

	// SessionID is set for EntrySessionInfo.
	SessionID string `json:"sessionId,omitempty"`

	// Model info for EntryModelChange.
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"modelId,omitempty"`

	// ThinkingLevel for EntryThinkingChange.
	ThinkingLevel string `json:"thinkingLevel,omitempty"`

	// CompactionSummary for EntryCompaction.
	CompactionSummary string `json:"compactionSummary,omitempty"`

	// Metadata for arbitrary key-value data.
	Metadata map[string]any `json:"metadata,omitempty"`

	// Content is raw content for custom entry types.
	Content json.RawMessage `json:"content,omitempty"`
}

// GetMessages parses MessageData into a slice of core.Message.
func (e *SessionTreeEntry) GetMessages() []core.Message {
	if len(e.MessageData) == 0 {
		return nil
	}

	// Probe the role to determine which concrete type to use.
	var probe struct {
		Role core.MessageRole `json:"role"`
	}
	if err := json.Unmarshal(e.MessageData, &probe); err != nil {
		return nil
	}

	switch probe.Role {
	case core.MessageRoleUser:
		var msg core.UserMessage
		if err := json.Unmarshal(e.MessageData, &msg); err != nil {
			return nil
		}
		return []core.Message{msg}
	case core.MessageRoleAssistant:
		var msg core.AssistantMessage
		if err := json.Unmarshal(e.MessageData, &msg); err != nil {
			return nil
		}
		return []core.Message{msg}
	case core.MessageRoleTool:
		var msg core.ToolResultMessage
		if err := json.Unmarshal(e.MessageData, &msg); err != nil {
			return nil
		}
		return []core.Message{msg}
	default:
		return nil
	}
}

// SetMessage sets a single message.
func (e *SessionTreeEntry) SetMessage(msg core.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	e.MessageData = data
	return nil
}

// SessionContext is rebuilt from session entries for LLM calls.
// || 从会话条目重建的 LLM 上下文
type SessionContext struct {
	// Messages is the conversation history.
	Messages []core.Message

	// SystemPrompt is the current system prompt.
	SystemPrompt string

	// Model is the current model configuration.
	Model *SessionModel

	// ThinkingLevel is the current thinking level.
	ThinkingLevel string
}

// SessionModel represents a model configuration.
type SessionModel struct {
	Provider string
	ModelID  string
}

// SessionStorage is the interface for persisting session entries.
// || 会话存储接口
type SessionStorage interface {
	// ReadAll reads all entries from storage.
	ReadAll() ([]SessionTreeEntry, error)

	// Append adds entries to storage.
	Append(entries []SessionTreeEntry) error

	// Close closes the storage backend.
	Close() error
}

// SessionError represents a session error.
type SessionError struct {
	Code    string
	Message string
	Err     error
}

func (e *SessionError) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Message + ": " + e.Err.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *SessionError) Unwrap() error { return e.Err }

// Error codes.
const (
	ErrStorage      = "STORAGE_ERROR"
	ErrSessionBusy  = "SESSION_BUSY"
	ErrSessionClose = "SESSION_CLOSED"
	ErrInvalidEntry = "INVALID_ENTRY"
)
