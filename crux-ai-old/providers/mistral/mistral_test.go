package mistral

import (
	"testing"
)

func TestNormalizeToolCallID(t *testing.T) {
	tests := []struct {
		input string
		len   int
	}{
		{"call_123", 9},
		{"abc", 9},
		{"a]very_long_id_that_needs_truncation", 9},
	}

	for _, tt := range tests {
		result := normalizeToolCallID(tt.input)
		if len(result) != tt.len {
			t.Errorf("normalizeToolCallID(%s): expected length %d, got %d", tt.input, tt.len, len(result))
		}
		// Check all chars are alphanumeric
		for _, c := range result {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
				t.Errorf("normalizeToolCallID(%s): invalid character %c", tt.input, c)
			}
		}
	}
}

func TestNormalizeToolCallIDDeterministic(t *testing.T) {
	id := "test_id_123"
	r1 := normalizeToolCallID(id)
	r2 := normalizeToolCallID(id)
	if r1 != r2 {
		t.Errorf("expected deterministic result: %s != %s", r1, r2)
	}
}

func TestMapStopReason(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"stop", "stop"},
		{"length", "length"},
		{"tool_calls", "toolUse"},
		{"unknown", "stop"},
	}

	for _, tt := range tests {
		got := mapStopReason(tt.input)
		if string(got) != tt.want {
			t.Errorf("mapStopReason(%s) = %s, want %s", tt.input, got, tt.want)
		}
	}
}
