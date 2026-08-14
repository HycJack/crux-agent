// Package plugin defines the agent contract interfaces for crux-kernel.
//
// All extended capabilities (session, context, memory, auto-learn, tools,
// approval, checkpoint, observe) are defined here as Go interfaces (Service
// Definitions). Implementations live in agent-engine/defaults and elsewhere; the
// kernel only carries the contracts, never concrete implementations.
//
// This package has a single dependency: github.com/hycjack/crux-ai/core.
// It does NOT depend on agent-engine, nor on any implementation package.
//
// Terminology note: this "plugin" package is distinct from the
// "crux-plugin" module (which is a JSON-RPC 2.0 subprocess framework).
// This package defines abstract contracts; crux-plugin provides one
// possible backend for ToolPlugin.
package plugin

import (
	"context"
	"time"

	core "github.com/hycjack/crux-ai/core"
)

// ─── SessionPlugin 会话持久化 ─────────────────────────────────────────────
// 参考: harness/session/session.go + session/types.go
// 默认实现: defaults/session.go
//
// 注意：SessionPlugin 是调用方契约，不直接被 engine 引用。
// engine 通过 AgentLoopConfig 的函数注入使用会话能力，调用方（如 TUI）
// 通过订阅 Agent 事件并调用 SessionPlugin 的 Append/BuildContext 来持久化。

type SessionPlugin interface {
	// ID returns the session identifier.
	ID() string

	// Append adds entries to the session and persists them.
	Append(entries ...SessionTreeEntry) error

	// Entries returns all session entries.
	Entries() []SessionTreeEntry

	// BuildContext rebuilds the conversation context from session entries.
	BuildContext() SessionContext

	// Close closes the session and its storage.
	Close() error
}

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
	EntrySystemPrompt   EntryType = "system_prompt"
	EntryLabel          EntryType = "label"
	EntryLeaf           EntryType = "leaf"
)

// SessionTreeEntry is a single entry in the session tree.
type SessionTreeEntry struct {
	ID        string    // 条目的唯一标识
	Type      EntryType // 条目类型
	ParentID  string    // 父条目 ID（支持树结构）
	Timestamp time.Time
	Message   core.Message   // only for EntryMessage
	Metadata  map[string]any // 扩展元数据，由调用方自由使用

	// --- EntrySessionInfo ---
	SessionID   string
	Description string

	// --- EntryCustomMessage ---
	CustomType string
	Content    any
	Display    bool
	Details    any

	// --- EntryBranchSummary / EntryLabel ---
	Summary      string
	FromID       string
	BranchRootID string

	// --- EntryCompaction ---
	CompactionSummary string
	TokensBefore      int
	FirstKeptEntryID  string
	ReplacedEntryIDs  []string // entries replaced by this compaction

	// --- EntryModelChange ---
	Provider string
	ModelID  string

	// --- EntryThinkingChange ---
	ThinkingLevel string
}

// SessionContext is the rebuilt context from session entries.
// It is produced by SessionPlugin implementations (e.g. agent-engine/defaults JSONLSession).
type SessionContext struct {
	Messages      []core.Message
	ThinkingLevel string
	Model         *SessionModel         // active model at context point
	SystemPrompt  string                // latest system prompt entry
}

// SessionModel represents the active model in a session.
type SessionModel struct {
	Provider string
	ModelID  string
}

// ─── ContextPlugin 上下文管理 ─────────────────────────────────────────────
// 参考: harness/context/pipeline.go + harness/context/context.go
// 默认实现: defaults/context.go

type ContextPlugin interface {
	AddMessage(msg core.Message) error
	GetMessages() []core.Message
	IsNearLimit(threshold float64) bool
	GetStats() ContextStats
	Compact(ctx context.Context) error
}

type ContextStats struct {
	TotalTokens     int
	MessageCount    int
	Compactions     int
	MaxTokens       int
	AvailableTokens int
	UsagePercent    float64
}

// ─── MemoryPlugin 长期记忆 ────────────────────────────────────────────────
// 参考: runtime/memory/memory.go
// 默认实现: defaults/memory.go

type MemoryPlugin interface {
	Get(key string) (string, bool)
	Set(key, value string)
	SetWithCategory(key, value, category string)
	Delete(key string)
	FormatForPrompt() string
	Hash() string
	Save() error
}

// ─── AutoLearnPlugin 自动学习 ─────────────────────────────────────────────
// 参考: runtime/autolearn/autolearn.go
// 默认实现: defaults/autolearn.go

type AutoLearnPlugin interface {
	ProcessUserInput(text string) int
	ProcessToolResult(text string) int
	MaybeExtract(ctx context.Context, messages []core.Message, extractor Extractor) bool
}

type Extractor interface {
	Extract(ctx context.Context, messages []core.Message) ([]Trigger, error)
}

type Trigger struct {
	Source  string
	Key     string
	Value   string
	Context string
}

// ─── ToolPlugin 工具插件 ──────────────────────────────────────────────────
// 参考: crux-plugin/protocol.go, crux-plugin/tooladapter.go
//
// crux-plugin 的 ToolAdapter 可以通过适配包装实现此接口。
// 详见 agent-engine-abstract.md 二-A节。

type ToolPlugin interface {
	Name() string
	Description() string
	Parameters() []byte
	Execute(ctx context.Context, toolCallID string, params []byte, onUpdate func([]byte)) (ToolResult, error)
}

type ToolResult struct {
	Content   []core.ContentBlock
	Details   []byte
	IsError   bool
	Terminate bool
}

// ─── ApprovalPlugin 审批门 ────────────────────────────────────────────────
// 参考: harness/approval/gate.go
// 默认实现: defaults/approval.go

type ApprovalResult int

const (
	ApprovalAllow ApprovalResult = iota // Proceed with execution
	ApprovalBlock                       // Block execution
	ApprovalAsk                         // Requires external callback
)

type ApprovalPlugin interface {
	// Evaluate checks whether a tool call should be allowed, blocked, or
	// requires user approval. Returns the decision, a reason string, and
	// any error.
	Evaluate(ctx context.Context, toolName string, toolID string, args []byte) (ApprovalResult, string, error)
}

// ─── CheckpointPlugin 快照/回滚 ──────────────────────────────────────────
// 参考: harness/checkpoint/checkpoint.go
// 默认实现: defaults/checkpoint.go

type CheckpointInfo struct {
	ID        string
	Label     string
	Timestamp time.Time
	MsgCount  int
}

type CheckpointPlugin interface {
	// Save creates a snapshot from the current messages. Returns the snapshot ID.
	Save(label string, messages []core.Message) (string, error)

	// Undo rolls back to the previous snapshot. Returns the restored messages.
	Undo() ([]core.Message, bool)

	// Redo moves forward to the next snapshot. Returns the restored messages.
	Redo() ([]core.Message, bool)

	// List returns all snapshots (for UI display).
	List() []CheckpointInfo
}

// ─── ObservePlugin 观测/日志 ──────────────────────────────────────────────
// 参考: harness/observe/observe.go
// 默认实现: defaults/observe.go

type ObservePlugin interface {
	Debug(msg string, fields map[string]any)
	Info(msg string, fields map[string]any)
	Warn(msg string, fields map[string]any)
	Error(msg string, fields map[string]any)
}
