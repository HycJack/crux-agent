// Package memory provides persistent long-term memory for agents.
//
// Design goals:
//   - Cross-session persistence: user preferences, task progress, key facts
//   - JSON persistence: human-readable, hand-editable
//   - Simple API: Get/Set/Delete/List, no heavy database
//
// Relationship with session package:
//   - session manages "session-level" conversation history
//   - memory manages "cross-session" long-term facts
package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is a long-term memory KV store.
// || 长期记忆 KV 存储
//
// Example:
//
//	mem, _ := memory.New("/path/to/memory.json")
//	mem.Set("user.name", "小明")
//	mem.Set("user.preferred_language", "zh-CN")
//	name, ok := mem.Get("user.name")
//	mem.Save() // persist to disk
type Memory struct {
	mu   sync.RWMutex
	path string
	data map[string]Entry
}

// Entry is a single memory entry.
// || 单条记忆条目
type Entry struct {
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Category for grouping (e.g. "user", "task", "preference").
	Category string `json:"category,omitempty"`
}

// New loads or creates a memory file. If the file doesn't exist,
// it creates an empty memory store.
func New(path string) (*Memory, error) {
	m := &Memory{
		path: path,
		data: make(map[string]Entry),
	}

	// Ensure directory exists.
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}

	// Try to load existing data.
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &m.data); err != nil {
			return nil, err
		}
	}

	return m, nil
}

// Get retrieves the value for a key.
// Returns the value and whether it exists.
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
// Automatically updates the UpdatedAt timestamp.
func (m *Memory) Set(key, value string) {
	m.SetWithCategory(key, value, "")
}

// SetWithCategory stores a key-value pair with a category.
func (m *Memory) SetWithCategory(key, value, category string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	existing, exists := m.data[key]
	if exists {
		existing.Value = value
		existing.UpdatedAt = now
		if category != "" {
			existing.Category = category
		}
		m.data[key] = existing
	} else {
		m.data[key] = Entry{
			Value:     value,
			CreatedAt: now,
			UpdatedAt: now,
			Category:  category,
		}
	}
}

// Delete removes a key.
func (m *Memory) Delete(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
}

// Has checks if a key exists.
func (m *Memory) Has(key string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.data[key]
	return ok
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

// ListByCategory returns all entries in a category.
func (m *Memory) ListByCategory(category string) []Item {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var items []Item
	for k, v := range m.data {
		if v.Category == category {
			items = append(items, Item{Key: k, Entry: v})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Key < items[j].Key
	})
	return items
}

// Item is an entry returned by ListByCategory.
type Item struct {
	Key   string
	Entry Entry
}

// Size returns the number of entries.
func (m *Memory) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.data)
}

// Hash returns a quick hash of memory contents.
// Used to detect changes (to decide whether to rebuild system prompt).
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

// Save persists to disk (atomic write via temp file + rename).
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

// Load reloads from disk (overwrites in-memory data).
func (m *Memory) Load() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return json.Unmarshal(data, &m.data)
}

// FormatForPrompt formats long-term memory as a string for injection
// into the system prompt.
//
// Format:
//
//	# Long-term Memory
//
//	- user.name: 小明
//	- user.language: zh-CN
//	- task.current: 完善 demo 项目
func (m *Memory) FormatForPrompt() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.data) == 0 {
		return ""
	}

	// Group by category.
	categories := make(map[string][]string)
	for k, v := range m.data {
		cat := v.Category
		if cat == "" {
			cat = "general"
		}
		categories[cat] = append(categories[cat], k+": "+v.Value)
	}

	// Sort category names.
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
