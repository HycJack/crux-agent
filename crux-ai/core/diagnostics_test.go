package core

import (
	"errors"
	"testing"
)

type sentinelErr struct{ msg string }

func (s sentinelErr) Error() string { return s.msg }

type coderErr struct{}

func (coderErr) Error() string { return "coded" }
func (coderErr) Code() string  { return "E_CODED" }

func TestFormatThrownValue(t *testing.T) {
	if got := FormatThrownValue(errors.New("boom")); got != "boom" {
		t.Errorf("error: got %q", got)
	}
	if got := FormatThrownValue("plain"); got != "plain" {
		t.Errorf("string: got %q", got)
	}
	if got := FormatThrownValue(42); got != "42" {
		t.Errorf("int: got %q", got)
	}
	if got := FormatThrownValue(sentinelErr{msg: "x"}); got != "x" {
		t.Errorf("custom error: got %q", got)
	}
}

func TestExtractDiagnosticError(t *testing.T) {
	got := ExtractDiagnosticError(coderErr{})
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.Code != "E_CODED" {
		t.Errorf("code: got %q", got.Code)
	}
	if got.Message != "coded" {
		t.Errorf("message: got %q", got.Message)
	}
}

func TestNewDiagnostic(t *testing.T) {
	d := NewDiagnostic("retry", errors.New("flaky"), map[string]any{"attempt": 2})
	if d.Type != "retry" {
		t.Errorf("type: got %q", d.Type)
	}
	if d.Error == nil || d.Error.Message != "flaky" {
		t.Errorf("error not attached: %+v", d.Error)
	}
	if d.Details["attempt"] != 2 {
		t.Errorf("details lost: %+v", d.Details)
	}
	if d.Timestamp.IsZero() {
		t.Errorf("timestamp unset")
	}
}

func TestAppendDiagnostic(t *testing.T) {
	msg := &AssistantMessage{}
	AppendDiagnostic(msg, NewDiagnostic("retry", errors.New("e1"), nil))
	AppendDiagnostic(msg, NewDiagnostic("recovered", nil, map[string]any{"ok": true}))
	if len(msg.Diagnostics) != 2 {
		t.Fatalf("expected 2 diagnostics, got %d", len(msg.Diagnostics))
	}
	if msg.Diagnostics[0].Type != "retry" || msg.Diagnostics[1].Type != "recovered" {
		t.Errorf("order: %+v", msg.Diagnostics)
	}
}

func TestAppendDiagnostic_NilMessage(t *testing.T) {
	AppendDiagnostic(nil, NewDiagnostic("x", nil, nil))
}

func TestAppendDiagnostic_NilDiag(t *testing.T) {
	// Even a zero-valued diagnostic is appended; the caller is responsible
	// for not constructing one. This test only verifies the nil-message
	// branch did not panic and produced a usable Diagnostics slice.
	msg := &AssistantMessage{}
	AppendDiagnostic(msg, NewDiagnostic("", nil, nil))
	if len(msg.Diagnostics) != 1 {
		t.Errorf("expected 1 diagnostic, got %d", len(msg.Diagnostics))
	}
}
