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

// ApprovalStore is a SQLite-backed turn.ApprovalService implementation.
//
// Schema column `call_payload` stores the typed Call as JSON (new rows).
// Legacy rows have NULL call_payload and reconstruct Call from
// FunctionID + Arguments + RawArgs via the supplied OnLegacyCall hook.
type ApprovalStore struct {
	db *sql.DB

	mu  sync.Mutex
	ids map[string]struct{} // monotonic ID counter cache; not authoritative

	// OnLegacyCall reconstructs Call for legacy rows (call_payload IS NULL).
	// Required for zero-migration backward compat.
	OnLegacyCall func(functionID string, args map[string]any, rawArgs string) (call turn.TurnCall, err error)
}

// NewApprovalStoreFromDB wraps an existing *sql.DB. Caller owns the DB lifecycle.
func NewApprovalStoreFromDB(db *sql.DB, onLegacyCall func(string, map[string]any, string) (turn.TurnCall, error)) *ApprovalStore {
	return &ApprovalStore{
		db:           db,
		ids:          make(map[string]struct{}),
		OnLegacyCall: onLegacyCall,
	}
}

// Close is a no-op for ApprovalStore (DB lifecycle owned by caller).
func (a *ApprovalStore) Close() error { return nil }

// Create persists a new approval request and returns its ID.
// The new request is in decision="pending" with expires_at = now + 24h.
func (a *ApprovalStore) Create(ctx context.Context, turnID, sessionID, userID string, call turn.TurnCall, rawArgs string) (string, error) {
	id := fmt.Sprintf("appr-%d", time.Now().UnixNano())

	callPayload, err := json.Marshal(call)
	if err != nil {
		return "", fmt.Errorf("marshal call: %w", err)
	}

	// Extract function_id for legacy column compat. New consumers can rely on call_payload;
	// legacy consumers (or query-by-name SQL) read function_id.
	functionID, argsMap := extractCallFields(call)
	if rawArgs == "" && argsMap != nil {
		// If consumer didn't pass rawArgs, build it from the args map.
		if b, err := json.Marshal(argsMap); err == nil {
			rawArgs = string(b)
		}
	}

	argsJSON, _ := json.Marshal(argsMap)
	now := time.Now()
	expires := now.Add(24 * time.Hour)

	_, err = a.db.ExecContext(ctx, `
		INSERT INTO approval_requests (
			id, turn_id, session_id, user_id, agent_id, function_id, arguments, raw_args,
			policy_rule, decision, decided_by, reason, created_at, decided_at, expires_at,
			call_payload
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		id, turnID, sessionID, userID, "", functionID,
		string(argsJSON), rawArgs, "", "pending", "", "",
		now.Unix(), nil, expires.Unix(), string(callPayload),
	)
	if err != nil {
		return "", fmt.Errorf("insert approval: %w", err)
	}
	a.mu.Lock()
	a.ids[id] = struct{}{}
	a.mu.Unlock()
	return id, nil
}

// Resolve marks an approval request as decided.
func (a *ApprovalStore) Resolve(ctx context.Context, id, decidedBy, reason string, approved bool) error {
	decision := "denied"
	if approved {
		decision = "approved"
	}
	now := time.Now()
	res, err := a.db.ExecContext(ctx, `
		UPDATE approval_requests
		SET decision = ?, decided_by = ?, reason = ?, decided_at = ?
		WHERE id = ? AND decision = 'pending'
	`, decision, decidedBy, reason, now.Unix(), id)
	if err != nil {
		return fmt.Errorf("update approval: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("approval request %s not found or already decided", id)
	}
	return nil
}

// Get retrieves an approval request by ID, reconstructing Call from
// call_payload or legacy columns as appropriate.
func (a *ApprovalStore) Get(ctx context.Context, id string) (turn.ApprovalRequest[turn.TurnCall], error) {
	row := a.db.QueryRowContext(ctx, `
		SELECT id, turn_id, session_id, user_id, agent_id, function_id, arguments, raw_args,
			policy_rule, decision, decided_by, reason, created_at, decided_at, expires_at, call_payload
		FROM approval_requests WHERE id = ?
	`, id)
	return scanApproval(row, a.OnLegacyCall)
}

// ListPending returns all unresolved requests.
func (a *ApprovalStore) ListPending(ctx context.Context) ([]turn.ApprovalRequest[turn.TurnCall], error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT id, turn_id, session_id, user_id, agent_id, function_id, arguments, raw_args,
			policy_rule, decision, decided_by, reason, created_at, decided_at, expires_at, call_payload
		FROM approval_requests WHERE decision = 'pending' ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query pending: %w", err)
	}
	defer rows.Close()

	var out []turn.ApprovalRequest[turn.TurnCall]
	for rows.Next() {
		req, err := scanApproval(rows, a.OnLegacyCall)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

func scanApproval(row scanner, onLegacyCall func(string, map[string]any, string) (turn.TurnCall, error)) (turn.ApprovalRequest[turn.TurnCall], error) {
	var (
		r           turn.ApprovalRequest[turn.TurnCall]
		agentID     string
		functionID  string
		argsJSON    sql.NullString
		rawArgs     sql.NullString
		policyRule  sql.NullString
		decision    string
		decidedBy   sql.NullString
		reason      sql.NullString
		createdAt   int64
		decidedAt   sql.NullInt64
		expiresAt   sql.NullInt64
		callPayload sql.NullString
	)
	_ = policyRule
	_ = expiresAt
	_ = agentID
	if err := row.Scan(
		&r.ID, &r.TurnID, &r.SessionID, &r.UserID, &agentID, &functionID, &argsJSON, &rawArgs,
		&policyRule, &decision, &decidedBy, &reason, &createdAt, &decidedAt, &expiresAt, &callPayload,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r, fmt.Errorf("approval not found")
		}
		return r, fmt.Errorf("scan approval: %w", err)
	}

	r.Decision = decision
	if decidedBy.Valid {
		r.DecidedBy = decidedBy.String
	}
	if reason.Valid {
		r.Reason = reason.String
	}
	if createdAt > 0 {
		r.CreatedAt = time.Unix(createdAt, 0).Format(time.RFC3339)
	}
	if decidedAt.Valid {
		r.ResolvedAt = time.Unix(decidedAt.Int64, 0).Format(time.RFC3339)
		r.Resolved = true
		r.Approved = decision == "approved"
	}

	// Reconstruct Call.
	if callPayload.Valid && callPayload.String != "" {
		// New row: deserialize Call directly.
		var call turn.TurnCall
		if err := json.Unmarshal([]byte(callPayload.String), &call); err == nil {
			r.Call = call
		}
	} else if onLegacyCall != nil {
		// Legacy row: ask consumer to reconstruct.
		var argsMap map[string]any
		if argsJSON.Valid && argsJSON.String != "" {
			_ = json.Unmarshal([]byte(argsJSON.String), &argsMap)
		}
		call, err := onLegacyCall(functionID, argsMap, rawArgs.String)
		if err == nil {
			r.Call = call
		}
	}
	return r, nil
}

// extractCallFields best-effort extracts function_id and args from a Call.
// This works for any consumer-supplied Call type that has been JSON-marshaled
// and back — i.e. it has top-level fields `Function.Name` or similar.
// For type-safety, consumers should set up their approval flow to pass
// function_id + raw_args directly via a typed ApprovalService wrapper.
func extractCallFields(call turn.TurnCall) (string, map[string]any) {
	if call == nil {
		return "", nil
	}
	// Re-marshal and peek at common shapes.
	b, err := json.Marshal(call)
	if err != nil {
		return "", nil
	}
	var probe map[string]any
	if err := json.Unmarshal(b, &probe); err != nil {
		return "", nil
	}
	var functionID string
	if v, ok := probe["function"].(map[string]any); ok {
		if n, ok := v["name"].(string); ok {
			functionID = n
		}
	}
	if functionID == "" {
		if n, ok := probe["name"].(string); ok {
			functionID = n
		}
	}
	return functionID, probe
}

// Compile-time check
var _ turn.ApprovalService[turn.TurnCall] = (*ApprovalStore)(nil)