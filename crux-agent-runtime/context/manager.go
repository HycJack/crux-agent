package context

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/hycjack/crux-ai/core"
	"crux-agent-runtime/session"
)

// Manager manages the context window for a session.
// It tracks token usage and triggers compaction when needed.
// || 上下文管理器：管理会话的上下文窗口
type Manager struct {
	mu     sync.RWMutex
	config ContextWindowConfig
	compactor Compactor
	counter   TokenCounter

	// Current state
	systemPrompt string
	tools        []core.Tool
	messages     []core.Message

	// Statistics
	totalTokens   int
	compactions   int
	lastCompacted int // message count after last compaction
}

// NewManager creates a new context manager.
func NewManager(config ContextWindowConfig) *Manager {
	return &Manager{
		config:    config,
		counter:   config.TokenCounter,
		compactor: NewSlideWindow(50), // Default compactor
	}
}

// SetCompactor sets the compaction strategy.
func (m *Manager) SetCompactor(compactor Compactor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.compactor = compactor
}

// SetSystemPrompt sets the system prompt.
func (m *Manager) SetSystemPrompt(prompt string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.systemPrompt = prompt
}

// SetTools sets the available tools.
func (m *Manager) SetTools(tools []core.Tool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tools = tools
}

// LoadFromSession loads messages from a session and rebuilds context.
func (m *Manager) LoadFromSession(sess *session.Session) {
	ctx := sess.BuildContext()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.systemPrompt = ctx.SystemPrompt
	m.messages = ctx.Messages

	// Recalculate tokens.
	counter := m.counter
	if counter == nil {
		counter = DefaultTokenCounter
	}
	m.totalTokens = counter(m.systemPrompt, m.messages, m.tools)
}

// AddMessage adds a message and checks if compaction is needed.
func (m *Manager) AddMessage(msg core.Message) error {
	m.mu.Lock()
	m.messages = append(m.messages, msg)

	// Update token count.
	counter := m.counter
	if counter == nil {
		counter = DefaultTokenCounter
	}
	m.totalTokens = counter(m.systemPrompt, m.messages, m.tools)
	m.mu.Unlock()

	// Check if compaction is needed.
	return m.CompactIfNeeded(context.Background())
}

// CompactIfNeeded compacts the context if it exceeds the window.
func (m *Manager) CompactIfNeeded(ctx context.Context) error {
	m.mu.RLock()
	needsCompaction := NeedsCompaction(m.counter, m.systemPrompt, m.messages, m.tools, m.config)
	m.mu.RUnlock()

	if !needsCompaction {
		return nil
	}

	return m.Compact(ctx)
}

// Compact forces a compaction.
func (m *Manager) Compact(ctx context.Context) error {
	m.mu.Lock()
	compactor := m.compactor
	messages := m.messages
	m.mu.Unlock()

	if compactor == nil {
		return nil
	}

	newMsgs, changed, err := compactor.Compact(ctx, messages)
	if err != nil {
		return fmt.Errorf("compaction failed: %w", err)
	}

	if changed {
		m.mu.Lock()
		m.messages = newMsgs
		m.compactions++
		m.lastCompacted = len(newMsgs)

		// Recalculate tokens.
		counter := m.counter
		if counter == nil {
			counter = DefaultTokenCounter
		}
		m.totalTokens = counter(m.systemPrompt, m.messages, m.tools)
		m.mu.Unlock()

		log.Printf("context: compacted %d -> %d messages, %d tokens",
			len(messages), len(newMsgs), m.totalTokens)
	}

	return nil
}

// GetMessages returns a copy of the current messages.
func (m *Manager) GetMessages() []core.Message {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]core.Message, len(m.messages))
	copy(result, m.messages)
	return result
}

// GetContext returns the current context for LLM calls.
func (m *Manager) GetContext() core.Context {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return core.Context{
		SystemPrompt: m.systemPrompt,
		Messages:     m.messages,
		Tools:        m.tools,
	}
}

// GetTokenCount returns the current token count.
func (m *Manager) GetTokenCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.totalTokens
}

// GetCompactionCount returns the number of compactions performed.
func (m *Manager) GetCompactionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.compactions
}

// IsNearLimit reports whether the context is near the token limit.
func (m *Manager) IsNearLimit(threshold float64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit := float64(m.config.MaxTokens - m.config.ReserveTokens)
	return float64(m.totalTokens) > limit*threshold
}

// Reset clears the context.
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = nil
	m.totalTokens = 0
	m.compactions = 0
}

// Stats returns context statistics.
type Stats struct {
	TotalTokens    int
	MessageCount   int
	Compactions    int
	MaxTokens      int
	AvailableTokens int
	UsagePercent   float64
}

// GetStats returns current statistics.
func (m *Manager) GetStats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	maxTokens := m.config.MaxTokens
	reserveTokens := m.config.ReserveTokens
	available := maxTokens - reserveTokens

	var usage float64
	if available > 0 {
		usage = float64(m.totalTokens) / float64(available) * 100
	}

	return Stats{
		TotalTokens:     m.totalTokens,
		MessageCount:    len(m.messages),
		Compactions:     m.compactions,
		MaxTokens:       maxTokens,
		AvailableTokens: available,
		UsagePercent:    usage,
	}
}
