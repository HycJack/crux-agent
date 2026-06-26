package l1

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Writer writes L1 atomic memory records as JSONL.
type Writer struct {
	BaseDir string
	mu      sync.Mutex
}

// NewWriter creates a writer rooted at baseDir/l1/.
func NewWriter(baseDir string) (*Writer, error) {
	p := filepath.Join(baseDir, "l1")
	if err := os.MkdirAll(p, 0o755); err != nil {
		return nil, fmt.Errorf("l1: create dir: %w", err)
	}
	return &Writer{BaseDir: p}, nil
}

// GenerateID returns a new record ID.
func GenerateID() string {
	return fmt.Sprintf("l1_%d_%s", time.Now().UnixNano(), randHex(4))
}

// Write appends one record to the L1 JSONL file.
func (w *Writer) Write(rec Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if rec.ID == "" {
		rec.ID = GenerateID()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("l1: marshal: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(w.BaseDir, "memories.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("l1: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("l1: write: %w", err)
	}
	return nil
}

// WriteMany appends a batch of records.
func (w *Writer) WriteMany(recs []Record) error {
	for _, r := range recs {
		if err := w.Write(r); err != nil {
			return err
		}
	}
	return nil
}

// ReadAll returns every active (non-superseded) record.
func (w *Writer) ReadAll() ([]Record, error) {
	f, err := os.Open(filepath.Join(w.BaseDir, "memories.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Record
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("l1: parse: %w", err)
		}
		if rec.SupersededBy == "" {
			out = append(out, rec)
		}
	}
	return out, scanner.Err()
}

func randHex(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = hex[time.Now().UnixNano()%int64(len(hex))]
		time.Sleep(1 * time.Nanosecond)
	}
	return string(b)
}