// Package checkpoint provides message snapshots and rollback.
package checkpoint

import (
	"sync"
	"time"

	core "crux-ai/core"
)

// Snapshot is a point-in-time capture of the message history.
type Snapshot struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Label     string         `json:"label,omitempty"`
	Messages  []core.Message `json:"messages"`
}

// Store manages snapshots with undo/redo support.
type Store struct {
	mu        sync.RWMutex
	snapshots []Snapshot
	index     int // current position (for undo/redo)
}

// New creates a new checkpoint store.
func New() *Store {
	return &Store{index: -1}
}

// Save creates a new snapshot from the current messages.
func (s *Store) Save(label string, messages []core.Message) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := Snapshot{
		ID:        genID(),
		Timestamp: time.Now(),
		Label:     label,
		Messages:  copyMessages(messages),
	}

	// Trim any redo history beyond current position
	if s.index < len(s.snapshots)-1 {
		s.snapshots = s.snapshots[:s.index+1]
	}

	s.snapshots = append(s.snapshots, snapshot)
	s.index = len(s.snapshots) - 1

	return snapshot
}

// Current returns the current snapshot, or nil if none exists.
func (s *Store) Current() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index < 0 || s.index >= len(s.snapshots) {
		return nil
	}
	snap := s.snapshots[s.index]
	return &snap
}

// Undo rolls back to the previous snapshot. Returns the restored messages.
func (s *Store) Undo() ([]core.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.index <= 0 {
		return nil, false
	}
	s.index--
	return copyMessages(s.snapshots[s.index].Messages), true
}

// Redo moves forward to the next snapshot. Returns the restored messages.
func (s *Store) Redo() ([]core.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.index >= len(s.snapshots)-1 {
		return nil, false
	}
	s.index++
	return copyMessages(s.snapshots[s.index].Messages), true
}

// Restore restores messages from a specific snapshot ID.
func (s *Store) Restore(id string) ([]core.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, snap := range s.snapshots {
		if snap.ID == id {
			s.index = i
			return copyMessages(snap.Messages), true
		}
	}
	return nil, false
}

// List returns all snapshots (for UI display).
func (s *Store) List() []Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Snapshot, len(s.snapshots))
	copy(result, s.snapshots)
	return result
}

func copyMessages(msgs []core.Message) []core.Message {
	result := make([]core.Message, len(msgs))
	copy(result, msgs)
	return result
}

func genID() string {
	return time.Now().Format("20060102150405.000000000")
}
