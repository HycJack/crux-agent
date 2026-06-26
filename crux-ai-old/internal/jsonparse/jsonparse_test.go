package jsonparse

import (
	"testing"
)

type testStruct struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestParseValid(t *testing.T) {
	input := `{"name":"test","value":42}`
	result, err := Parse[testStruct](input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "test" || result.Value != 42 {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestParseRepair(t *testing.T) {
	// Invalid JSON with raw control characters
	input := "{\"name\":\"test\nline\",\"value\":42}"
	result, err := Parse[testStruct](input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "test\nline" {
		t.Errorf("unexpected name: %s", result.Name)
	}
}

func TestRepairControlChars(t *testing.T) {
	input := "{\"text\":\"hello\nworld\"}"
	output := Repair(input)
	if output != "{\"text\":\"hello\\nworld\"}" {
		t.Errorf("unexpected repair result: %s", output)
	}
}

func TestRepairBadEscape(t *testing.T) {
	input := `{"text":"hello\qworld"}`
	output := Repair(input)
	// Should remove the backslash before unknown escape
	if output != `{"text":"helloqworld"}` {
		t.Errorf("unexpected repair result: %s", output)
	}
}

func TestStreamingComplete(t *testing.T) {
	input := `{"name":"test","value":42}`
	result, ok := Streaming[testStruct](input)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if result.Name != "test" {
		t.Errorf("unexpected name: %s", result.Name)
	}
}

func TestStreamingIncomplete(t *testing.T) {
	input := `{"name":"test","value":`
	result, ok := Streaming[testStruct](input)
	if !ok {
		t.Fatal("expected ok=true for completable JSON")
	}
	if result.Name != "test" {
		t.Errorf("unexpected name: %s", result.Name)
	}
}

func TestStreamingIncompleteString(t *testing.T) {
	input := `{"name":"hel`
	result, ok := Streaming[testStruct](input)
	if !ok {
		t.Fatal("expected ok=true for completable JSON")
	}
	if result.Name != "hel" {
		t.Errorf("unexpected name: %s", result.Name)
	}
}

func TestCompleteJSON(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{}`, `{}`},
		{`{"a":1}`, `{"a":1}`},
		{`{"a":1,`, `{"a":1}`},
		{`{"a":"b`, `{"a":"b"}`},
		{`[1,2`, `[1,2]`},
		{`{"a":{"b":1`, `{"a":{"b":1}}`},
	}

	for _, tt := range tests {
		got := completeJSON(tt.input)
		if got != tt.want {
			t.Errorf("completeJSON(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTrimTrailingComma(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"a":1,`, `{"a":1`},
		{`{"a":1}`, `{"a":1}`},
		{`[1,2,`, `[1,2`},
	}

	for _, tt := range tests {
		got := TrimTrailingComma(tt.input)
		if got != tt.want {
			t.Errorf("TrimTrailingComma(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
