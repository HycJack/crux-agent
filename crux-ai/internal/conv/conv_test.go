package conv

import (
	"testing"
)

func TestGetFloat(t *testing.T) {
	m := map[string]any{"x": 1.5, "s": "abc"}
	if got := GetFloat(m, "x"); got != 1.5 {
		t.Errorf("GetFloat(x) = %v, want 1.5", got)
	}
	if got := GetFloat(m, "s"); got != 0 {
		t.Errorf("GetFloat(s) = %v, want 0", got)
	}
	if got := GetFloat(m, "missing"); got != 0 {
		t.Errorf("GetFloat(missing) = %v, want 0", got)
	}
}

func TestGetFloatPath(t *testing.T) {
	m := map[string]any{
		"a": map[string]any{
			"b": map[string]any{"c": 42.0},
		},
	}
	if got := GetFloatPath(m, "a.b.c"); got != 42.0 {
		t.Errorf("GetFloatPath = %v, want 42", got)
	}
	if got := GetFloatPath(m, "a.b.missing"); got != 0 {
		t.Errorf("GetFloatPath missing = %v, want 0", got)
	}
	if got := GetFloatPath(m, "a.b.c.d"); got != 0 {
		t.Errorf("GetFloatPath too deep = %v, want 0", got)
	}
}

func TestGetString(t *testing.T) {
	m := map[string]any{"s": "hello", "n": 42}
	if got := GetString(m, "s"); got != "hello" {
		t.Errorf("GetString(s) = %q, want hello", got)
	}
	if got := GetString(m, "n"); got != "" {
		t.Errorf("GetString(n) = %q, want empty", got)
	}
	if got := GetString(m, "missing"); got != "" {
		t.Errorf("GetString(missing) = %q, want empty", got)
	}
}

func TestGetBool(t *testing.T) {
	m := map[string]any{"b": true, "s": "true"}
	if !GetBool(m, "b") {
		t.Error("expected true")
	}
	if GetBool(m, "s") {
		t.Error("expected false for string")
	}
}

func TestGetInt(t *testing.T) {
	m := map[string]any{"x": 42.7}
	if got := GetInt(m, "x"); got != 42 {
		t.Errorf("GetInt = %d, want 42", got)
	}
}

func TestMergeMaps(t *testing.T) {
	a := map[string]string{"a": "1", "b": "2"}
	b := map[string]string{"b": "3", "c": "4"}
	merged := MergeMaps(a, b)
	if merged["a"] != "1" {
		t.Errorf("a = %q, want 1", merged["a"])
	}
	if merged["b"] != "3" {
		t.Errorf("b = %q, want 3 (overwritten)", merged["b"])
	}
	if merged["c"] != "4" {
		t.Errorf("c = %q, want 4", merged["c"])
	}
	if a["b"] != "2" {
		t.Errorf("input a should not be modified, got b=%q", a["b"])
	}
}

func TestMergeMapsEmpty(t *testing.T) {
	merged := MergeMaps()
	if merged == nil {
		t.Error("expected non-nil empty map")
	}
	if len(merged) != 0 {
		t.Errorf("expected empty, got %v", merged)
	}
}

func TestJoinStrings(t *testing.T) {
	tests := []struct {
		in   []string
		sep  string
		want string
	}{
		{nil, ",", ""},
		{[]string{}, ",", ""},
		{[]string{"a"}, ",", "a"},
		{[]string{"a", "b"}, ",", "a,b"},
		{[]string{"a", "b", "c"}, "-", "a-b-c"},
	}
	for _, tc := range tests {
		got := JoinStrings(tc.in, tc.sep)
		if got != tc.want {
			t.Errorf("JoinStrings(%v, %q) = %q, want %q", tc.in, tc.sep, got, tc.want)
		}
	}
}

func TestSanitizeSurrogates_NoChange(t *testing.T) {
	input := "Hello 世界 🚀"
	got := SanitizeSurrogates(input)
	if got != input {
		t.Errorf("expected no change, got %q", got)
	}
}

func TestSanitizeSurrogates_UnpairedHigh(t *testing.T) {
	// Lone high surrogate: 0xD83D (high half of emoji).
	input := "before\xed\xa0\xbDafter"
	got := SanitizeSurrogates(input)
	if got != "beforeafter" {
		t.Errorf("expected lone surrogate dropped, got %q", got)
	}
}

func TestSanitizeSurrogates_UnpairedLow(t *testing.T) {
	// Lone low surrogate: 0xDE00.
	input := "before\xed\xb8\x80after"
	got := SanitizeSurrogates(input)
	if got != "beforeafter" {
		t.Errorf("expected lone surrogate dropped, got %q", got)
	}
}

func TestSanitizeSurrogates_ValidPair(t *testing.T) {
	// 🚀 is U+1F680 — valid pair of high (D83D) and low (DE80).
	input := "rocket 🚀 here"
	got := SanitizeSurrogates(input)
	if got != input {
		t.Errorf("expected pair preserved, got %q", got)
	}
}

func TestSanitizeSurrogates_InvalidUTF8(t *testing.T) {
	// 0xFF is invalid UTF-8 byte.
	input := "before\xffafter"
	got := SanitizeSurrogates(input)
	if got != "beforeafter" {
		t.Errorf("expected invalid byte dropped, got %q", got)
	}
}
