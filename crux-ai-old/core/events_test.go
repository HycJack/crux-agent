package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEventStreamPushAndEnd(t *testing.T) {
	s := NewEventStream[string, int]()

	s.Push("a")
	s.Push("b")
	s.End(42)

	result, err := s.Result()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 42 {
		t.Errorf("expected 42, got %d", result)
	}
}

func TestEventStreamError(t *testing.T) {
	s := NewEventStream[string, int]()

	expectedErr := errors.New("test error")
	s.Error(expectedErr)

	_, err := s.Result()
	if err != expectedErr {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
}

func TestEventStreamForEach(t *testing.T) {
	s := NewEventStream[AssistantMessageEvent, AssistantMessage]()

	go func() {
		s.Push(EventTextDelta{Type: "text_delta", Delta: "hello"})
		s.End(AssistantMessage{Role: "assistant"})
	}()

	var deltas []string
	result, err := s.ForEach(context.Background(), func(e AssistantMessageEvent) error {
		if d, ok := e.(EventTextDelta); ok {
			deltas = append(deltas, d.Delta)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Role != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", result.Role)
	}
	if len(deltas) != 1 || deltas[0] != "hello" {
		t.Errorf("expected ['hello'], got %v", deltas)
	}
}

func TestEventStreamForEachError(t *testing.T) {
	s := NewEventStream[string, int]()

	go func() {
		s.Push("a")
	}()

	expectedErr := errors.New("callback error")
	_, err := s.ForEach(context.Background(), func(v string) error {
		return expectedErr
	})

	if err != expectedErr {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
}

func TestEventStreamContextCancel(t *testing.T) {
	s := NewEventStream[string, int]()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.ForEach(ctx, func(v string) error {
		return nil
	})

	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestEventStreamDoubleEnd(t *testing.T) {
	s := NewEventStream[string, int]()

	s.End(1)
	s.End(2)

	result, _ := s.Result()
	if result != 1 {
		t.Errorf("expected 1, got %d", result)
	}
}

func TestEventStreamPushAfterEnd(t *testing.T) {
	s := NewEventStream[string, int]()

	s.End(1)
	s.Push("after")

	result, _ := s.Result()
	if result != 1 {
		t.Errorf("expected 1, got %d", result)
	}
}

func TestEventStreamTimeout(t *testing.T) {
	s := NewEventStream[string, int]()

	done := make(chan struct{})
	go func() {
		_, _ = s.Result()
		close(done)
	}()

	select {
	case <-done:
		t.Error("Result should block on unclosed stream")
	case <-time.After(50 * time.Millisecond):
		// Expected
	}
}
