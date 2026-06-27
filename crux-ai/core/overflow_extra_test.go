package core

import (
	"errors"
	"testing"
)

// --- Case 1: error-message overflow (already exercised by overflow_test.go) -----
// These tests focus on Cases 2 & 3 (silent overflow).

func TestIsContextOverflowMessage_Case1_ErrorMessage(t *testing.T) {
	msg := &AssistantMessage{
		StopReason:   StopError,
		ErrorMessage: "prompt is too long: 300000 tokens > 200000 maximum",
	}
	if !IsContextOverflowMessage(msg, 0) {
		t.Error("Case 1: error message should be detected without contextWindow")
	}
}

func TestIsContextOverflowMessage_Case2_SilentOverflow(t *testing.T) {
	// z.ai style: successful response, but usage.input + cacheRead > window.
	msg := &AssistantMessage{
		StopReason: StopStop,
		Usage:      Usage{Input: 90000, CacheRead: 50000, Output: 200},
	}
	if !IsContextOverflowMessage(msg, 100000) {
		t.Error("Case 2: silent overflow (usage > window) should be detected")
	}
	// Below the threshold → no overflow.
	msg.Usage.Input = 50000
	if IsContextOverflowMessage(msg, 100000) {
		t.Error("Case 2: input below window should not be flagged")
	}
}

func TestIsContextOverflowMessage_Case2_NoWindowMeansNoSilentCheck(t *testing.T) {
	// Without contextWindow, Case 2 cannot run.
	msg := &AssistantMessage{
		StopReason: StopStop,
		Usage:      Usage{Input: 999999, Output: 1},
	}
	if IsContextOverflowMessage(msg, 0) {
		t.Error("Case 2: without contextWindow, should not flag silent overflow")
	}
}

func TestIsContextOverflowMessage_Case3_LengthStopOverflow(t *testing.T) {
	// Xiaomi MiMo style: stopReason=length, output=0, input fills contextWindow.
	msg := &AssistantMessage{
		StopReason: StopLength,
		Usage:      Usage{Input: 99000, CacheRead: 0, Output: 0},
	}
	if !IsContextOverflowMessage(msg, 100000) {
		t.Error("Case 3: filled window + zero output + length stop should be flagged")
	}
	// Output > 0 → not Xiaomi-style overflow.
	msg.Usage.Output = 100
	if IsContextOverflowMessage(msg, 100000) {
		t.Error("Case 3: with output > 0, length stop is not silent overflow")
	}
	// StopReason != length → not Case 3.
	msg2 := &AssistantMessage{
		StopReason: StopStop,
		Usage:      Usage{Input: 99000, Output: 0},
	}
	if IsContextOverflowMessage(msg2, 100000) {
		t.Error("Case 3: stopReason=stop should not trigger length-stop check")
	}
}

func TestIsContextOverflow_TypedErrorPath(t *testing.T) {
	oe := &OverflowError{Provider: ProviderOpenAI, ContextWindow: 1000, Usage: 5000}
	if !IsContextOverflow(oe) {
		t.Error("typed *OverflowError must short-circuit to true")
	}
	plain := errors.New("unknown")
	if IsContextOverflow(plain) {
		t.Error("non-overflow error should return false")
	}
}
