// // Package core provides AI provider abstractions.
// package core

// import (
// 	"context"
// 	"errors"
// 	"fmt"
// 	"net/http"
// 	"regexp"
// 	"strings"
// 	"time"
// )

// // ============================================================================
// // Error Classification System
// //
// // Reference: oh-my-pi (packages/ai/src/utils/retry.ts) design:
// //   - Errors are classified by source type, not per-provider retry logic
// //   - Retry decisions are decoupled from provider implementations via interfaces
// // ============================================================================

// // ErrorKind represents a classified error category.
// type ErrorKind int

// const (
// 	ErrorKindUnknown       ErrorKind = iota
// 	ErrorKindRateLimit               // 429 - rate limiting
// 	ErrorKindOverloaded              // 500/503 - server overloaded
// 	ErrorKindAuth                    // 401/403 - authentication/authorization failure
// 	ErrorKindModelNotFound           // 404 - model not found
// 	ErrorKindContextLength           // context length exceeded (400+ special codes)
// 	ErrorKindTimeout                 // request timeout
// 	ErrorKindBadRequest              // 400 (non-retryable)
// 	ErrorKindServerError             // 500 (non-overloaded, non-retryable)
// )

// // String returns the string representation of the error kind.
// func (k ErrorKind) String() string {
// 	switch k {
// 	case ErrorKindRateLimit:
// 		return "rate_limit"
// 	case ErrorKindOverloaded:
// 		return "overloaded"
// 	case ErrorKindAuth:
// 		return "auth"
// 	case ErrorKindModelNotFound:
// 		return "model_not_found"
// 	case ErrorKindContextLength:
// 		return "context_length"
// 	case ErrorKindTimeout:
// 		return "timeout"
// 	case ErrorKindBadRequest:
// 		return "bad_request"
// 	case ErrorKindServerError:
// 		return "server_error"
// 	default:
// 		return "unknown"
// 	}
// }

// // IsRetryable returns whether this error kind can be retried.
// func (k ErrorKind) IsRetryable() bool {
// 	switch k {
// 	case ErrorKindRateLimit, ErrorKindOverloaded, ErrorKindTimeout:
// 		return true
// 	default:
// 		return false
// 	}
// }

// // ProviderError carries classified error information.
// type ProviderError struct {
// 	Kind     ErrorKind
// 	Message  string
// 	HTTPCode int
// 	Wrapped  error
// }

// func (e *ProviderError) Error() string {
// 	if e.Wrapped != nil {
// 		return fmt.Sprintf("[%s] %s: %v", e.Kind, e.Message, e.Wrapped)
// 	}
// 	return fmt.Sprintf("[%s] %s", e.Kind, e.Message)
// }

// func (e *ProviderError) Unwrap() error {
// 	return e.Wrapped
// }

// // NewProviderError creates a classified provider error.
// func NewProviderError(httpCode int, message string, wrapped error) *ProviderError {
// 	kind := classifyHTTPCode(httpCode)
// 	return &ProviderError{
// 		Kind:     kind,
// 		Message:  message,
// 		HTTPCode: httpCode,
// 		Wrapped:  wrapped,
// 	}
// }

// // IsRetryableProviderError checks if an error is a classified retryable error.
// func IsRetryableProviderError(err error) bool {
// 	var pe *ProviderError
// 	if errors.As(err, &pe) {
// 		return pe.Kind.IsRetryable()
// 	}
// 	return false
// }

// // ExtractErrorKind extracts the error kind from an error.
// func ExtractErrorKind(err error) ErrorKind {
// 	var pe *ProviderError
// 	if errors.As(err, &pe) {
// 		return pe.Kind
// 	}
// 	return ErrorKindUnknown
// }

// // classifyHTTPCode returns an ErrorKind based on the HTTP status code.
// func classifyHTTPCode(code int) ErrorKind {
// 	switch {
// 	case code == http.StatusTooManyRequests:
// 		return ErrorKindRateLimit
// 	case code == http.StatusUnauthorized, code == http.StatusForbidden:
// 		return ErrorKindAuth
// 	case code == http.StatusNotFound:
// 		return ErrorKindModelNotFound
// 	case code == http.StatusBadRequest:
// 		return ErrorKindBadRequest
// 	case code == http.StatusServiceUnavailable || code == http.StatusBadGateway || code == http.StatusGatewayTimeout:
// 		return ErrorKindOverloaded
// 	case code >= 500:
// 		return ErrorKindServerError
// 	default:
// 		return ErrorKindUnknown
// 	}
// }

// // Retryable is an interface that marks an error as retryable.
// type Retryable interface {
// 	Retryable() bool
// }

// // ============================================================================
// // Composable Retry Decorator
// //
// // Reference: oh-my-pi's callWithCopilotModelRetry() design:
// //   - WrapRetry does not intrude into provider code
// //   - Supports layered retry strategies
// // ============================================================================

// // RetryPolicy defines the retry strategy.
// type RetryPolicy struct {
// 	MaxAttempts    int
// 	InitialBackoff time.Duration
// 	MaxBackoff     time.Duration
// 	BackoffFactor  float64
// 	// RateLimitBaseBackoff is the starting backoff for HTTP 429 responses.
// 	// Unlike regular backoff (which scales with BackoffFactor), the rate-limit
// 	// wait is fixed (subject to MaxBackoff) so we don't exponentially drift
// 	// past a server's Retry-After window. Default: 5s.
// 	RateLimitBaseBackoff time.Duration
// 	ShouldRetry          func(error) bool
// 	OnRetry              func(attempt int, err error)
// 	// RetryAfter returns the wait duration for a given error, when the
// 	// underlying provider supplied a Retry-After header or equivalent.
// 	// When non-zero it overrides RateLimitBaseBackoff for that error.
// 	RetryAfter func(error) time.Duration
// }

// // DefaultRetryPolicy returns the default retry policy.
// func DefaultRetryPolicy() RetryPolicy {
// 	return RetryPolicy{
// 		MaxAttempts:          3,
// 		InitialBackoff:       1 * time.Second,
// 		MaxBackoff:           30 * time.Second,
// 		BackoffFactor:        2.0,
// 		RateLimitBaseBackoff: 5 * time.Second,
// 		ShouldRetry:          IsRetryableProviderError,
// 	}
// }

// // WrapRetry wraps a function with automatic retry according to the RetryPolicy.
// func WrapRetry[T any](ctx context.Context, policy RetryPolicy, fn func(context.Context) (T, error)) (T, error) {
// 	if policy.MaxAttempts <= 0 {
// 		policy.MaxAttempts = 3
// 	}
// 	if policy.InitialBackoff <= 0 {
// 		policy.InitialBackoff = 1 * time.Second
// 	}
// 	if policy.MaxBackoff <= 0 {
// 		policy.MaxBackoff = 30 * time.Second
// 	}
// 	if policy.BackoffFactor <= 0 {
// 		policy.BackoffFactor = 2.0
// 	}
// 	if policy.RateLimitBaseBackoff <= 0 {
// 		policy.RateLimitBaseBackoff = 5 * time.Second
// 	}
// 	if policy.ShouldRetry == nil {
// 		policy.ShouldRetry = IsRetryableProviderError
// 	}

// 	var lastErr error
// 	backoff := policy.InitialBackoff

// 	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
// 		if ctx.Err() != nil {
// 			var zero T
// 			return zero, ctx.Err()
// 		}

// 		result, err := fn(ctx)
// 		if err == nil {
// 			return result, nil
// 		}
// 		lastErr = err

// 		if !policy.ShouldRetry(err) {
// 			break
// 		}
// 		if attempt == policy.MaxAttempts-1 {
// 			break
// 		}
// 		if policy.OnRetry != nil {
// 			policy.OnRetry(attempt+1, err)
// 		}

// 		waitTime := backoff
// 		isRateLimit := ExtractErrorKind(err) == ErrorKindRateLimit
// 		if isRateLimit {
// 			// Honor a server-supplied Retry-After hint when available,
// 			// otherwise fall back to the rate-limit base (capped by MaxBackoff).
// 			if policy.RetryAfter != nil {
// 				if hint := policy.RetryAfter(err); hint > 0 {
// 					waitTime = hint
// 				} else {
// 					waitTime = policy.RateLimitBaseBackoff
// 				}
// 			} else {
// 				waitTime = policy.RateLimitBaseBackoff
// 			}
// 			if waitTime > policy.MaxBackoff {
// 				waitTime = policy.MaxBackoff
// 			}
// 		}

// 		select {
// 		case <-ctx.Done():
// 			var zero T
// 			return zero, ctx.Err()
// 		case <-time.After(waitTime):
// 		}

// 		// Regular (non-rate-limit) backoff is exponential.
// 		// Rate-limit waits stay fixed at RateLimitBaseBackoff to avoid
// 		// runaway delays when MaxBackoff is large.
// 		if !isRateLimit {
// 			backoff = time.Duration(float64(backoff) * policy.BackoffFactor)
// 			if backoff > policy.MaxBackoff {
// 				backoff = policy.MaxBackoff
// 			}
// 		}
// 	}

// 	var zero T
// 	return zero, lastErr
// }

// // ============================================================================
// // Provider-Specific Error Helpers
// // ============================================================================

// // IsHTTPCodeError checks if an error originates from a specific HTTP status code.
// func IsHTTPCodeError(err error, code int) bool {
// 	var pe *ProviderError
// 	if errors.As(err, &pe) {
// 		return pe.HTTPCode == code
// 	}
// 	return false
// }

// // IsTransient400Error checks if an error is a specific transient "400" error from an API.
// func IsTransient400Error(err error, transientCodes ...string) bool {
// 	if !IsHTTPCodeError(err, http.StatusBadRequest) {
// 		return false
// 	}
// 	if len(transientCodes) == 0 {
// 		return false
// 	}
// 	msg := err.Error()
// 	for _, code := range transientCodes {
// 		if strings.Contains(msg, code) {
// 			return true
// 		}
// 	}
// 	return false
// }

// // NewContextError constructs a context-length error carrying classification.
// func NewContextError(message string, wrapped error) *ProviderError {
// 	return &ProviderError{
// 		Kind:    ErrorKindContextLength,
// 		Message: message,
// 		Wrapped: wrapped,
// 	}
// }

// // ============================================================================
// // Retryable Error Classifier (regex-based)
// //
// // Reference: pi-mono packages/ai/src/utils/retry.ts (isRetryableAssistantError).
// // Some errors look transient on the wire but are actually non-retryable
// // (subscription/usage limits, billing). We classify BEFORE retry so callers
// // don't burn their budget on quota exhaustion.
// // ============================================================================

// // nonRetryableLimitPatterns matches non-retryable usage-limit / billing errors.
// // These are explicit account/subscription limits, not transient throttles.
// var nonRetryableLimitPatterns = []*regexp.Regexp{
// 	regexp.MustCompile(`GoUsageLimitError`),
// 	regexp.MustCompile(`FreeUsageLimitError`),
// 	regexp.MustCompile(`Monthly usage limit reached`),
// 	regexp.MustCompile(`available balance`),
// 	regexp.MustCompile(`insufficient_quota`),
// 	regexp.MustCompile(`out of budget`),
// 	regexp.MustCompile(`quota exceeded`),
// 	regexp.MustCompile(`(?i)billing`),
// }

// // retryableProviderPatterns matches transient retryable failures.
// // Curated from pi-mono's RETRYABLE_PROVIDER_ERROR_PATTERN.
// var retryableProviderPatterns = []*regexp.Regexp{
// 	// Generic load / status / server-side failures
// 	regexp.MustCompile(`(?i)overloaded`),
// 	regexp.MustCompile(`(?i)rate.?limit`),
// 	regexp.MustCompile(`(?i)too many requests`),
// 	regexp.MustCompile(`(?i)429`),
// 	regexp.MustCompile(`(?i)500`),
// 	regexp.MustCompile(`(?i)502`),
// 	regexp.MustCompile(`(?i)503`),
// 	regexp.MustCompile(`(?i)504`),
// 	regexp.MustCompile(`(?i)service.?unavailable`),
// 	regexp.MustCompile(`(?i)server.?error`),
// 	regexp.MustCompile(`(?i)internal.?error`),
// 	regexp.MustCompile(`(?i)provider.?returned.?error`),
// 	// Network / transport
// 	regexp.MustCompile(`(?i)network.?error`),
// 	regexp.MustCompile(`(?i)connection.?error`),
// 	regexp.MustCompile(`(?i)connection.?refused`),
// 	regexp.MustCompile(`(?i)connection.?lost`),
// 	regexp.MustCompile(`(?i)other side closed`),
// 	regexp.MustCompile(`(?i)fetch failed`),
// 	regexp.MustCompile(`(?i)upstream.?connect`),
// 	regexp.MustCompile(`(?i)reset before headers`),
// 	regexp.MustCompile(`(?i)socket hang up`),
// 	regexp.MustCompile(`(?i)timed? out`),
// 	regexp.MustCompile(`(?i)timeout`),
// 	regexp.MustCompile(`(?i)terminated`),
// 	// WebSocket transports
// 	regexp.MustCompile(`(?i)websocket.?closed`),
// 	regexp.MustCompile(`(?i)websocket.?error`),
// 	// Stream-ended-without-message events
// 	regexp.MustCompile(`(?i)ended without`),
// 	regexp.MustCompile(`(?i)stream ended before message_stop`),
// 	regexp.MustCompile(`(?i)http2 request did not get a response`),
// 	// Retry guidance emitted mid-stream
// 	regexp.MustCompile(`(?i)retry delay`),
// 	regexp.MustCompile(`(?i)you can retry your request`),
// 	regexp.MustCompile(`(?i)try your request again`),
// 	regexp.MustCompile(`(?i)please retry your request`),
// }

// // IsRetryableAssistantError reports whether a failed assistant message
// // looks like a transient provider/transport error suitable for restart.
// //
// // This is a classifier, not a retry policy. Callers should still apply
// // their own budget, backoff, and reporting before restarting the turn.
// // Use IsContextOverflowMessage first to avoid retrying context overflow.
// func IsRetryableAssistantError(msg *AssistantMessage) bool {
// 	if msg == nil {
// 		return false
// 	}
// 	if msg.StopReason != StopError || msg.ErrorMessage == "" {
// 		return false
// 	}
// 	// Non-retryable limit/billing errors short-circuit.
// 	for _, p := range nonRetryableLimitPatterns {
// 		if p.MatchString(msg.ErrorMessage) {
// 			return false
// 		}
// 	}
// 	for _, p := range retryableProviderPatterns {
// 		if p.MatchString(msg.ErrorMessage) {
// 			return true
// 		}
// 	}
// 	return false
// }
