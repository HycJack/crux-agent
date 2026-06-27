// Package core provides typed errors and HTTP classification for AI provider
// responses.
//
// Errors follow a two-layer model:
//
//   - Sentinel values (ErrOverflow, ErrAuth, ...) for cheap equality checks
//     via errors.Is.
//   - Typed structs (OverflowError, AuthError, ...) carrying rich context
//     (provider, HTTP status, retry-after hint, ...). All types implement
//     Is/Unwrap so they match the corresponding sentinel.
//
// Reference: pi-mono packages/ai/src/utils/errors.ts.
package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Sentinel error reasons used by classification helpers. They are exposed as
// plain error values so callers can use errors.Is.
var (
	// ErrOverflow is a generic context-overflow marker. Prefer *OverflowError
	// for richer context.
	ErrOverflow = errors.New("context overflow")

	// ErrAborted indicates the caller cancelled the operation.
	ErrAborted = errors.New("operation aborted")

	// ErrAuth indicates an authentication / authorization failure.
	ErrAuth = errors.New("authentication failed")

	// ErrRateLimit indicates the provider rate-limited the request.
	ErrRateLimit = errors.New("rate limited")

	// ErrServer indicates a transient server-side failure.
	ErrServer = errors.New("server error")

	// ErrNetwork indicates a transport / connection failure.
	ErrNetwork = errors.New("network error")

	// ErrTimeout indicates a timeout occurred. Use *HTTPTimeoutError for
	// richer context about the timeout source.
	ErrTimeout = errors.New("timeout")
)

// OverflowError is raised when a request exceeds the provider's context
// window. Detected via errors.As(err, &oe) and inspectable for the numeric
// context.
type OverflowError struct {
	Provider      KnownProvider
	Message       string
	ContextWindow int
	Usage         int
}

func (e *OverflowError) Error() string {
	switch {
	case e.Message != "":
		return fmt.Sprintf("%s: context overflow: %s", e.Provider, e.Message)
	case e.ContextWindow > 0 && e.Usage > 0:
		return fmt.Sprintf("%s: context overflow: usage %d > window %d", e.Provider, e.Usage, e.ContextWindow)
	default:
		return fmt.Sprintf("%s: context overflow", e.Provider)
	}
}

// Is allows errors.Is to match ErrOverflow.
func (e *OverflowError) Is(target error) bool { return target == ErrOverflow }

// Unwrap allows errors.Is to recognize *OverflowError.
func (e *OverflowError) Unwrap() error { return ErrOverflow }

// CompactionCancelledError signals an explicit compaction abort. Callers can
// discriminate cancellation from other failures via errors.As rather than
// string matching.
type CompactionCancelledError struct {
	Reason string
}

func (e *CompactionCancelledError) Error() string {
	if e.Reason == "" {
		return "compaction cancelled"
	}
	return "compaction cancelled: " + e.Reason
}

// AbortError signals the consumer cancelled the in-flight operation.
type AbortError struct {
	Cause error
}

func (e *AbortError) Error() string {
	if e.Cause == nil {
		return "operation aborted"
	}
	return "operation aborted: " + e.Cause.Error()
}

// Unwrap returns the underlying cause.
func (e *AbortError) Unwrap() error { return e.Cause }

// Is allows errors.Is to match ErrAborted or context.Canceled.
func (e *AbortError) Is(t error) bool {
	return t == ErrAborted || t == context.Canceled
}

// AuthError wraps a 401/403 from the provider.
type AuthError struct {
	Provider KnownProvider
	Cause    error
}

func (e *AuthError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: authentication failed", e.Provider)
	}
	return fmt.Sprintf("%s: authentication failed: %v", e.Provider, e.Cause)
}

func (e *AuthError) Unwrap() error { return e.Cause }

// Is allows errors.Is to match ErrAuth.
func (e *AuthError) Is(t error) bool { return t == ErrAuth }

// RateLimitError wraps a 429 with optional retry-after hint.
type RateLimitError struct {
	Provider   KnownProvider
	RetryAfter time.Duration
	Cause      error
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s: rate limited (retry after %s)", e.Provider, e.RetryAfter)
	}
	if e.Cause == nil {
		return fmt.Sprintf("%s: rate limited", e.Provider)
	}
	return fmt.Sprintf("%s: rate limited: %v", e.Provider, e.Cause)
}

func (e *RateLimitError) Unwrap() error { return e.Cause }

// Is allows errors.Is to match ErrRateLimit.
func (e *RateLimitError) Is(t error) bool { return t == ErrRateLimit }

// ServerError wraps a 5xx response.
type ServerError struct {
	Provider   KnownProvider
	StatusCode int
	Cause      error
}

func (e *ServerError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: server error %d", e.Provider, e.StatusCode)
	}
	return fmt.Sprintf("%s: server error %d: %v", e.Provider, e.StatusCode, e.Cause)
}

func (e *ServerError) Unwrap() error { return e.Cause }

// Is allows errors.Is to match ErrServer.
func (e *ServerError) Is(t error) bool { return t == ErrServer }

// NetworkError wraps a transport-level failure (DNS, connect, EOF, etc.).
type NetworkError struct {
	Provider KnownProvider
	Cause    error
}

func (e *NetworkError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: network error", e.Provider)
	}
	return fmt.Sprintf("%s: network error: %v", e.Provider, e.Cause)
}

func (e *NetworkError) Unwrap() error { return e.Cause }

// Is allows errors.Is to match ErrNetwork.
func (e *NetworkError) Is(t error) bool { return t == ErrNetwork }

// TimeoutSource identifies where a timeout originated.
type TimeoutSource string

const (
	// TimeoutSourceAgent indicates the entire agent run exceeded its timeout.
	TimeoutSourceAgent TimeoutSource = "agent"

	// TimeoutSourceHTTP indicates an HTTP request to the LLM provider timed out.
	TimeoutSourceHTTP TimeoutSource = "http"

	// TimeoutSourceTool indicates a tool execution (e.g., bash) timed out.
	TimeoutSourceTool TimeoutSource = "tool"
)

// HTTPTimeoutError wraps a context.DeadlineExceeded with source information.
//
// Named HTTPTimeoutError (not TimeoutError) to avoid clashing with the
// stream-idle TimeoutError in timeouts.go.
type HTTPTimeoutError struct {
	Source   TimeoutSource
	Duration time.Duration
	Provider KnownProvider
	ToolName string
	Cause    error
}

func (e *HTTPTimeoutError) Error() string {
	var parts []string
	parts = append(parts, string(e.Source))

	if e.Duration > 0 {
		parts = append(parts, fmt.Sprintf("after %s", e.Duration))
	}
	if e.Provider != "" {
		parts = append(parts, fmt.Sprintf("provider=%s", e.Provider))
	}
	if e.ToolName != "" {
		parts = append(parts, fmt.Sprintf("tool=%s", e.ToolName))
	}
	if e.Cause != nil && e.Cause != context.DeadlineExceeded {
		parts = append(parts, fmt.Sprintf("cause=%v", e.Cause))
	}

	return fmt.Sprintf("timeout: %s", strings.Join(parts, " "))
}

// Unwrap returns the underlying cause.
func (e *HTTPTimeoutError) Unwrap() error {
	if e.Cause != nil {
		return e.Cause
	}
	return ErrTimeout
}

// Is allows errors.Is to match ErrTimeout or context.DeadlineExceeded.
func (e *HTTPTimeoutError) Is(t error) bool {
	return t == ErrTimeout || t == context.DeadlineExceeded
}

// --- Error predicates (B2: typed-error shorthand) ------------------------------
//
// These are thin wrappers over errors.Is(err, ErrXxx) for sites that
// branch on error category. They exist for readability and for symmetry
// with pi-mono's `isRetryableProviderError` / similar helpers.
// IsAuthError lives in retry.go to keep retry-specific predicates grouped.

// IsRateLimitError reports whether err is a rate-limit / quota response
// (HTTP 408/429). Auto-retry may apply with Retry-After honoring.
func IsRateLimitError(err error) bool {
	return errors.Is(err, ErrRateLimit)
}

// IsServerError reports whether err is a 5xx server-side failure.
// Auto-retry applies with exponential backoff.
func IsServerError(err error) bool {
	return errors.Is(err, ErrServer)
}

// IsAbortedError reports whether err indicates the caller cancelled the
// in-flight operation. Auto-retry MUST NOT apply.
func IsAbortedError(err error) bool {
	return errors.Is(err, ErrAborted)
}

// ClassifyHTTPError maps an HTTP error response to a typed error. The body
// is the error payload returned by the provider. If no classification
// applies, nil is returned and the caller is expected to surface the raw
// error.
func ClassifyHTTPError(provider KnownProvider, status int, body string) error {
	switch {
	case status == http.StatusRequestTimeout, status == http.StatusTooManyRequests:
		return &RateLimitError{Provider: provider, Cause: fmt.Errorf("status %d: %s", status, trimBody(body))}
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return &AuthError{Provider: provider, Cause: fmt.Errorf("status %d: %s", status, trimBody(body))}
	case status >= 500 && status <= 599:
		return &ServerError{Provider: provider, StatusCode: status, Cause: fmt.Errorf("%s", trimBody(body))}
	case status == http.StatusRequestEntityTooLarge:
		return &OverflowError{Provider: provider, Message: trimBody(body)}
	default:
		return nil
	}
}

// trimBody truncates the response body for error messages.
func trimBody(body string) string {
	body = strings.TrimSpace(body)
	const max = 500
	if len(body) > max {
		return body[:max] + "..."
	}
	return body
}

// WrapTimeout wraps a context.DeadlineExceeded error with source information.
func WrapTimeout(source TimeoutSource, duration time.Duration, cause error) error {
	if cause == nil {
		cause = context.DeadlineExceeded
	}
	return &HTTPTimeoutError{
		Source:   source,
		Duration: duration,
		Cause:    cause,
	}
}

// WrapHTTPTimeout wraps an HTTP request timeout with provider information.
func WrapHTTPTimeout(provider KnownProvider, duration time.Duration, cause error) error {
	if cause == nil {
		cause = context.DeadlineExceeded
	}
	return &HTTPTimeoutError{
		Source:   TimeoutSourceHTTP,
		Duration: duration,
		Provider: provider,
		Cause:    cause,
	}
}

// WrapToolTimeout wraps a tool execution timeout with tool name.
func WrapToolTimeout(toolName string, duration time.Duration, cause error) error {
	if cause == nil {
		cause = context.DeadlineExceeded
	}
	return &HTTPTimeoutError{
		Source:   TimeoutSourceTool,
		Duration: duration,
		ToolName: toolName,
		Cause:    cause,
	}
}
