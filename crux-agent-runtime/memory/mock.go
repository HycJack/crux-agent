package memory

import (
	"context"
	"sync"
)

// MockProvider is a mock implementation for testing.
type MockProvider struct {
	mu          sync.RWMutex
	entries     map[string]Entry
	StoreFunc   func(ctx context.Context, entry Entry) error
	SearchFunc  func(ctx context.Context, query string, limit int) ([]Entry, error)
	GetFunc     func(ctx context.Context, key string) (Entry, bool, error)
	DeleteFunc  func(ctx context.Context, key string) error
}

// NewMockProvider creates a new mock provider.
func NewMockProvider() *MockProvider {
	return &MockProvider{
		entries: make(map[string]Entry),
	}
}

func (m *MockProvider) Store(ctx context.Context, entry Entry) error {
	if m.StoreFunc != nil {
		return m.StoreFunc(ctx, entry)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[entry.Key] = entry
	return nil
}

func (m *MockProvider) Search(ctx context.Context, query string, limit int) ([]Entry, error) {
	if m.SearchFunc != nil {
		return m.SearchFunc(ctx, query, limit)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []Entry
	for _, e := range m.entries {
		results = append(results, e)
		if limit > 0 && len(results) >= limit {
			break
		}
	}
	return results, nil
}

func (m *MockProvider) Get(ctx context.Context, key string) (Entry, bool, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, key)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.entries[key]
	return entry, ok, nil
}

func (m *MockProvider) Delete(ctx context.Context, key string) error {
	if m.DeleteFunc != nil {
		return m.DeleteFunc(ctx, key)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, key)
	return nil
}

func (m *MockProvider) List(ctx context.Context, filter Filter) ([]Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var results []Entry
	for _, e := range m.entries {
		results = append(results, e)
	}
	return results, nil
}

func (m *MockProvider) FormatForPrompt(ctx context.Context, query string, limit int) (string, error) {
	entries, _ := m.Search(ctx, query, limit)
	return formatEntries(entries), nil
}

func (m *MockProvider) Close() error { return nil }

// WithSearchError configures the mock to return an error on Search.
func (m *MockProvider) WithSearchError(err error) *MockProvider {
	m.SearchFunc = func(ctx context.Context, query string, limit int) ([]Entry, error) {
		return nil, err
	}
	return m
}

// WithStoreError configures the mock to return an error on Store.
func (m *MockProvider) WithStoreError(err error) *MockProvider {
	m.StoreFunc = func(ctx context.Context, entry Entry) error {
		return err
	}
	return m
}

// Entries returns all entries (for testing).
func (m *MockProvider) Entries() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var results []Entry
	for _, e := range m.entries {
		results = append(results, e)
	}
	return results
}
