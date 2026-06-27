package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// ============================================================================
// Helper: Create a mock event stream
// ============================================================================

// startMockStream creates a mock EventStream that generates events at intervals.
// events: a series of events (value, delay), ends automatically after sending
type mockEventValue struct {
	value string
	err   error
	delay time.Duration
}

func startMockStream(events ...struct {
	value string
	err   error
	delay time.Duration
}) *EventStream[string, string] {
	stream := NewEventStream[string, string]()

	go func() {
		for _, evt := range events {
			if evt.delay > 0 {
				time.Sleep(evt.delay)
			}
			if evt.err != nil {
				stream.Error(evt.err)
				return
			}
			stream.Push(evt.value)
		}
		stream.End("done")
	}()

	return stream
}

// ============================================================================
// StreamTimeoutError 测试
// ============================================================================

func TestTimeoutError(t *testing.T) {
	err := &TimeoutError{Kind: "first_event", Timeout: 10 * time.Second}
	if !IsTimeoutError(err) {
		t.Error("IsTimeoutError should return true")
	}
	if err.Error() != "stream first_event timeout after 10s" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestIsTimeoutError(t *testing.T) {
	if IsTimeoutError(errors.New("other error")) {
		t.Error("other errors should not be timeout errors")
	}
	if IsTimeoutError(nil) {
		t.Error("nil should not be timeout errors")
	}
}

// ============================================================================
// StreamWithTimeout 测试
// ============================================================================

func TestStreamWithTimeout_ReceivesEvents(t *testing.T) {
	stream := startMockStream(
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "hello", delay: 0},
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "world", delay: 0},
	)

	wrapped := NewEventStreamWithTimeout(stream, DefaultStreamTimeoutConfig())
	var received []string
	result, err := wrapped.ForEach(context.Background(), func(s string) error {
		received = append(received, s)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Errorf("result = %q, want %q", result, "done")
	}
	if len(received) != 2 || received[0] != "hello" || received[1] != "world" {
		t.Errorf("received = %v, want [hello world]", received)
	}
}

func TestStreamWithTimeout_FirstEventTimeout(t *testing.T) {
	stream := startMockStream(
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "late", delay: 2 * time.Second},
	)

	config := DefaultStreamTimeoutConfig()
	config.FirstEventTimeout = 100 * time.Millisecond // 很快超时

	wrapped := NewEventStreamWithTimeout(stream, config)
	_, err := wrapped.ForEach(context.Background(), func(s string) error {
		return nil
	})
	if !IsTimeoutError(err) {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestStreamWithTimeout_IdleTimeout(t *testing.T) {
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
	config.FirstEventTimeout = 5 * time.Second  // 首帧不超时
	config.IdleTimeout = 100 * time.Millisecond // idle 很快超时

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

func TestStreamWithTimeout_CtxCancel(t *testing.T) {
	stream := startMockStream(
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "hello", delay: 10 * time.Millisecond},
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "world", delay: 200 * time.Millisecond},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wrapped := NewEventStreamWithTimeout(stream, DefaultStreamTimeoutConfig())
	var received []string

	// �?goroutine 中消费，稍后取消
	errCh := make(chan error, 1)
	go func() {
		_, err := wrapped.ForEach(ctx, func(s string) error {
			received = append(received, s)
			if len(received) == 1 {
				cancel() // 收到第一个后取消
			}
			return nil
		})
		errCh <- err
	}()

	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestStreamWithTimeout_HandlerError(t *testing.T) {
	stream := startMockStream(
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "first", delay: 0},
	)

	wrapped := NewEventStreamWithTimeout(stream, DefaultStreamTimeoutConfig())
	_, err := wrapped.ForEach(context.Background(), func(s string) error {
		return fmt.Errorf("handler error")
	})
	if err == nil || err.Error() != "handler error" {
		t.Errorf("expected handler error, got %v", err)
	}
}

func TestStreamWithTimeout_IsProgressItem(t *testing.T) {
	// Heartbeat events (marked as isProgress=false) should not reset idle timer
	stream := startMockStream(
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "ping", delay: 10 * time.Millisecond},
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "ping", delay: 50 * time.Millisecond}, // 心跳
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "actual_content", delay: 100 * time.Millisecond}, // 这个应该�?idle 超时
	)

	config := DefaultStreamTimeoutConfig()
	config.FirstEventTimeout = 5 * time.Second
	config.IdleTimeout = 80 * time.Millisecond // 三次心跳就会超时
	config.IsProgressItem = func(item any) bool {
		s, ok := item.(string)
		if !ok {
			return true
		}
		// "ping" 不是进度事件（心跳）
		return s != "ping"
	}

	wrapped := NewEventStreamWithTimeout(stream, config)
	var received []string
	_, err := wrapped.ForEach(context.Background(), func(s string) error {
		received = append(received, s)
		return nil
	})
	if !IsTimeoutError(err) {
		t.Fatalf("expected timeout due to heartbeats not resetting timer, got %v", err)
	}
	// 应该收到 3 个事件：ping, ping(�?个心�?, 然后超时
	// 注意：第2�?ping�?0ms�? idle timeout(80ms) �?130ms > 100ms
	// 所以实际上应该在第3�?actual_content 之前就超时了
	if len(received) != 2 {
		t.Logf("received %d events (expected 2 heartbeats then timeout)", len(received))
	}
}

func TestStreamWithTimeout_ZeroTimeoutDefaults(t *testing.T) {
	// Zero timeout == infinite
	stream := startMockStream(
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "ok", delay: 500 * time.Millisecond},
	)

	config := StreamTimeoutConfig{
		FirstEventTimeout: 0,
		IdleTimeout:       0,
		IsProgressItem:    nil,
	}

	wrapped := NewEventStreamWithTimeout(stream, config)
	result, err := wrapped.ForEach(context.Background(), func(s string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Errorf("result = %q, want %q", result, "done")
	}
}

// ============================================================================
// Race 测试
// ============================================================================

func TestRace_ReceivesEvents(t *testing.T) {
	stream := startMockStream(
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "a", delay: 0},
	)

	race := NewRace[string, string](stream, RaceConfig{PerEventTimeout: 5 * time.Second})
	result := race.Next(context.Background())
	if result.Kind != RaceEvent {
		t.Fatalf("expected RaceEvent, got %v", result.Kind)
	}
	if result.Item != "a" {
		t.Errorf("item = %q, want %q", result.Item, "a")
	}
}

func TestRace_Timeout(t *testing.T) {
	stream := startMockStream(
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "slow", delay: 2 * time.Second},
	)

	race := NewRace[string, string](stream, RaceConfig{PerEventTimeout: 50 * time.Millisecond})
	result := race.Next(context.Background())
	if result.Kind != RaceTimeout {
		t.Fatalf("expected RaceTimeout, got %v", result.Kind)
	}
}

func TestRace_Abort(t *testing.T) {
	stream := startMockStream(
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "slow", delay: 2 * time.Second},
	)

	abort := make(chan struct{})
	race := NewRace[string, string](stream, RaceConfig{Abort: abort})

	// �?goroutine 中等待，稍后 abort
	resultCh := make(chan RaceResult[string], 1)
	go func() {
		resultCh <- race.Next(context.Background())
	}()
	close(abort)

	result := <-resultCh
	if result.Kind != RaceAbort {
		t.Fatalf("expected RaceAbort, got %v", result.Kind)
	}
}

func TestRace_Done(t *testing.T) {
	stream := startMockStream(
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "only", delay: 0},
	)

	race := NewRace[string, string](stream, RaceConfig{})

	// 消耗所有事件
	result := race.Next(context.Background())
	if result.Kind != RaceEvent {
		t.Fatalf("expected RaceEvent, got %v", result.Kind)
	}

	result = race.Next(context.Background())
	if result.Kind != RaceDone {
		t.Fatalf("expected RaceDone, got %v", result.Kind)
	}
}

func TestRace_Error(t *testing.T) {
	stream := startMockStream(
		struct {
			value string
			err   error
			delay time.Duration
		}{value: "", err: errors.New("stream error"), delay: 0},
	)

	race := NewRace[string, string](stream, RaceConfig{})
	result := race.Next(context.Background())
	if result.Kind != RaceError {
		t.Fatalf("expected RaceError, got %v", result.Kind)
	}
	if result.Error == nil || result.Error.Error() != "stream error" {
		t.Errorf("unexpected error: %v", result.Error)
	}
}

func TestRace_CtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	stream := startMockStream()
	race := NewRace[string, string](stream, RaceConfig{PerEventTimeout: 5 * time.Second})

	result := race.Next(ctx)
	if result.Kind != RaceAbort {
		t.Fatalf("expected RaceAbort due to cancelled context, got %v", result.Kind)
	}
}
