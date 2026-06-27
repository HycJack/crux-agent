package core

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// =============================================================================
// Sentinel / typed-error pairings
// =============================================================================

func TestOverflowError_ErrorsIs(t *testing.T) {
	e := &OverflowError{Provider: ProviderOpenAI, Message: "too long"}
	if !errors.Is(e, ErrOverflow) {
		t.Error("OverflowError should match ErrOverflow via Is")
	}
	var target *OverflowError
	if !errors.As(e, &target) {
		t.Error("errors.As should target *OverflowError")
	}
}

func TestOverflowError_MessageVariants(t *testing.T) {
	cases := []struct {
		name string
		err  OverflowError
		want string
	}{
		{"with-message", OverflowError{Provider: ProviderOpenAI, Message: "boom"}, "openai: context overflow: boom"},
		{"numeric", OverflowError{Provider: ProviderOpenAI, ContextWindow: 100, Usage: 200}, "openai: context overflow: usage 200 > window 100"},
		{"fallback", OverflowError{Provider: ProviderOpenAI}, "openai: context overflow"},
	}
	for _, c := range cases {
		if got := c.err.Error(); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestAuthError_ErrorsIs(t *testing.T) {
	cause := errors.New("token expired")
	e := &AuthError{Provider: ProviderAnthropic, Cause: cause}
	if !errors.Is(e, ErrAuth) {
		t.Error("AuthError should match ErrAuth")
	}
	if !errors.Is(e, cause) {
		t.Error("AuthError should Unwrap to cause")
	}
}

func TestRateLimitError_WithRetryAfter(t *testing.T) {
	e := &RateLimitError{Provider: ProviderOpenAI, RetryAfter: 7 * time.Second}
	if !errors.Is(e, ErrRateLimit) {
		t.Error("RateLimitError should match ErrRateLimit")
	}
	if got := e.Error(); got != "openai: rate limited (retry after 7s)" {
		t.Errorf("unexpected message: %q", got)
	}
}

func TestServerError_StoresStatusCode(t *testing.T) {
	e := &ServerError{Provider: ProviderOpenAI, StatusCode: 503}
	if !errors.Is(e, ErrServer) {
		t.Error("ServerError should match ErrServer")
	}
	if got := e.Error(); got != "openai: server error 503" {
		t.Errorf("unexpected message: %q", got)
	}
}

func TestNetworkError_ErrorsIs(t *testing.T) {
	e := &NetworkError{Provider: ProviderGoogle}
	if !errors.Is(e, ErrNetwork) {
		t.Error("NetworkError should match ErrNetwork")
	}
}

func TestAbortError_MatchesCanceled(t *testing.T) {
	e := &AbortError{}
	if !errors.Is(e, ErrAborted) {
		t.Error("AbortError should match ErrAborted")
	}
	if !errors.Is(e, context.Canceled) {
		t.Error("AbortError should match context.Canceled")
	}
}

func TestCompactionCancelledError(t *testing.T) {
	if (&CompactionCancelledError{}).Error() != "compaction cancelled" {
		t.Error("empty Reason should give bare message")
	}
	if got := (&CompactionCancelledError{Reason: "user"}).Error(); got != "compaction cancelled: user" {
		t.Errorf("unexpected message: %q", got)
	}
}

// =============================================================================
// HTTPTimeoutError
// =============================================================================

func TestHTTPTimeoutError_ErrorsIs(t *testing.T) {
	e := WrapHTTPTimeout(ProviderOpenAI, 5*time.Minute, context.DeadlineExceeded)
	if !errors.Is(e, ErrTimeout) {
		t.Error("should match ErrTimeout")
	}
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Error("should match context.DeadlineExceeded")
	}
	var target *HTTPTimeoutError
	if !errors.As(e, &target) {
		t.Error("errors.As should target *HTTPTimeoutError")
	}
	if target.Provider != ProviderOpenAI {
		t.Errorf("Provider = %v want openai", target.Provider)
	}
}

func TestWrapTimeout_NilCause(t *testing.T) {
	e := WrapTimeout(TimeoutSourceAgent, time.Second, nil)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Error("nil cause should default to context.DeadlineExceeded")
	}
}

func TestWrapToolTimeout(t *testing.T) {
	e := WrapToolTimeout("bash", 30*time.Second, context.DeadlineExceeded)
	var target *HTTPTimeoutError
	if !errors.As(e, &target) {
		t.Fatal("should unwrap to *HTTPTimeoutError")
	}
	if target.Source != TimeoutSourceTool || target.ToolName != "bash" {
		t.Errorf("unexpected fields: %+v", target)
	}
}

// =============================================================================
// ClassifyHTTPError
// =============================================================================

func TestClassifyHTTPError(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantType error
	}{
		{"429", http.StatusTooManyRequests, &RateLimitError{}},
		{"408", http.StatusRequestTimeout, &RateLimitError{}},
		{"401", http.StatusUnauthorized, &AuthError{}},
		{"403", http.StatusForbidden, &AuthError{}},
		{"500", http.StatusInternalServerError, &ServerError{}},
		{"503", http.StatusServiceUnavailable, &ServerError{}},
		{"413", http.StatusRequestEntityTooLarge, &OverflowError{}},
	}
	for _, tt := range tests {
		got := ClassifyHTTPError(ProviderOpenAI, tt.status, "boom")
		if got == nil {
			t.Errorf("%s: nil result", tt.name)
			continue
		}
		// Confirm the wrapped sentinel matches via errors.Is.
		switch tt.wantType.(type) {
		case *RateLimitError:
			if !errors.Is(got, ErrRateLimit) {
				t.Errorf("%s: expected ErrRateLimit", tt.name)
			}
		case *AuthError:
			if !errors.Is(got, ErrAuth) {
				t.Errorf("%s: expected ErrAuth", tt.name)
			}
		case *ServerError:
			if !errors.Is(got, ErrServer) {
				t.Errorf("%s: expected ErrServer", tt.name)
			}
		case *OverflowError:
			if !errors.Is(got, ErrOverflow) {
				t.Errorf("%s: expected ErrOverflow", tt.name)
			}
		}
	}
}

func TestClassifyHTTPError_NilForUnclassified(t *testing.T) {
	if got := ClassifyHTTPError(ProviderOpenAI, http.StatusBadRequest, "bad"); got != nil {
		t.Errorf("400 should be unclassified (nil), got %v", got)
	}
	if got := ClassifyHTTPError(ProviderOpenAI, http.StatusOK, ""); got != nil {
		t.Errorf("200 should be unclassified (nil), got %v", got)
	}
}

func TestTrimBody(t *testing.T) {
	short := "hello"
	if got := trimBody(short); got != "hello" {
		t.Errorf("trimBody(short) = %q", got)
	}
	long := make([]byte, 600)
	for i := range long {
		long[i] = 'a'
	}
	got := trimBody(string(long))
	if len(got) > 510 { // 500 + "..." suffix
		t.Errorf("trimBody should truncate, got len=%d", len(got))
	}
	if got[len(got)-3:] != "..." {
		t.Errorf("trimBody should end with ellipsis, got %q", got[len(got)-3:])
	}
}