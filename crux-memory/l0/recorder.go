// Package l0 stores raw conversation messages as append-only JSONL.
//
// L0 is the foundation of the 4-layer memory hierarchy. Every user/assistant
// message is written here immediately, before any LLM extraction runs. This
// guarantees no information loss: even if L1/L2/L3 fail, the raw stream is
// recoverable.
package l0

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Role is the speaker of a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

// Message is one turn in a conversation.
type Message struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Role      Role      `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Recorder appends messages to a per-session JSONL file under BaseDir.
type Recorder struct {
	BaseDir string
	mu      sync.Mutex
}

// NewRecorder creates a recorder rooted at baseDir. The directory is created
// if it does not exist.
func NewRecorder(baseDir string) (*Recorder, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("l0: create base dir: %w", err)
	}
	return &Recorder{BaseDir: baseDir}, nil
}

// Record appends a single message to <baseDir>/<sessionID>.jsonl.
// Returns the assigned ID.
func (r *Recorder) Record(sessionID string, role Role, content string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := fmt.Sprintf("l0_%d_%s", time.Now().UnixNano(), randHex(4))
	msg := Message{
		ID:        id,
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}
	path := r.sessionPath(sessionID)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("l0: open %s: %w", path, err)
	}
	defer f.Close()

	line, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("l0: marshal: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return "", fmt.Errorf("l0: write: %w", err)
	}
	return id, nil
}

// ReadAll returns every message in a session, oldest first.
func (r *Recorder) ReadAll(sessionID string) ([]Message, error) {
	path := r.sessionPath(sessionID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("l0: open %s: %w", path, err)
	}
	defer f.Close()

	var out []Message
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("l0: parse line: %w", err)
		}
		out = append(out, msg)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("l0: scan: %w", err)
	}
	return out, nil
}

// ReadRecent returns the last n messages (or all if fewer exist).
func (r *Recorder) ReadRecent(sessionID string, n int) ([]Message, error) {
	all, err := r.ReadAll(sessionID)
	if err != nil {
		return nil, err
	}
	if len(all) <= n {
		return all, nil
	}
	return all[len(all)-n:], nil
}

func (r *Recorder) sessionPath(sessionID string) string {
	safe := sanitizeSessionID(sessionID)
	return filepath.Join(r.BaseDir, safe+".jsonl")
}

func sanitizeSessionID(s string) string {
	out := make([]rune, 0, len(s))
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

func randHex(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = hex[time.Now().UnixNano()%int64(len(hex))]
		time.Sleep(1 * time.Nanosecond) // ensure uniqueness within tight loops
	}
	return string(b)
}