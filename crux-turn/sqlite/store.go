package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hycjack/crux-turn"
)

// Store is a SQLite-backed turn.Store implementation.
type Store struct {
	db *sql.DB
}

// NewStore opens (or creates) a SQLite-backed turn store at the given path.
// The schema is migrated automatically.
func NewStore(path string) (*Store, error) {
	db, err := Open(path)
	if err != nil {
		return nil, err
	}
	if err := Migrate(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// NewStoreFromDB wraps an existing *sql.DB. Caller owns the DB lifecycle.
func NewStoreFromDB(db *sql.DB) *Store {
	return &Store{db: db}
}

// Close releases the database connection.
func (s *Store) Close() error { return s.db.Close() }

// DB returns the underlying *sql.DB. Used by sibling subpackages (ApprovalStore)
// to share the same connection. Caller should NOT close it directly.
func (s *Store) DB() *sql.DB { return s.db }

// Save persists a turn (upsert by id).
func (s *Store) Save(ctx context.Context, turn *turn.Turn) error {
	if turn.UpdatedAt.IsZero() {
		turn.UpdatedAt = time.Now()
	}
	messagesJSON, err := json.Marshal(turn.Messages)
	if err != nil {
		return fmt.Errorf("marshal messages: %w", err)
	}
	pendingJSON, err := json.Marshal(turn.Pending)
	if err != nil {
		return fmt.Errorf("marshal pending: %w", err)
	}
	metadataJSON, err := json.Marshal(turn.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO turns (id, session_id, user_id, agent_id, state, round, messages, pending, metadata, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			session_id = excluded.session_id,
			user_id = excluded.user_id,
			agent_id = excluded.agent_id,
			state = excluded.state,
			round = excluded.round,
			messages = excluded.messages,
			pending = excluded.pending,
			metadata = excluded.metadata,
			error = excluded.error,
			updated_at = excluded.updated_at
	`,
		turn.ID, turn.SessionID, turn.UserID, turn.AgentID, turn.State, turn.Round,
		string(messagesJSON), string(pendingJSON), string(metadataJSON), turn.Error,
		turn.CreatedAt.Unix(), turn.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("insert turn: %w", err)
	}
	return nil
}

// Load retrieves a turn by ID.
func (s *Store) Load(ctx context.Context, id string) (*turn.Turn, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, user_id, agent_id, state, round, messages, pending, metadata, error, created_at, updated_at
		FROM turns WHERE id = ?
	`, id)
	return scanTurn(row)
}

// List returns all turns for a session, ordered by created_at ASC.
func (s *Store) List(ctx context.Context, sessionID string) ([]*turn.Turn, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, user_id, agent_id, state, round, messages, pending, metadata, error, created_at, updated_at
		FROM turns WHERE session_id = ? ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query turns: %w", err)
	}
	defer rows.Close()

	var out []*turn.Turn
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanTurn(row scanner) (*turn.Turn, error) {
	var (
		t            turn.Turn
		messagesJSON sql.NullString
		pendingJSON  sql.NullString
		metadataJSON sql.NullString
		errStr       sql.NullString
		createdAt    int64
		updatedAt    int64
	)
	if err := row.Scan(
		&t.ID, &t.SessionID, &t.UserID, &t.AgentID, &t.State, &t.Round,
		&messagesJSON, &pendingJSON, &metadataJSON, &errStr,
		&createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, turn.ErrTurnNotFound
		}
		return nil, fmt.Errorf("scan turn: %w", err)
	}
	if messagesJSON.Valid && messagesJSON.String != "" {
		if err := json.Unmarshal([]byte(messagesJSON.String), &t.Messages); err != nil {
			return nil, fmt.Errorf("unmarshal messages: %w", err)
		}
	}
	if pendingJSON.Valid && pendingJSON.String != "" {
		if err := json.Unmarshal([]byte(pendingJSON.String), &t.Pending); err != nil {
			return nil, fmt.Errorf("unmarshal pending: %w", err)
		}
	}
	if metadataJSON.Valid && metadataJSON.String != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &t.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}
	if errStr.Valid {
		t.Error = errStr.String
	}
	if createdAt > 0 {
		t.CreatedAt = time.Unix(createdAt, 0)
	}
	if updatedAt > 0 {
		t.UpdatedAt = time.Unix(updatedAt, 0)
	}
	return &t, nil
}

// Compile-time check
var _ turn.Store = (*Store)(nil)

// InProcessApproval is a simple in-memory ApprovalService for tests.
// The ApprovalStore SQLite impl lives in approval.go.
type InProcessApproval struct {
	mu    sync.RWMutex
	items map[string]*turn.ApprovalRequest[turn.TurnCall]
}

// NewInProcessApproval creates a new in-memory approval service.
func NewInProcessApproval() *InProcessApproval {
	return &InProcessApproval{items: make(map[string]*turn.ApprovalRequest[turn.TurnCall])}
}

// Create persists a new approval request and returns its ID.
func (a *InProcessApproval) Create(ctx context.Context, turnID, sessionID, userID string, call turn.TurnCall, rawArgs string) (string, error) {
	id := fmt.Sprintf("appr-%d", time.Now().UnixNano())
	a.mu.Lock()
	defer a.mu.Unlock()
	a.items[id] = &turn.ApprovalRequest[turn.TurnCall]{
		ID:        id,
		TurnID:    turnID,
		SessionID: sessionID,
		UserID:    userID,
		Call:      call,
		RawArgs:   rawArgs,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	return id, nil
}

// Resolve marks an approval request as decided.
func (a *InProcessApproval) Resolve(ctx context.Context, id, decidedBy, reason string, approved bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	req, ok := a.items[id]
	if !ok {
		return fmt.Errorf("approval request %s not found", id)
	}
	if req.Resolved {
		return fmt.Errorf("approval request %s already resolved", id)
	}
	req.Resolved = true
	req.Approved = approved
	req.DecidedBy = decidedBy
	req.Reason = reason
	req.ResolvedAt = time.Now().Format(time.RFC3339)
	return nil
}

// Get retrieves an approval request by ID.
func (a *InProcessApproval) Get(ctx context.Context, id string) (turn.ApprovalRequest[turn.TurnCall], error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	req, ok := a.items[id]
	if !ok {
		return turn.ApprovalRequest[turn.TurnCall]{}, fmt.Errorf("approval request %s not found", id)
	}
	return *req, nil
}

// Compile-time check
var _ turn.ApprovalService[turn.TurnCall] = (*InProcessApproval)(nil)

// jsonNoOp is here to keep the json import alive when callers use this
// file but not the approval.go sibling.
var _ = json.Marshal