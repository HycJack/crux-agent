package hash

import (
	"testing"
)

func TestShort(t *testing.T) {
	// Same input should always produce same output
	h1 := Short("hello")
	h2 := Short("hello")
	if h1 != h2 {
		t.Errorf("expected same hash for same input, got %s and %s", h1, h2)
	}

	// Different inputs should produce different outputs
	h3 := Short("world")
	if h1 == h3 {
		t.Errorf("expected different hashes for different inputs, got %s", h1)
	}
}

func TestShortEmpty(t *testing.T) {
	h := Short("")
	if h == "" {
		t.Error("expected non-empty hash for empty string")
	}
}

func TestShortDeterministic(t *testing.T) {
	// Hash should be deterministic across runs
	h := Short("test_string_123")
	// We can't check the exact value without knowing the algorithm,
	// but we can check it's consistent
	h2 := Short("test_string_123")
	if h != h2 {
		t.Errorf("hash not deterministic: %s != %s", h, h2)
	}
}

func TestShortBase36(t *testing.T) {
	h := Short("test")
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')) {
			t.Errorf("hash contains non-base36 character: %c", c)
		}
	}
}
