package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTimeoutError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantStr string
		wantIs  []error
		wantAs  interface{}
	}{
		{
			name:    "agent timeout",
			err:     WrapTimeout(TimeoutSourceAgent, 3*time.Minute, nil),
			wantStr: "timeout: agent after 3m0s",
			wantIs:  []error{ErrTimeout, context.DeadlineExceeded},
			wantAs:  &TimeoutError{},
		},
		{
			name:    "HTTP timeout with provider",
			err:     WrapHTTPTimeout(ProviderOpenAI, 5*time.Minute, nil),
			wantStr: "timeout: http after 5m0s provider=openai",
			wantIs:  []error{ErrTimeout, context.DeadlineExceeded},
			wantAs:  &TimeoutError{},
		},
		{
			name:    "tool timeout",
			err:     WrapToolTimeout("bash", 30*time.Second, nil),
			wantStr: "timeout: tool after 30s tool=bash",
			wantIs:  []error{ErrTimeout, context.DeadlineExceeded},
			wantAs:  &TimeoutError{},
		},
		{
			name:    "timeout with custom cause",
			err:     WrapTimeout(TimeoutSourceAgent, 1*time.Minute, errors.New("custom error")),
			wantStr: "timeout: agent after 1m0s cause=custom error",
			wantIs:  []error{ErrTimeout, context.DeadlineExceeded},
			wantAs:  &TimeoutError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test Error() output
			if got := tt.err.Error(); got != tt.wantStr {
				t.Errorf("Error() = %q, want %q", got, tt.wantStr)
			}

			// Test errors.Is
			for _, target := range tt.wantIs {
				if !errors.Is(tt.err, target) {
					t.Errorf("errors.Is(err, %v) = false, want true", target)
				}
			}

			// Test errors.As
			if tt.wantAs != nil {
				var timeoutErr *TimeoutError
				if !errors.As(tt.err, &timeoutErr) {
					t.Errorf("errors.As(err, &TimeoutError) = false, want true")
				}
			}
		})
	}
}

func TestTimeoutErrorUnwrap(t *testing.T) {
	cause := errors.New("underlying error")
	err := WrapTimeout(TimeoutSourceAgent, 1*time.Minute, cause)

	// Test Unwrap returns the cause
	if unwrapped := errors.Unwrap(err); unwrapped != cause {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, cause)
	}
}

func TestTimeoutErrorFields(t *testing.T) {
	err := WrapHTTPTimeout(ProviderAnthropic, 2*time.Minute, nil)

	var timeoutErr *TimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatal("errors.As failed")
	}

	if timeoutErr.Source != TimeoutSourceHTTP {
		t.Errorf("Source = %s, want %s", timeoutErr.Source, TimeoutSourceHTTP)
	}
	if timeoutErr.Duration != 2*time.Minute {
		t.Errorf("Duration = %v, want %v", timeoutErr.Duration, 2*time.Minute)
	}
	if timeoutErr.Provider != ProviderAnthropic {
		t.Errorf("Provider = %s, want %s", timeoutErr.Provider, ProviderAnthropic)
	}
}

func TestIsRetryableErrorWithTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "context.DeadlineExceeded is not retryable",
			err:  context.DeadlineExceeded,
			want: false,
		},
		{
			name: "ErrTimeout is not retryable",
			err:  ErrTimeout,
			want: false,
		},
		{
			name: "wrapped timeout is not retryable",
			err:  WrapTimeout(TimeoutSourceAgent, 1*time.Minute, nil),
			want: false,
		},
		{
			name: "wrapped HTTP timeout is not retryable",
			err:  WrapHTTPTimeout(ProviderOpenAI, 5*time.Minute, nil),
			want: false,
		},
		{
			name: "wrapped tool timeout is not retryable",
			err:  WrapToolTimeout("bash", 30*time.Second, nil),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryableError(tt.err); got != tt.want {
				t.Errorf("IsRetryableError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
