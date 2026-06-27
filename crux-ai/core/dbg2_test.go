package core

import (
	"context"
	"testing"
	"time"
)

func TestStreamWithTimeout_IdleTimeout_Copy(t *testing.T) {
	stream := startMockStream(
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "fast", delay: 10 * time.Millisecond},
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "slow", delay: 500 * time.Millisecond},
	)

	config := DefaultStreamTimeoutConfig()
	config.FirstEventTimeout = 5 * time.Second
	config.IdleTimeout = 100 * time.Millisecond

	wrapped := NewEventStreamWithTimeout(stream, config)
	var received []string
	_, err := wrapped.ForEach(context.Background(), func(s string) error {
		received = append(received, s)
		return nil
	})
	if !IsTimeoutError(err) {
		t.Fatalf("expected idle timeout error, got %v", err)
	}
	if len(received) != 1 {
		t.Errorf("expected 1 event before timeout, got %d", len(received))
	}
}