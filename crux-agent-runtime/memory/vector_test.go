package memory

import (
	"context"
	"testing"
)

func TestInMemoryVectorClient(t *testing.T) {
	client := NewInMemoryVectorClient()
	ctx := context.Background()

	// Upsert.
	err := client.Upsert(ctx, "test", []Point{
		{ID: "1", Vector: []float32{1, 0, 0}, Payload: map[string]interface{}{"key": "a"}},
		{ID: "2", Vector: []float32{0, 1, 0}, Payload: map[string]interface{}{"key": "b"}},
	})
	if err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	// Search.
	results, err := client.Search(ctx, "test", []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "1" {
		t.Errorf("expected first result ID=1, got %s", results[0].ID)
	}

	// Delete.
	err = client.Delete(ctx, "test", "1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	results, _ = client.Search(ctx, "test", []float32{1, 0, 0}, 2)
	if len(results) != 1 {
		t.Errorf("expected 1 result after delete, got %d", len(results))
	}
}

func TestSimpleEmbedder(t *testing.T) {
	embedder := NewSimpleEmbedder(10)
	ctx := context.Background()

	vec, err := embedder.Embed(ctx, "hello world")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(vec) != 10 {
		t.Errorf("expected vector length 10, got %d", len(vec))
	}

	// Same text should produce same vector.
	vec2, _ := embedder.Embed(ctx, "hello world")
	for i := range vec {
		if vec[i] != vec2[i] {
			t.Errorf("vectors differ at index %d", i)
		}
	}

	// Different text should produce different vector.
	vec3, _ := embedder.Embed(ctx, "goodbye")
	same := true
	for i := range vec {
		if vec[i] != vec3[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different texts should produce different vectors")
	}
}

func TestSimpleEmbedder_Batch(t *testing.T) {
	embedder := NewSimpleEmbedder(10)
	ctx := context.Background()

	vecs, err := embedder.EmbedBatch(ctx, []string{"hello", "world"})
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	if len(vecs) != 2 {
		t.Errorf("expected 2 vectors, got %d", len(vecs))
	}
}

func TestVectorStore(t *testing.T) {
	client := NewInMemoryVectorClient()
	embedder := NewSimpleEmbedder(10)
	store := NewVectorStore(client, embedder, "test")
	ctx := context.Background()

	// Store.
	err := store.Store(ctx, Entry{
		ID:       "1",
		Key:      "user.name",
		Value:    "小明",
		Category: "user",
	})
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Search.
	results, err := store.Search(ctx, "user name", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}

	// FormatForPrompt.
	prompt, err := store.FormatForPrompt(ctx, "user", 5)
	if err != nil {
		t.Fatalf("FormatForPrompt failed: %v", err)
	}
	if prompt == "" {
		t.Error("expected non-empty prompt")
	}
}

func TestVectorStoreAdapter(t *testing.T) {
	mem, _ := New(t.TempDir() + "/test.json")
	adapter := NewVectorStoreAdapter(mem)
	ctx := context.Background()

	// Store.
	err := adapter.Store(ctx, Entry{Key: "user.name", Value: "小明", Category: "user"})
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Get.
	entry, found, err := adapter.Get(ctx, "user.name")
	if err != nil || !found {
		t.Fatalf("Get failed: err=%v, found=%v", err, found)
	}
	if entry.Value != "小明" {
		t.Errorf("expected value '小明', got %q", entry.Value)
	}

	// Search.
	results, err := adapter.Search(ctx, "name", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least 1 result")
	}

	// Delete.
	err = adapter.Delete(ctx, "user.name")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, found, _ = adapter.Get(ctx, "user.name")
	if found {
		t.Error("expected entry to be deleted")
	}
}

func TestPayloadToEntry(t *testing.T) {
	payload := map[string]interface{}{
		"key":       "user.name",
		"value":     "小明",
		"category":  "user",
		"created_at": "2024-01-01T00:00:00Z",
		"updated_at": "2024-01-02T00:00:00Z",
	}

	entry := payloadToEntry(payload)
	if entry.Key != "user.name" {
		t.Errorf("expected key 'user.name', got %q", entry.Key)
	}
	if entry.Value != "小明" {
		t.Errorf("expected value '小明', got %q", entry.Value)
	}
	if entry.Category != "user" {
		t.Errorf("expected category 'user', got %q", entry.Category)
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		a, b []float32
		want float64
	}{
		{[]float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{[]float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{[]float32{1, 0, 0}, []float32{-1, 0, 0}, -1.0},
	}

	for _, tt := range tests {
		got := cosineSimilarity(tt.a, tt.b)
		if got < tt.want-0.001 || got > tt.want+0.001 {
			t.Errorf("cosineSimilarity(%v, %v) = %f, want %f", tt.a, tt.b, got, tt.want)
		}
	}
}
