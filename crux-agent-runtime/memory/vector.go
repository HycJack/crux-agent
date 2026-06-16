package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// VectorClient is the interface for vector database operations.
// || 向量数据库客户端接口
type VectorClient interface {
	// Upsert inserts or updates points in the collection.
	Upsert(ctx context.Context, collection string, points []Point) error

	// Search searches for similar vectors.
	Search(ctx context.Context, collection string, vector []float32, limit int) ([]SearchResult, error)

	// Delete removes a point by ID.
	Delete(ctx context.Context, collection string, id string) error

	// Close closes the connection.
	Close() error
}

// Point is a vector point for storage.
type Point struct {
	ID      string                 `json:"id"`
	Vector  []float32              `json:"vector"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

// SearchResult is a search result from the vector database.
type SearchResult struct {
	ID      string                 `json:"id"`
	Score   float64                `json:"score"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

// Embedder is the interface for text vectorization.
// || 文本向量化接口
type Embedder interface {
	// Embed converts a single text to vector.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch converts multiple texts to vectors.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// VectorStore uses a vector database for semantic memory search.
// || 向量存储后端
type VectorStore struct {
	mu        sync.RWMutex
	client    VectorClient
	embedder  Embedder
	namespace string
}

// NewVectorStore creates a new vector store.
func NewVectorStore(client VectorClient, embedder Embedder, namespace string) *VectorStore {
	return &VectorStore{
		client:    client,
		embedder:  embedder,
		namespace: namespace,
	}
}

func (s *VectorStore) Store(ctx context.Context, entry Entry) error {
	// Generate embedding.
	text := entry.Key + ": " + entry.Value
	embedding, err := s.embedder.Embed(ctx, text)
	if err != nil {
		return fmt.Errorf("vector store embed: %w", err)
	}

	// Store in vector database.
	point := Point{
		ID:     entry.ID,
		Vector: embedding,
		Payload: map[string]interface{}{
			"key":       entry.Key,
			"value":     entry.Value,
			"category":  entry.Category,
			"created_at": entry.CreatedAt.Format(time.RFC3339),
			"updated_at": entry.UpdatedAt.Format(time.RFC3339),
		},
	}

	return s.client.Upsert(ctx, s.namespace, []Point{point})
}

func (s *VectorStore) Search(ctx context.Context, query string, limit int) ([]Entry, error) {
	if query == "" {
		return s.List(ctx, Filter{Limit: limit})
	}

	// Embed query.
	queryVector, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("vector store embed query: %w", err)
	}

	// Vector search.
	results, err := s.client.Search(ctx, s.namespace, queryVector, limit)
	if err != nil {
		return nil, fmt.Errorf("vector store search: %w", err)
	}

	// Convert to entries.
	entries := make([]Entry, 0, len(results))
	for _, r := range results {
		entry := payloadToEntry(r.Payload)
		entry.ID = r.ID
		entries = append(entries, entry)
	}

	return entries, nil
}

func (s *VectorStore) Get(ctx context.Context, key string) (Entry, bool, error) {
	// Vector store doesn't support key-based lookup directly.
	// Use search with the key as query.
	entries, err := s.Search(ctx, key, 1)
	if err != nil {
		return Entry{}, false, err
	}
	if len(entries) == 0 {
		return Entry{}, false, nil
	}
	// Check if the key matches exactly.
	if entries[0].Key == key {
		return entries[0], true, nil
	}
	return Entry{}, false, nil
}

func (s *VectorStore) Delete(ctx context.Context, key string) error {
	// Find the entry by key first.
	entry, found, err := s.Get(ctx, key)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return s.client.Delete(ctx, s.namespace, entry.ID)
}

func (s *VectorStore) List(ctx context.Context, filter Filter) ([]Entry, error) {
	// Vector store doesn't support full listing efficiently.
	// Use a generic query.
	query := ""
	if filter.Category != "" {
		query = filter.Category
	}
	limit := filter.Limit
	if limit == 0 {
		limit = 100
	}
	return s.Search(ctx, query, limit)
}

func (s *VectorStore) FormatForPrompt(ctx context.Context, query string, limit int) (string, error) {
	if limit == 0 {
		limit = 10
	}
	entries, err := s.Search(ctx, query, limit)
	if err != nil {
		return "", err
	}
	return formatEntries(entries), nil
}

func (s *VectorStore) Close() error {
	return s.client.Close()
}

// payloadToEntry converts a vector payload to Entry.
func payloadToEntry(payload map[string]interface{}) Entry {
	entry := Entry{}
	if v, ok := payload["key"].(string); ok {
		entry.Key = v
	}
	if v, ok := payload["value"].(string); ok {
		entry.Value = v
	}
	if v, ok := payload["category"].(string); ok {
		entry.Category = v
	}
	if v, ok := payload["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			entry.CreatedAt = t
		}
	}
	if v, ok := payload["updated_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			entry.UpdatedAt = t
		}
	}
	return entry
}

// InMemoryVectorClient is a simple in-memory vector client for testing.
// || 内存向量客户端（测试用）
type InMemoryVectorClient struct {
	mu      sync.RWMutex
	vectors map[string]map[string]Point // collection -> id -> point
}

// NewInMemoryVectorClient creates a new in-memory vector client.
func NewInMemoryVectorClient() *InMemoryVectorClient {
	return &InMemoryVectorClient{
		vectors: make(map[string]map[string]Point),
	}
}

func (c *InMemoryVectorClient) Upsert(ctx context.Context, collection string, points []Point) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vectors[collection] == nil {
		c.vectors[collection] = make(map[string]Point)
	}
	for _, p := range points {
		c.vectors[collection][p.ID] = p
	}
	return nil
}

func (c *InMemoryVectorClient) Search(ctx context.Context, collection string, vector []float32, limit int) ([]SearchResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	points := c.vectors[collection]
	if len(points) == 0 {
		return nil, nil
	}

	// Simple cosine similarity.
	type scored struct {
		id    string
		score float64
		payload map[string]interface{}
	}

	var results []scored
	for id, p := range points {
		score := cosineSimilarity(vector, p.Vector)
		results = append(results, scored{id: id, score: score, payload: p.Payload})
	}

	// Sort by score descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	searchResults := make([]SearchResult, len(results))
	for i, r := range results {
		searchResults[i] = SearchResult{
			ID:      r.id,
			Score:   r.score,
			Payload: r.payload,
		}
	}
	return searchResults, nil
}

func (c *InMemoryVectorClient) Delete(ctx context.Context, collection string, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vectors[collection] != nil {
		delete(c.vectors[collection], id)
	}
	return nil
}

func (c *InMemoryVectorClient) Close() error { return nil }

// cosineSimilarity calculates cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (sqrt(normA) * sqrt(normB))
}

func sqrt(x float64) float64 {
	// Simple Newton's method.
	z := x / 2
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// SimpleEmbedder is a basic embedder for testing (hash-based, not semantic).
// || 简单嵌入器（测试用，非语义）
type SimpleEmbedder struct {
	dimensions int
}

// NewSimpleEmbedder creates a simple hash-based embedder for testing.
func NewSimpleEmbedder(dimensions int) *SimpleEmbedder {
	return &SimpleEmbedder{dimensions: dimensions}
}

func (e *SimpleEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vec := make([]float32, e.dimensions)
	for i, r := range text {
		vec[i%e.dimensions] += float32(r)
	}
	// Normalize.
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm > 0 {
		norm = sqrt(norm)
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / norm)
		}
	}
	return vec, nil
}

func (e *SimpleEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := e.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		results[i] = vec
	}
	return results, nil
}

// VectorStoreAdapter adapts the old Memory interface to MemoryProvider.
// || 适配器：将旧的 Memory 适配为 MemoryProvider
type VectorStoreAdapter struct {
	inner *Memory
}

// NewVectorStoreAdapter wraps the old Memory as a MemoryProvider.
func NewVectorStoreAdapter(mem *Memory) *VectorStoreAdapter {
	return &VectorStoreAdapter{inner: mem}
}

func (a *VectorStoreAdapter) Store(ctx context.Context, entry Entry) error {
	a.inner.SetWithCategory(entry.Key, entry.Value, entry.Category)
	return a.inner.Save()
}

func (a *VectorStoreAdapter) Search(ctx context.Context, query string, limit int) ([]Entry, error) {
	// KV store: keyword matching.
	a.inner.mu.RLock()
	defer a.inner.mu.RUnlock()

	var results []Entry
	queryLower := strings.ToLower(query)
	for _, e := range a.inner.data {
		if strings.Contains(strings.ToLower(e.Key), queryLower) ||
			strings.Contains(strings.ToLower(e.Value), queryLower) {
			results = append(results, e)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (a *VectorStoreAdapter) Get(ctx context.Context, key string) (Entry, bool, error) {
	a.inner.mu.RLock()
	defer a.inner.mu.RUnlock()
	entry, ok := a.inner.data[key]
	return entry, ok, nil
}

func (a *VectorStoreAdapter) Delete(ctx context.Context, key string) error {
	a.inner.Delete(key)
	return a.inner.Save()
}

func (a *VectorStoreAdapter) List(ctx context.Context, filter Filter) ([]Entry, error) {
	a.inner.mu.RLock()
	defer a.inner.mu.RUnlock()

	var results []Entry
	for _, e := range a.inner.data {
		if filter.Category != "" && e.Category != filter.Category {
			continue
		}
		results = append(results, e)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})

	if filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}
	return results, nil
}

func (a *VectorStoreAdapter) FormatForPrompt(ctx context.Context, query string, limit int) (string, error) {
	entries, err := a.Search(ctx, query, limit)
	if err != nil {
		return "", err
	}
	return formatEntries(entries), nil
}

func (a *VectorStoreAdapter) Close() error {
	return a.inner.Save()
}
