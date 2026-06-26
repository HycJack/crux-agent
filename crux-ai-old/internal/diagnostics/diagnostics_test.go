package diagnostics

import (
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	d := New("test_type")
	if d.Type != "test_type" {
		t.Errorf("expected type 'test_type', got %s", d.Type)
	}
	if d.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestNewWithDetails(t *testing.T) {
	d := New("test_type", "some details")
	if d.Details != "some details" {
		t.Errorf("expected details 'some details', got %v", d.Details)
	}
}

func TestNewWithError(t *testing.T) {
	err := errors.New("test error")
	d := NewWithError("error_type", err)
	if d.Error != "test error" {
		t.Errorf("expected error 'test error', got %s", d.Error)
	}
}

func TestExtractError(t *testing.T) {
	d := Diagnostic{Error: "some error"}
	if ExtractError(d) != "some error" {
		t.Errorf("expected 'some error', got %s", ExtractError(d))
	}

	d2 := Diagnostic{}
	if ExtractError(d2) != "" {
		t.Errorf("expected empty string, got %s", ExtractError(d2))
	}
}

func TestFormatThrownValue(t *testing.T) {
	tests := []struct {
		input any
		want  string
	}{
		{nil, "<nil>"},
		{errors.New("error msg"), "error msg"},
		{"string val", "string val"},
		{42, "42"},
	}

	for _, tt := range tests {
		got := FormatThrownValue(tt.input)
		if got != tt.want {
			t.Errorf("FormatThrownValue(%v): got %s, want %s", tt.input, got, tt.want)
		}
	}
}
