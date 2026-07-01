// Package sqlite provides a SQLite-backed Store + ApprovalStore for the
// turn library. Uses modernc.org/sqlite (pure Go, no CGO).
//
// Schema is fully compatible with the legacy crux-harness/approval/sqlite
// table layout — old DBs open without migration. The new column `call_payload`
// carries the typed Call as JSON for adapter-aware consumers; legacy rows
// leave it NULL and the consumer reconstructs Call from FunctionID+Arguments+RawArgs.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	// Pure-Go SQLite driver — no CGO required.
	_ "modernc.org/sqlite"
)

// Open opens (or creates) a SQLite database file at the given path.
// Parent directories are created if missing. WAL mode + busy_timeout are enabled
// for safe concurrent reads under load.
func Open(path string) (*sql.DB, error) {
	if len(path) > 1 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, path[2:])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return db, nil
}

// Migrate runs the canonical turn + approval schema. Idempotent.
func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, schemaSQL)
	return err
}

// schemaSQL is the canonical schema for the turn library's SQLite layer.
//
// turns: mirrors legacy crux-harness/turn/sqlite.go exactly so old DBs
//        open without migration. Messages/Pending/Metadata stored as JSON.
//
// approval_requests: mirrors legacy crux-harness/approval/sqlite.go.
//   Legacy rows have NULL call_payload; new rows serialize Call there.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS turns (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	user_id TEXT,
	agent_id TEXT,
	state TEXT NOT NULL,
	round INTEGER DEFAULT 0,
	messages TEXT,
	pending TEXT,
	metadata TEXT,
	error TEXT,
	created_at INTEGER,
	updated_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_turns_session ON turns(session_id);
CREATE INDEX IF NOT EXISTS idx_turns_state ON turns(state);
CREATE INDEX IF NOT EXISTS idx_turns_updated ON turns(updated_at DESC);

CREATE TABLE IF NOT EXISTS approval_requests (
	id TEXT PRIMARY KEY,
	turn_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	user_id TEXT,
	agent_id TEXT,
	function_id TEXT NOT NULL,
	arguments TEXT,
	raw_args TEXT,
	policy_rule TEXT,
	decision TEXT NOT NULL,
	decided_by TEXT,
	reason TEXT,
	created_at INTEGER,
	decided_at INTEGER,
	expires_at INTEGER,
	call_payload TEXT
);
CREATE INDEX IF NOT EXISTS idx_appr_pending ON approval_requests(decision, expires_at);
CREATE INDEX IF NOT EXISTS idx_appr_turn ON approval_requests(turn_id);
CREATE INDEX IF NOT EXISTS idx_appr_session ON approval_requests(session_id);
`