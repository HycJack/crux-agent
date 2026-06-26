package core

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// ============================================================================
// 错误分类测试
// ============================================================================

func TestClassifyHTTPCode(t *testing.T) {
	tests := []struct {
		code int
		want ErrorKind
	}{
		{200, ErrorKindUnknown},
		{400, ErrorKindBadRequest},
		{401, ErrorKindAuth},
		{403, ErrorKindAuth},
		{404, ErrorKindModelNotFound},
		{429, ErrorKindRateLimit},
		{500, ErrorKindServerError},
		{502, ErrorKindOverloaded},
		{503, ErrorKindOverloaded},
		{504, ErrorKindOverloaded},
	}

	for _, tt := range tests {
		pe := NewProviderError(tt.code, "test", nil)
		if pe.Kind != tt.want {
			t.Errorf("classifyHTTPCode(%d) = %v, want %v", tt.code, pe.Kind, tt.want)
		}
	}
}

func TestErrorKindIsRetryable(t *testing.T) {
	tests := []struct {
		kind ErrorKind
		want bool
	}{
		{ErrorKindRateLimit, true},
		{ErrorKindOverloaded, true},
		{ErrorKindTimeout, true},
		{ErrorKindAuth, false},
		{ErrorKindBadRequest, false},
		{ErrorKindServerError, false},
	}
	for _, tt := range tests {
		if got := tt.kind.IsRetryable(); got != tt.want {
			t.Errorf("%v.IsRetryable() = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

func TestIsRetryableAssistantError_Transient(t *testing.T) {
	cases := []string{
		"provider overloaded, try again",
		"rate limit exceeded",
		"Too Many Requests (429)",
		"server returned 503",
		"service unavailable",
		"connection refused",
		"fetch failed",
		"upstream connect error",
		"socket hang up",
		"timed out waiting for response",
		"websocket closed unexpectedly",
		"provider returned error",
		"you can retry your request",
		"Anthropic stream ended before message_stop",
	}
	for _, c := range cases {
		msg := &AssistantMessage{StopReason: StopError, ErrorMessage: c}
		if !IsRetryableAssistantError(msg) {
			t.Errorf("expected retryable: %q", c)
		}
	}
}

func TestIsRetryableAssistantError_NonRetryableLimits(t *testing.T) {
	cases := []string{
		"GoUsageLimitError: weekly budget exhausted",
		"FreeUsageLimitError: upgrade required",
		"Monthly usage limit reached",
		"You have no available balance on your account",
		"insufficient_quota: please add billing",
		"out of budget for this model",
		"quota exceeded for this account",
		"please update your billing details",
	}
	for _, c := range cases {
		msg := &AssistantMessage{StopReason: StopError, ErrorMessage: c}
		if IsRetryableAssistantError(msg) {
			t.Errorf("expected NON-retryable limit error: %q", c)
		}
	}
}

func TestIsRetryableAssistantError_NotErrorStopReason(t *testing.T) {
	// Only error/aborted AssistantMessages qualify.
	msg := &AssistantMessage{StopReason: StopStop, ErrorMessage: "rate limit"}
	if IsRetryableAssistantError(msg) {
		t.Errorf("non-error stopReason should not classify as retryable")
	}
}

func TestIsRetryableAssistantError_NilAndEmpty(t *testing.T) {
	if IsRetryableAssistantError(nil) {
		t.Errorf("nil message should not be retryable")
	}
	msg := &AssistantMessage{StopReason: StopError}
	if IsRetryableAssistantError(msg) {
		t.Errorf("empty errorMessage should not be retryable")
	}
}

func TestIsRetryableAssistantError_UnmatchedPattern(t *testing.T) {
	// An errorMessage that doesn't match anything stays false.
	msg := &AssistantMessage{StopReason: StopError, ErrorMessage: "invalid api key format"}
	if IsRetryableAssistantError(msg) {
		t.Errorf("unmatched errorMessage should default to non-retryable")
	}
}

func TestNewProviderError(t *testing.T) {
	wrapped := errors.New("connection reset")
	pe := NewProviderError(503, "service overloaded", wrapped)
	if pe.HTTPCode != 503 {
		t.Errorf("HTTPCode = %d, want 503", pe.HTTPCode)
	}
	if pe.Kind != ErrorKindOverloaded {
		t.Errorf("Kind = %v, want overloaded", pe.Kind)
	}
	if !errors.Is(pe, wrapped) {
		t.Error("Unwrap failed")
	}
}

func TestIsRetryableProviderError(t *testing.T) {
	// 可重试
	if !IsRetryableProviderError(NewProviderError(429, "rate limit", nil)) {
		t.Error("429 should be retryable")
	}
	if !IsRetryableProviderError(NewProviderError(503, "overloaded", nil)) {
		t.Error("503 should be retryable")
	}

	// 不可重试
	if IsRetryableProviderError(NewProviderError(400, "bad request", nil)) {
		t.Error("400 should NOT be retryable")
	}
	if IsRetryableProviderError(NewProviderError(401, "unauthorized", nil)) {
		t.Error("401 should NOT be retryable")
	}

	// 未分类的错误
	if IsRetryableProviderError(errors.New("random error")) {
		t.Error("unclassified error should NOT be retryable")
	}
}

// ============================================================================
// 重试装饰器测试
// ============================================================================

func TestWrapRetry_SuccessOnFirstAttempt(t *testing.T) {
	attempts := 0
	result, err := WrapRetry(context.Background(), DefaultRetryPolicy(),
		func(ctx context.Context) (int, error) {
			attempts++
			return 42, nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 42 {
		t.Errorf("result = %d, want 42", result)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

func TestWrapRetry_RetryOnRateLimit(t *testing.T) {
	attempts := 0
	result, err := WrapRetry(context.Background(), DefaultRetryPolicy(),
		func(ctx context.Context) (int, error) {
			attempts++
			if attempts < 3 {
				return 0, NewProviderError(429, "rate limit", nil)
			}
			return 42, nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 42 {
		t.Errorf("result = %d, want 42", result)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestWrapRetry_NoRetryOnBadRequest(t *testing.T) {
	attempts := 0
	_, err := WrapRetry(context.Background(), DefaultRetryPolicy(),
		func(ctx context.Context) (int, error) {
			attempts++
			return 0, NewProviderError(400, "bad request", nil)
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (should not retry 400)", attempts)
	}
}

func TestWrapRetry_CustomShouldRetry(t *testing.T) {
	attempts := 0
	policy := DefaultRetryPolicy()
	policy.ShouldRetry = func(err error) bool {
		// 只重试 3 次以内
		return attempts < 3
	}
	_, err := WrapRetry(context.Background(), policy,
		func(ctx context.Context) (int, error) {
			attempts++
			return 0, errors.New("transient error")
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestWrapRetry_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	attempts := 0
	_, err := WrapRetry(ctx, DefaultRetryPolicy(),
		func(ctx context.Context) (int, error) {
			attempts++
			return 0, NewProviderError(429, "rate limit", nil)
		})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if attempts != 0 {
		t.Errorf("expected 0 attempts after cancellation, got %d", attempts)
	}
}

func TestWrapRetry_OnRetryCallback(t *testing.T) {
	var retried []int
	policy := DefaultRetryPolicy()
	policy.MaxAttempts = 3
	policy.OnRetry = func(attempt int, err error) {
		retried = append(retried, attempt)
	}
	policy.InitialBackoff = 1 * time.Millisecond // 快速测试

	attempts := 0
	_, err := WrapRetry(context.Background(), policy,
		func(ctx context.Context) (int, error) {
			attempts++
			if attempts < 3 {
				return 0, NewProviderError(429, "rate limit", nil)
			}
			return 42, nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(retried) != 2 {
		t.Errorf("expected 2 retries, got %d: %v", len(retried), retried)
	}
}

func TestExtractErrorKind(t *testing.T) {
	pe := NewProviderError(http.StatusTooManyRequests, "", nil)
	if kind := ExtractErrorKind(pe); kind != ErrorKindRateLimit {
		t.Errorf("ExtractErrorKind = %v, want rate_limit", kind)
	}
	// 未分类
	if kind := ExtractErrorKind(errors.New("random")); kind != ErrorKindUnknown {
		t.Errorf("ExtractErrorKind(unclassified) = %v, want unknown", kind)
	}
}

func TestIsHTTPCodeError(t *testing.T) {
	pe := NewProviderError(503, "", nil)
	if !IsHTTPCodeError(pe, 503) {
		t.Error("IsHTTPCodeError(503) should be true")
	}
	if IsHTTPCodeError(pe, 429) {
		t.Error("IsHTTPCodeError(429) should be false")
	}
}

func TestNewContextError(t *testing.T) {
	err := NewContextError("context too long", nil)
	if err.Kind != ErrorKindContextLength {
		t.Errorf("Kind = %v, want context_length", err.Kind)
	}
	if err.Kind.IsRetryable() {
		t.Error("context_length should NOT be retryable (need to trim context first)")
	}
}
