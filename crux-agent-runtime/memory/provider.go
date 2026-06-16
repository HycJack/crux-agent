package memory

import (
	"context"
	"time"
)

// MemoryProvider is the unified interface for all memory backends.
// || 所有记忆后端的统一接口
//
// Implementations:
//   - KVStore: JSON file storage (default)
//   - VectorStore: vector database (Qdrant, Milvus, Chroma)
//   - Mem0Provider: Mem0 cloud service
//   - RAGProvider: RAG retrieval system
type MemoryProvider interface {
	// Store stores a single memory entry.
	Store(ctx context.Context, entry Entry) error

	// Search searches for relevant memories using natural language query.
	// The backend may use keyword matching (KV), vector similarity, or LLM extraction.
	Search(ctx context.Context, query string, limit int) ([]Entry, error)

	// Get retrieves a memory by key.
	Get(ctx context.Context, key string) (Entry, bool, error)

	// Delete removes a memory by key.
	Delete(ctx context.Context, key string) error

	// List lists all memories matching the filter.
	List(ctx context.Context, filter Filter) ([]Entry, error)

	// FormatForPrompt formats memories as a string for system prompt injection.
	// query is used for semantic search (if supported), limit caps the results.
	FormatForPrompt(ctx context.Context, query string, limit int) (string, error)

	// Close closes the underlying connection.
	Close() error
}

// Filter is used to filter memories in List.
type Filter struct {
	Category string
	Keys     []string
	Since    *time.Time
	Limit    int
}

// formatEntries formats a slice of entries as a prompt string.
func formatEntries(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}

	result := "# Long-term Memory\n\n"
	for _, e := range entries {
		result += "- " + e.Key + ": " + e.Value + "\n"
	}
	return result
}
