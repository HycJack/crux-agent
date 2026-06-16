package session

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteStorage is a SQLite-based session storage backend.
// || SQLite 存储后端
type SQLiteStorage struct {
	mu sync.Mutex
	db *sql.DB
}

// NewSQLiteStorage creates a new SQLite storage backend.
func NewSQLiteStorage(dbPath string) (*SQLiteStorage, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, &SessionError{Code: ErrStorage, Message: "failed to open database", Err: err}
	}

	// Create table if not exists.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS session_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			message_data TEXT,
			session_id TEXT,
			provider TEXT,
			model_id TEXT,
			thinking_level TEXT,
			compaction_summary TEXT,
			metadata TEXT,
			content TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_session_entries_type ON session_entries(type);
		CREATE INDEX IF NOT EXISTS idx_session_entries_timestamp ON session_entries(timestamp);
	`)
	if err != nil {
		db.Close()
		return nil, &SessionError{Code: ErrStorage, Message: "failed to create table", Err: err}
	}

	return &SQLiteStorage{db: db}, nil
}

// ReadAll reads all entries from the SQLite database.
func (s *SQLiteStorage) ReadAll() ([]SessionTreeEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT type, timestamp, message_data, session_id, 
		       provider, model_id, thinking_level, 
		       compaction_summary, metadata, content
		FROM session_entries
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, &SessionError{Code: ErrStorage, Message: "failed to query entries", Err: err}
	}
	defer rows.Close()

	var entries []SessionTreeEntry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, &SessionError{Code: ErrStorage, Message: "failed to scan entry", Err: err}
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// Append adds entries to the SQLite database.
func (s *SQLiteStorage) Append(entries []SessionTreeEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return &SessionError{Code: ErrStorage, Message: "failed to begin transaction", Err: err}
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO session_entries 
		(type, timestamp, message_data, session_id, provider, model_id, 
		 thinking_level, compaction_summary, metadata, content)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return &SessionError{Code: ErrStorage, Message: "failed to prepare statement", Err: err}
	}
	defer stmt.Close()

	for _, entry := range entries {
		if entry.Timestamp.IsZero() {
			entry.Timestamp = time.Now()
		}

		var messageData, metadata, content *string
		if len(entry.MessageData) > 0 {
			s := string(entry.MessageData)
			messageData = &s
		}
		if entry.Metadata != nil {
			b, err := json.Marshal(entry.Metadata)
			if err == nil {
				s := string(b)
				metadata = &s
			}
		}
		if len(entry.Content) > 0 {
			s := string(entry.Content)
			content = &s
		}

		_, err = stmt.Exec(
			string(entry.Type),
			entry.Timestamp,
			messageData,
			nilIfEmpty(entry.SessionID),
			nilIfEmpty(entry.Provider),
			nilIfEmpty(entry.ModelID),
			nilIfEmpty(entry.ThinkingLevel),
			nilIfEmpty(entry.CompactionSummary),
			metadata,
			content,
		)
		if err != nil {
			return &SessionError{Code: ErrStorage, Message: "failed to insert entry", Err: err}
		}
	}

	return tx.Commit()
}

// Close closes the database connection.
func (s *SQLiteStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

// scanEntry scans a single row into a SessionTreeEntry.
func scanEntry(rows *sql.Rows) (SessionTreeEntry, error) {
	var (
		entryType         string
		timestamp         time.Time
		messageData       *string
		sessionID         *string
		provider          *string
		modelID           *string
		thinkingLevel     *string
		compactionSummary *string
		metadata          *string
		content           *string
	)

	err := rows.Scan(
		&entryType,
		&timestamp,
		&messageData,
		&sessionID,
		&provider,
		&modelID,
		&thinkingLevel,
		&compactionSummary,
		&metadata,
		&content,
	)
	if err != nil {
		return SessionTreeEntry{}, err
	}

	entry := SessionTreeEntry{
		Type:      EntryType(entryType),
		Timestamp: timestamp,
	}

	if messageData != nil {
		entry.MessageData = json.RawMessage(*messageData)
	}
	if sessionID != nil {
		entry.SessionID = *sessionID
	}
	if provider != nil {
		entry.Provider = *provider
	}
	if modelID != nil {
		entry.ModelID = *modelID
	}
	if thinkingLevel != nil {
		entry.ThinkingLevel = *thinkingLevel
	}
	if compactionSummary != nil {
		entry.CompactionSummary = *compactionSummary
	}
	if metadata != nil {
		var m map[string]any
		if err := json.Unmarshal([]byte(*metadata), &m); err == nil {
			entry.Metadata = m
		}
	}
	if content != nil {
		entry.Content = json.RawMessage(*content)
	}

	return entry, nil
}

// nilIfEmpty returns nil if s is empty, otherwise returns a pointer to s.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
