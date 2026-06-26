package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"rate-limit sentinel", ErrRateLimit, true},
		{"server sentinel", ErrServer, true},
		{"network sentinel", ErrNetwork, true},
		{"auth sentinel", ErrAuth, false},
		{"aborted sentinel", ErrAborted, false},
		{"context cancel", context.Canceled, false},
		{"overflow", &OverflowError{Provider: "openai"}, false},
		{"string 429", errors.New("429: too many requests"), true},
		{"string socket", errors.New("read tcp: connection reset by peer"), true},
		{"string timeout", errors.New("context deadline exceeded"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryableError(tt.err); got != tt.want {
				t.Errorf("IsRetryableError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRetrySuccessFirstAttempt(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), DefaultRetryConfig(), func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestRetrySuccessAfterRetries(t *testing.T) {
	calls := 0
	cfg := RetryConfig{
		Enabled:    true,
		MaxRetries: 3,
		BaseDelay:  time.Millisecond,
		Multiplier: 2,
		OnRetry:    func(attempt int, delay time.Duration, err error) {},
	}
	err := Retry(context.Background(), cfg, func() error {
		calls++
		if calls < 3 {
			return &ServerError{Provider: "openai", StatusCode: 500}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestRetryExhausts(t *testing.T) {
	calls := 0
	cfg := RetryConfig{
		Enabled:    true,
		MaxRetries: 2,
		BaseDelay:  time.Millisecond,
		Multiplier: 2,
	}
	err := Retry(context.Background(), cfg, func() error {
		calls++
		return &ServerError{Provider: "openai", StatusCode: 500}
	})
	if err == nil {
		t.Fatal("expected error after exhaustion")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls (1 initial + 2 retries), got %d", calls)
	}
}

func TestRetryNonRetryableReturnsImmediately(t *testing.T) {
	calls := 0
	cfg := RetryConfig{Enabled: true, MaxRetries: 5, BaseDelay: time.Millisecond}
	err := Retry(context.Background(), cfg, func() error {
		calls++
		return &AuthError{Provider: "openai"}
	})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestRetryContextCancellation(t *testing.T) {
	cfg := RetryConfig{Enabled: true, MaxRetries: 3, BaseDelay: 100 * time.Millisecond, Multiplier: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := Retry(ctx, cfg, func() error {
		calls++
		return &ServerError{Provider: "openai", StatusCode: 500}
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestRetryDisabledCallsOnce(t *testing.T) {
	calls := 0
	cfg := RetryConfig{Enabled: false}
	err := Retry(context.Background(), cfg, func() error {
		calls++
		return &ServerError{Provider: "openai", StatusCode: 500}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}
