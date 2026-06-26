package core

import (
	"fmt"
	"time"
)

// DiagnosticErrorInfo captures the structured fields of a Go error suitable
// for serialization on the wire.
//
// Reference: pi-mono packages/ai/src/utils/diagnostics.ts (DiagnosticErrorInfo).
type DiagnosticErrorInfo struct {
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
	Code    string `json:"code,omitempty"`
}

// Diagnostic is a structured event attached to an AssistantMessage when a
// runtime issue (retry, recovered failure, redacted payload, etc.) occurs
// during streaming.
//
// Reference: pi-mono packages/ai/src/utils/diagnostics.ts (AssistantMessageDiagnostic).
type Diagnostic struct {
	Type      string         `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Error     *DiagnosticErrorInfo `json:"error,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// FormatThrownValue converts an arbitrary panic-like value into a string.
// Mirrors pi-mono's formatThrownValue: Error -> message, string -> as-is,
// other -> fmt.Sprintf("%v", value).
func FormatThrownValue(value any) string {
	switch v := value.(type) {
	case error:
		if v == nil {
			return ""
		}
		if v.Error() != "" {
			return v.Error()
		}
		return fmt.Sprintf("%T", v)
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ExtractDiagnosticError converts an unknown error value into a
// DiagnosticErrorInfo with name/message/stack/code fields when available.
func ExtractDiagnosticError(err error) *DiagnosticErrorInfo {
	if err == nil {
		return nil
	}
	info := &DiagnosticErrorInfo{
		Name:    fmt.Sprintf("%T", err),
		Message: FormatThrownValue(err),
	}
	type stackTracer interface{ StackTrace() string }
	if s, ok := err.(stackTracer); ok {
		info.Stack = s.StackTrace()
	}
	type coder interface{ Code() string }
	if c, ok := err.(coder); ok {
		info.Code = c.Code()
	}
	return info
}

// NewDiagnostic creates a Diagnostic with the current timestamp.
func NewDiagnostic(diagType string, err error, details map[string]any) Diagnostic {
	d := Diagnostic{
		Type:      diagType,
		Timestamp: time.Now(),
		Details:   details,
	}
	if err != nil {
		d.Error = ExtractDiagnosticError(err)
	}
	return d
}

// AppendDiagnostic appends a diagnostic to the message in-place.
// Idempotent w.r.t. nil: nil diagnostic is ignored.
func AppendDiagnostic(msg *AssistantMessage, diag Diagnostic) {
	if msg == nil {
		return
	}
	msg.Diagnostics = append(msg.Diagnostics, diag)
}

// AppendDiagnosticFn is a convenience for the "diag as a func value" pattern.
// Useful with onError handlers: onError: func(err) { AppendDiagnosticFn(msg, "stream", err) }
func AppendDiagnosticFn(msg *AssistantMessage, diagType string, err error) {
	AppendDiagnostic(msg, NewDiagnostic(diagType, err, nil))
}