package memory

import "fmt"

// ErrorCode represents the type of memory error.
type ErrorCode string

const (
	ErrCodeConnection ErrorCode = "CONNECTION_ERROR"
	ErrCodeTimeout    ErrorCode = "TIMEOUT"
	ErrCodeRateLimit  ErrorCode = "RATE_LIMIT"
	ErrCodeAuth       ErrorCode = "AUTH_ERROR"
	ErrCodeNotFound   ErrorCode = "NOT_FOUND"
	ErrCodeInternal   ErrorCode = "INTERNAL_ERROR"
)

// MemoryError is a structured error for memory operations.
type MemoryError struct {
	Code     ErrorCode
	Message  string
	Err      error
	Provider string
}

func (e *MemoryError) Error() string {
	return fmt.Sprintf("[%s] %s: %s", e.Provider, e.Code, e.Message)
}

func (e *MemoryError) Unwrap() error { return e.Err }

// IsRetryable returns true if the error is transient and can be retried.
func (e *MemoryError) IsRetryable() bool {
	switch e.Code {
	case ErrCodeConnection, ErrCodeTimeout, ErrCodeRateLimit:
		return true
	default:
		return false
	}
}
