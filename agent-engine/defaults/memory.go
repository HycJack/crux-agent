package defaults

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hycjack/crux-kernel/plugin"
)

// Memory is a cross-session long-term memory KV store backed by JSON.
type Memory struct {
	mu   sync.RWMutex
	path string
	data map[string]memoryEntry
}

type memoryEntry struct {
	ID        string            `json:"id,omitempty"`
	Key       string            `json:"key"`
	Value     string            `json:"value"`
	Category  string            `json:"category,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// compile-time assertion: *Memory implements plugin.MemoryPlugin
var _ plugin.MemoryPlugin = (*Memory)(nil)

// NewMemory loads or creates a JSON memory file.
func NewMemory(path string) (*Memory, error) {
	m := &Memory{
		path: path,
		data: make(map[string]memoryEntry),
	}

	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}

	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &m.data); err != nil {
			return nil, err
		}
	}

	return m, nil
}

// Get retrieves a value by key.
func (m *Memory) Get(key string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.data[key]
	if !ok {
		return "", false
	}
	return e.Value, true
}

// Set stores a key-value pair.
func (m *Memory) Set(key, value string) {
	m.SetWithCategory(key, value, "")
}

// SetWithCategory stores a key-value pair with a category.
func (m *Memory) SetWithCategory(key, value, category string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	if existing, ok := m.data[key]; ok {
		existing.Value = value
		existing.UpdatedAt = now
		if category != "" {
			existing.Category = category
		}
		m.data[key] = existing
	} else {
		m.data[key] = memoryEntry{
			Key:       key,
			Value:     value,
			Category:  category,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
}

// Delete removes a key.
func (m *Memory) Delete(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
}

// FormatForPrompt formats memory as a string for system prompt injection.
func (m *Memory) FormatForPrompt() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.data) == 0 {
		return ""
	}

	categories := make(map[string][]string)
	for k, v := range m.data {
		cat := v.Category
		if cat == "" {
			cat = "general"
		}
		categories[cat] = append(categories[cat], k+": "+v.Value)
	}

	catNames := make([]string, 0, len(categories))
	for c := range categories {
		catNames = append(catNames, c)
	}
	sort.Strings(catNames)

	result := "# Long-term Memory\n\n"
	for _, cat := range catNames {
		lines := categories[cat]
		sort.Strings(lines)
		for _, line := range lines {
			result += "- " + line + "\n"
		}
	}
	return result
}

// Hash returns a quick hash of memory contents for change detection.
func (m *Memory) Hash() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.data) == 0 {
		return ""
	}
	var sb strings.Builder
	for k, v := range m.data {
		sb.WriteString(k)
		sb.WriteString("|")
		sb.WriteString(v.UpdatedAt.Format(time.RFC3339Nano))
		sb.WriteString(";")
	}
	return sb.String()
}

// Keys returns all keys (sorted alphabetically).
func (m *Memory) Keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Size returns the number of stored memory entries.
func (m *Memory) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.data)
}

// Save persists memory to disk (atomic write via temp file + rename).
func (m *Memory) Save() error {
	m.mu.RLock()
	data, err := json.MarshalIndent(m.data, "", "  ")
	m.mu.RUnlock()
	if err != nil {
		return err
	}

	dir := filepath.Dir(m.path)
	tmp, err := os.CreateTemp(dir, ".memory-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, m.path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
