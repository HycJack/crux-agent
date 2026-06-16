package session

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hycjack/crux-ai/core"
)

// Session manages a conversation session as a tree of entries.
// || 会话管理器
type Session struct {
	mu            sync.RWMutex
	storage       SessionStorage
	entries       []SessionTreeEntry
	id            string
	branches      map[string]*Branch
	currentBranch string
}

// NewSession creates a new session with the given storage backend.
func NewSession(storage SessionStorage) (*Session, error) {
	s := &Session{storage: storage}

	entries, err := storage.ReadAll()
	if err != nil {
		return nil, &SessionError{Code: ErrStorage, Message: "failed to load session", Err: err}
	}
	s.entries = entries

	// Find session ID from entries.
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

// SetID sets the session ID and persists it.
func (s *Session) SetID(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = id

	entry := SessionTreeEntry{
		Type:      EntrySessionInfo,
		SessionID: id,
	}
	if s.storage != nil {
		if err := s.storage.Append([]SessionTreeEntry{entry}); err != nil {
			return err
		}
	}
	s.entries = append(s.entries, entry)
	return nil
}

// Append adds entries to the session and persists them.
func (s *Session) Append(entries ...SessionTreeEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.storage != nil {
		if err := s.storage.Append(entries); err != nil {
			return &SessionError{Code: ErrStorage, Message: "failed to append entries", Err: err}
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

// BuildSessionContext rebuilds context from a list of entries.
func BuildSessionContext(entries []SessionTreeEntry) SessionContext {
	var thinkingLevel string
	var model *SessionModel
	var systemPrompt string
	compactionIdx := -1

	for i, entry := range entries {
		switch entry.Type {
		case EntryThinkingChange:
			thinkingLevel = entry.ThinkingLevel
		case EntryModelChange:
			model = &SessionModel{Provider: entry.Provider, ModelID: entry.ModelID}
		case EntryCompaction:
			compactionIdx = i
		case EntrySystemPrompt:
			if prompt, ok := entry.Metadata["prompt"].(string); ok {
				systemPrompt = prompt
			}
		}
	}

	var messages []core.Message

	if compactionIdx >= 0 {
		// After compaction, only include entries after the compaction point.
		// Plus a summary message.
		compaction := entries[compactionIdx]
		entries = entries[compactionIdx+1:]

		if compaction.CompactionSummary != "" {
			messages = append(messages, core.UserMessage{
				Role:    core.MessageRoleUser,
				Content: compaction.CompactionSummary,
			})
		}
	}

	for _, entry := range entries {
		switch entry.Type {
		case EntryUserMessage, EntryAssistantMessage, EntryToolResult:
			messages = append(messages, entry.GetMessages()...)
		}
	}

	return SessionContext{
		Messages:      messages,
		SystemPrompt:  systemPrompt,
		Model:         model,
		ThinkingLevel: thinkingLevel,
	}
}

// Close closes the session and its storage.
func (s *Session) Close() error {
	if s.storage != nil {
		return s.storage.Close()
	}
	return nil
}

// AgentSession is a high-level wrapper around a session with event
// subscription and run management.
// || Agent 会话包装器
type AgentSession struct {
	mu        sync.Mutex
	session   *Session
	listeners []chan AgentEvent
	closed    atomic.Bool
	lastErr   error
}

// NewAgentSession creates a new AgentSession.
func NewAgentSession(storage SessionStorage) (*AgentSession, error) {
	sess, err := NewSession(storage)
	if err != nil {
		return nil, err
	}
	return &AgentSession{session: sess}, nil
}

// Session returns the underlying Session.
func (a *AgentSession) Session() *Session {
	return a.session
}

// Subscribe registers a new event listener. The returned channel
// receives ALL events from this point forward.
func (a *AgentSession) Subscribe(buffer int) chan AgentEvent {
	if buffer <= 0 {
		buffer = 32
	}
	ch := make(chan AgentEvent, buffer)
	a.mu.Lock()
	a.listeners = append(a.listeners, ch)
	a.mu.Unlock()
	return ch
}

// Unsubscribe removes a previously-registered listener and closes its channel.
func (a *AgentSession) Unsubscribe(ch chan AgentEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, l := range a.listeners {
		if l == ch {
			a.listeners = append(a.listeners[:i], a.listeners[i+1:]...)
			close(ch)
			return
		}
	}
}

// fanout broadcasts an event to all listeners.
func (a *AgentSession) fanout(event AgentEvent) {
	a.mu.Lock()
	listeners := append([]chan AgentEvent{}, a.listeners...)
	a.mu.Unlock()
	for _, ch := range listeners {
		select {
		case ch <- event:
		default:
			// Drop if listener is slow.
		}
	}
}

// IsRunning reports whether a run is currently in flight.
func (a *AgentSession) IsRunning() bool {
	// This will be connected to the agent loop.
	return false
}

// LastError returns the error from the most recent run.
func (a *AgentSession) LastError() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastErr
}

// Close releases all listeners and closes the session.
func (a *AgentSession) Close() error {
	if !a.closed.CompareAndSwap(false, true) {
		return nil
	}
	a.mu.Lock()
	for _, ch := range a.listeners {
		close(ch)
	}
	a.listeners = nil
	a.mu.Unlock()
	return a.session.Close()
}

// AgentEvent is the interface for agent session events.
type AgentEvent interface {
	agentEventTag()
}

// EventRunStart signals the start of a run.
type EventRunStart struct{}

func (EventRunStart) agentEventTag() {}

// EventRunEnd signals the end of a run.
type EventRunEnd struct {
	Messages []core.Message
	Error    error
}

func (EventRunEnd) agentEventTag() {}

// EventMessageUpdate signals a message update.
type EventMessageUpdate struct {
	Message core.Message
}

func (EventMessageUpdate) agentEventTag() {}

// EventToolExecution signals a tool execution event.
type EventToolExecution struct {
	ToolName string
	Status   string // "start", "complete", "error"
}

func (EventToolExecution) agentEventTag() {}

// Branch support methods for Session

// Fork creates a new branch from the current position.
// || 从当前位置创建分支
func (s *Session) Fork(ctx context.Context, name string, config BranchConfig) (*Branch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check branch limit
	if config.MaxBranches > 0 {
		branches := s.listBranchesUnsafe()
		if len(branches) >= config.MaxBranches {
			return nil, fmt.Errorf("maximum number of branches (%d) reached", config.MaxBranches)
		}
	}

	// Generate branch ID
	branchID := fmt.Sprintf("branch-%d", time.Now().UnixNano())

	// Get messages up to current point
	messages := make([]SessionTreeEntry, len(s.entries))
	copy(messages, s.entries)

	// Generate summary
	var summary string
	if config.AutoSummary {
		if config.SummaryFunc != nil {
			var err error
			summary, err = config.SummaryFunc(ctx, name, messages)
			if err != nil {
				// Fallback to truncation
				summary = TruncateSummary(messages, 5)
			}
		} else {
			summary = TruncateSummary(messages, 5)
		}
	}

	// Create branch
	branch := &Branch{
		ID:        branchID,
		Name:      name,
		SessionID: s.id,
		Summary:   summary,
		Messages:  messages,
		CreatedAt: time.Now(),
	}

	// Store branch
	if s.branches == nil {
		s.branches = make(map[string]*Branch)
	}
	s.branches[branchID] = branch

	// Append summary to the branch messages
	if summary != "" {
		branch.Messages = append(branch.Messages, SessionTreeEntry{
			Type:      EntrySystemPrompt,
			Timestamp: time.Now(),
			Metadata: map[string]any{
				"prompt": "## Branch summary\n\n" + summary,
				"type":   "branch_summary",
			},
		})
	}

	return branch, nil
}

// SwitchTo switches to a different branch.
// || 切换到指定分支
func (s *Session) SwitchTo(branchID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	branch, ok := s.branches[branchID]
	if !ok {
		return fmt.Errorf("branch %q not found", branchID)
	}

	// Save current state to current branch
	if s.currentBranch != "" {
		if current, ok := s.branches[s.currentBranch]; ok {
			current.Messages = make([]SessionTreeEntry, len(s.entries))
			copy(current.Messages, s.entries)
		}
	}

	// Switch to new branch
	s.currentBranch = branchID
	s.entries = make([]SessionTreeEntry, len(branch.Messages))
	copy(s.entries, branch.Messages)

	return nil
}

// Branches returns all branches.
// || 返回所有分支
func (s *Session) Branches() []*Branch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listBranchesUnsafe()
}

func (s *Session) listBranchesUnsafe() []*Branch {
	branches := make([]*Branch, 0, len(s.branches))
	for _, b := range s.branches {
		branches = append(branches, b)
	}
	return branches
}

// CurrentBranch returns the current active branch.
// || 返回当前分支
func (s *Session) CurrentBranch() *Branch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.currentBranch == "" {
		return nil
	}
	return s.branches[s.currentBranch]
}

// DeleteBranch removes a branch. Cannot delete the current branch.
// || 删除分支（不能删除当前分支）
func (s *Session) DeleteBranch(branchID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if branchID == s.currentBranch {
		return fmt.Errorf("cannot delete the current branch")
	}

	if _, ok := s.branches[branchID]; !ok {
		return fmt.Errorf("branch %q not found", branchID)
	}

	delete(s.branches, branchID)
	return nil
}

// AppendToCurrentBranch appends entries to the current branch.
// || 追加到当前分支
func (s *Session) AppendToCurrentBranch(entries ...SessionTreeEntry) error {
	return s.Append(entries...)
}
