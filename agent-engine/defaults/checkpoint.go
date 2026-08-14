package defaults

import (
	"fmt"
	"sync"
	"time"

	"github.com/hycjack/crux-kernel/plugin"
	core "github.com/hycjack/crux-ai/core"
)

// CheckpointStore manages message snapshots with undo/redo support.
type CheckpointStore struct {
	mu        sync.RWMutex
	snapshots []plugin.CheckpointInfo
	msgs      [][]core.Message // parallel to snapshots
	index     int              // current position for undo/redo
	counter   int
}

// NewCheckpointStore creates a new checkpoint store.
func NewCheckpointStore() *CheckpointStore {
	return &CheckpointStore{index: -1}
}

// Save creates a snapshot from the current messages.
func (s *CheckpointStore) Save(label string, messages []core.Message) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.counter++
	id := fmt.Sprintf("snap-%d", s.counter)

	// Trim any redo history beyond current position
	if s.index < len(s.snapshots)-1 {
		s.snapshots = s.snapshots[:s.index+1]
		s.msgs = s.msgs[:s.index+1]
	}

	info := plugin.CheckpointInfo{
		ID:        id,
		Label:     label,
		Timestamp: time.Now(),
		MsgCount:  len(messages),
	}
	s.snapshots = append(s.snapshots, info)
	s.msgs = append(s.msgs, copyMessages(messages))
	s.index = len(s.snapshots) - 1

	return id, nil
}

// Undo rolls back to the previous snapshot.
func (s *CheckpointStore) Undo() ([]core.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.index <= 0 {
		return nil, false
	}
	s.index--
	return copyMessages(s.msgs[s.index]), true
}

// Redo moves forward to the next snapshot.
func (s *CheckpointStore) Redo() ([]core.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.index >= len(s.snapshots)-1 {
		return nil, false
	}
	s.index++
	return copyMessages(s.msgs[s.index]), true
}

// List returns all snapshots for UI display.
func (s *CheckpointStore) List() []plugin.CheckpointInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]plugin.CheckpointInfo, len(s.snapshots))
	copy(result, s.snapshots)
	return result
}

// Current returns the index pointing to the current snapshot position.
func (s *CheckpointStore) Current() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.index
}

func copyMessages(msgs []core.Message) []core.Message {
	if msgs == nil {
		return nil
	}
	out := make([]core.Message, len(msgs))
	copy(out, msgs)
	return out
}

// compile-time assertion
var _ plugin.CheckpointPlugin = (*CheckpointStore)(nil)
