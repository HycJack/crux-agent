package core

import (
	"context"
	"errors"
	"testing"
)

func TestOverflowErrorMessages(t *testing.T) {
	tests := []struct {
		name string
		err  *OverflowError
		want string
	}{
		{"message only", &OverflowError{Provider: "openai", Message: "context length exceeded"}, "openai: context overflow: context length exceeded"},
		{"window only", &OverflowError{Provider: "glm", ContextWindow: 200000, Usage: 250000}, "glm: context overflow: usage 250000 > window 200000"},
		{"bare", &OverflowError{Provider: "kimi"}, "kimi: context overflow"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if !errors.Is(tt.err, ErrOverflow) {
				t.Errorf("errors.Is(_, ErrOverflow) = false")
			}
		})
	}
}

func TestAbortError(t *testing.T) {
	ae := &AbortError{Cause: errors.New("user pressed Ctrl-C")}
	if !errors.Is(ae, ErrAborted) {
		t.Error("expected errors.Is(ErrAborted)")
	}
	if !errors.Is(ae, context.Canceled) {
		t.Error("expected errors.Is(_, context.Canceled)")
	}
	if got := ae.Error(); got != "operation aborted: user pressed Ctrl-C" {
		t.Errorf("Error() = %q", got)
	}
}

func TestClassifyHTTPError(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   error
	}{
		{401, "unauthorized", ErrAuth},
		{403, "forbidden", ErrAuth},
		{429, "rate", ErrRateLimit},
		{408, "timeout", ErrRateLimit},
		{500, "boom", ErrServer},
		{503, "overloaded", ErrServer},
		{413, "request too large", ErrOverflow},
		{400, "bad json", nil},
		{418, "teapot", nil},
	}
	for _, tt := range tests {
		err := ClassifyHTTPError("openai", tt.status, tt.body)
		if tt.want == nil {
			if err != nil {
				t.Errorf("status %d: expected nil, got %v", tt.status, err)
			}
			continue
		}
		if !errors.Is(err, tt.want) {
			t.Errorf("status %d: errors.Is(%v, %v) = false", tt.status, err, tt.want)
		}
	}
}

func TestRateLimitErrorMessage(t *testing.T) {
	rl := &RateLimitError{Provider: "openai", RetryAfter: 5 * 1_000_000_000} // 5s in nanoseconds
	if got := rl.Error(); got == "" {
		t.Errorf("expected non-empty message")
	}
}
