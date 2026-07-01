package turn

import (
	"context"
	"sort"
	"sync"
)

// MemoryStore is an in-memory Store implementation.
// Use for tests and single-process deployments.
type MemoryStore struct {
	mu    sync.RWMutex
	turns map[string]*Turn
}

// NewMemoryStore creates a new memory-backed store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{turns: make(map[string]*Turn)}
}

// Save persists a turn (deep-copies slices to avoid external mutation).
func (s *MemoryStore) Save(ctx context.Context, turn *Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *turn
	if turn.Messages != nil {
		cp.Messages = make([]TurnMsg, len(turn.Messages))
		copy(cp.Messages, turn.Messages)
	}
	if turn.Pending != nil {
		cp.Pending = make([]TurnCall, len(turn.Pending))
		copy(cp.Pending, turn.Pending)
	}
	if turn.Metadata != nil {
		cp.Metadata = make(map[string]string, len(turn.Metadata))
		for k, v := range turn.Metadata {
			cp.Metadata[k] = v
		}
	}
	s.turns[turn.ID] = &cp
	return nil
}

// Load retrieves a turn by ID.
func (s *MemoryStore) Load(ctx context.Context, id string) (*Turn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.turns[id]
	if !ok {
		return nil, ErrTurnNotFound
	}
	cp := *t
	return &cp, nil
}

// List returns all turns for a session, ordered by created_at ASC.
func (s *MemoryStore) List(ctx context.Context, sessionID string) ([]*Turn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Turn
	for _, t := range s.turns {
		if t.SessionID == sessionID {
			cp := *t
			out = append(out, &cp)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// ErrTurnNotFound is returned when a turn ID doesn't exist.
var ErrTurnNotFound = errNotFound("turn not found")

type errNotFound string

func (e errNotFound) Error() string { return string(e) }