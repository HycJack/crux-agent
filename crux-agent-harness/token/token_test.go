package token

import (
	"testing"
)

func TestCounter(t *testing.T) {
	c, err := New("gpt-4o")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	tests := []struct {
		text string
		min  int
		max  int
	}{
		{"Hello, world!", 2, 10},
		{"", 0, 0},
		{"This is a longer sentence with multiple words to test token counting.", 10, 30},
	}

	for _, tt := range tests {
		count := c.CountTokens(tt.text)
		if count < tt.min || count > tt.max {
			t.Errorf("CountTokens(%q) = %d, want [%d, %d]", tt.text, count, tt.min, tt.max)
		}
	}
}

func TestGetCounter(t *testing.T) {
	c1, err := GetCounter("gpt-4o")
	if err != nil {
		t.Fatalf("GetCounter failed: %v", err)
	}
	c2, err := GetCounter("gpt-4o")
	if err != nil {
		t.Fatalf("GetCounter failed: %v", err)
	}
	if c1 != c2 {
		t.Error("GetCounter should return cached instance")
	}
}
