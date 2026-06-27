package core

import (
	"errors"
	"testing"
)

// --- nonRetryablePatterns: quota / billing exclusion ----------------------------

func TestIsRetryableError_NonRetryableQuota(t *testing.T) {
	cases := []string{
		"GoUsageLimitError: free tier exceeded",
		"FreeUsageLimitError hit",
		"Monthly usage limit reached",
		"Please top up your available balance",
		"Error 429: insufficient_quota on account",
		"You are out of budget for this model",
		"quota exceeded for today",
		"Account suspended due to billing issue",
		"Insufficient BILLING credit",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			if IsRetryableError(errors.New(msg)) {
				t.Errorf("expected non-retryable for %q", msg)
			}
		})
	}
}

func TestIsRetryableError_NewRetryableWebSocket(t *testing.T) {
	cases := []string{
		"websocket closed by peer",
		"WEBSOCKET ERROR 1006",
		"stream ended without message_stop",
		"Anthropic stream ended before message_stop",
		"http2 request did not get a response",
		"Provider returned error 502",
		"network error: connection lost",
		"other side closed the connection",
		"reset before headers were written",
		"connection lost mid-stream",
		"context terminated by server",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			if !IsRetryableError(errors.New(msg)) {
				t.Errorf("expected retryable for %q", msg)
			}
		})
	}
}

// --- IsRetryableAssistantError -------------------------------------------------

func TestIsRetryableAssistantError_NilAndNonError(t *testing.T) {
	if IsRetryableAssistantError(nil) {
		t.Error("nil msg should not be retryable")
	}
	msg := &AssistantMessage{StopReason: StopStop, ErrorMessage: "all good"}
	if IsRetryableAssistantError(msg) {
		t.Error("non-error msg should not be retryable")
	}
	msg = &AssistantMessage{StopReason: StopError, ErrorMessage: ""}
	if IsRetryableAssistantError(msg) {
		t.Error("error msg without errorMessage should not be retryable")
	}
}

func TestIsRetryableAssistantError_QuotaShortCircuits(t *testing.T) {
	msg := &AssistantMessage{
		StopReason:    StopError,
		ErrorMessage:  "quota exceeded",
	}
	if IsRetryableAssistantError(msg) {
		t.Error("quota-exceeded msg must not be classified retryable")
	}
}

func TestIsRetryableAssistantError_Transient(t *testing.T) {
	cases := []string{
		"upstream connect timeout",
		"HTTP 502 bad gateway",
		"service unavailable",
		"websocket closed unexpectedly",
		"connection reset by peer",
	}
	for _, m := range cases {
		t.Run(m, func(t *testing.T) {
			msg := &AssistantMessage{StopReason: StopError, ErrorMessage: m}
			if !IsRetryableAssistantError(msg) {
				t.Errorf("expected retryable for %q", m)
			}
		})
	}
}

// --- buildProviderErrorPattern -------------------------------------------------

func TestBuildProviderErrorPattern(t *testing.T) {
	re := buildProviderErrorPattern("foo", "bar", "baz")
	if !re.MatchString("FOO") || !re.MatchString("Bar") || !re.MatchString("BAZ") {
		t.Error("pattern should match all fragments case-insensitively")
	}
	if re.MatchString("qux") {
		t.Error("pattern should not match unrelated text")
	}
}