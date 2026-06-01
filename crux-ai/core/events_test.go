package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestEventStreamPush 测试 Push 方法的正常流程
func TestEventStreamPush(t *testing.T) {
	stream := NewEventStream[AssistantMessageEvent, AssistantMessage]()
	defer stream.End(AssistantMessage{})

	evt := EventTextStart{Type: "text_start"}
	if !stream.Push(evt) {
		t.Fatal("Push should return true on first call")
	}
}

// TestEventStreamPushAfterClose 测试关闭后 Push 返回 false
func TestEventStreamPushAfterClose(t *testing.T) {
	stream := NewEventStream[AssistantMessageEvent, AssistantMessage]()
	stream.End(AssistantMessage{})

	if stream.Push(EventTextStart{Type: "text_start"}) {
		t.Fatal("Push should return false after End")
	}
}

// TestEventStreamPushAfterError 测试 Error 后 Push 返回 false
func TestEventStreamPushAfterError(t *testing.T) {
	stream := NewEventStream[AssistantMessageEvent, AssistantMessage]()
	stream.Error(errors.New("test error"))

	if stream.Push(EventTextStart{Type: "text_start"}) {
		t.Fatal("Push should return false after Error")
	}
}

// TestEventStreamEnd 测试 End 正常流程
func TestEventStreamEnd(t *testing.T) {
	stream := NewEventStream[AssistantMessageEvent, AssistantMessage]()

	msg := AssistantMessage{Role: "assistant", StopReason: StopStop}
	stream.End(msg)

	result, err := stream.Result()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if result.Role != "assistant" {
		t.Fatalf("Expected role 'assistant', got: %s", result.Role)
	}
}

// TestEventStreamError 测试 Error 流程
func TestEventStreamError(t *testing.T) {
	stream := NewEventStream[AssistantMessageEvent, AssistantMessage]()

	testErr := errors.New("stream error")
	stream.Error(testErr)

	_, err := stream.Result()
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if err.Error() != "stream error" {
		t.Fatalf("Expected 'stream error', got: %s", err.Error())
	}
}

// TestEventStreamDoubleEnd 测试重复调用 End 不会 panic
func TestEventStreamDoubleEnd(t *testing.T) {
	stream := NewEventStream[AssistantMessageEvent, AssistantMessage]()

	stream.End(AssistantMessage{Role: "first"})
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Double End should not panic: %v", r)
		}
	}()
	stream.End(AssistantMessage{Role: "second"})
}

// TestEventStreamDoubleError 测试重复调用 Error 不会 panic
func TestEventStreamDoubleError(t *testing.T) {
	stream := NewEventStream[AssistantMessageEvent, AssistantMessage]()

	stream.Error(errors.New("first"))
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Double Error should not panic: %v", r)
		}
	}()
	stream.Error(errors.New("second"))
}

// TestEventStreamStop 测试 Stop 后 Push 返回 false
func TestEventStreamStop(t *testing.T) {
	stream := NewEventStream[AssistantMessageEvent, AssistantMessage]()

	stream.Stop()

	if stream.Push(EventTextStart{Type: "text_start"}) {
		t.Fatal("Push should return false after Stop")
	}
}

// TestEventStreamConcurrentPush 测试并发 Push 的安全性
func TestEventStreamConcurrentPush(t *testing.T) {
	stream := NewEventStream[AssistantMessageEvent, AssistantMessage]()

	const goroutines = 50
	const eventsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				stream.Push(EventTextStart{Type: "text_start"})
			}
		}()
	}

	wg.Wait()
	stream.End(AssistantMessage{})

	_, err := stream.Result()
	if err != nil {
		t.Fatalf("Result should not error: %v", err)
	}
}

// TestEventStreamForEach 测试 ForEach 正常迭代
func TestEventStreamForEach(t *testing.T) {
	stream := NewEventStream[AssistantMessageEvent, AssistantMessage]()

	go func() {
		stream.Push(EventTextStart{Type: "text_start"})
		stream.Push(EventTextDelta{Type: "text_delta", Delta: "hello"})
		stream.Push(EventTextEnd{Type: "text_end"})
		stream.End(AssistantMessage{Role: "assistant", StopReason: StopStop})
	}()

	count := 0
	_, err := stream.ForEach(context.Background(), func(evt AssistantMessageEvent) error {
		count++
		return nil
	})

	if err != nil {
		t.Fatalf("ForEach returned error: %v", err)
	}
	if count != 3 {
		t.Fatalf("Expected 3 events, got %d", count)
	}
}

// TestEventStreamForEachContextCancel 测试 context 取消传播
func TestEventStreamForEachContextCancel(t *testing.T) {
	stream := NewEventStream[AssistantMessageEvent, AssistantMessage]()

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		for {
			stream.Push(EventTextDelta{Type: "text_delta", Delta: "x"})
			time.Sleep(10 * time.Millisecond)
		}
	}()

	cancel()

	_, err := stream.ForEach(ctx, func(evt AssistantMessageEvent) error {
		return nil
	})

	if err == nil {
		t.Fatal("Expected context cancellation error")
	}
	if err != context.Canceled {
		t.Fatalf("Expected context.Canceled, got: %v", err)
	}
}

// TestEventStreamForEachHandlerError 测试 handler 返回错误时停止
func TestEventStreamForEachHandlerError(t *testing.T) {
	stream := NewEventStream[AssistantMessageEvent, AssistantMessage]()

	handlerErr := errors.New("handler error")

	go func() {
		stream.Push(EventTextStart{Type: "text_start"})
		stream.End(AssistantMessage{})
	}()

	_, err := stream.ForEach(context.Background(), func(evt AssistantMessageEvent) error {
		return handlerErr
	})

	if err != handlerErr {
		t.Fatalf("Expected handler error, got: %v", err)
	}
}

// TestCalculateCost 测试成本计算
func TestCalculateCost(t *testing.T) {
	model := Model{
		Cost: Cost{
			Input:      3.0,
			Output:     15.0,
			CacheRead:  0.3,
			CacheWrite: 3.75,
		},
	}
	usage := Usage{
		Input:      1000,
		Output:     500,
		CacheRead:  2000,
		CacheWrite: 1000,
	}

	cost := CalculateCost(model, usage)

	expectedInput := 1000.0 * 3.0 / 1_000_000
	expectedOutput := 500.0 * 15.0 / 1_000_000
	expectedCacheRead := 2000.0 * 0.3 / 1_000_000
	expectedCacheWrite := 1000.0 * 3.75 / 1_000_000
	expectedTotal := expectedInput + expectedOutput + expectedCacheRead + expectedCacheWrite

	if cost.Input != expectedInput {
		t.Errorf("Input cost: expected %f, got %f", expectedInput, cost.Input)
	}
	if cost.Output != expectedOutput {
		t.Errorf("Output cost: expected %f, got %f", expectedOutput, cost.Output)
	}
	if cost.CacheRead != expectedCacheRead {
		t.Errorf("CacheRead cost: expected %f, got %f", expectedCacheRead, cost.CacheRead)
	}
	if cost.CacheWrite != expectedCacheWrite {
		t.Errorf("CacheWrite cost: expected %f, got %f", expectedCacheWrite, cost.CacheWrite)
	}
	if cost.Total != expectedTotal {
		t.Errorf("Total cost: expected %f, got %f", expectedTotal, cost.Total)
	}
}

// TestCalculateCostZeroUsage 测试零使用量的成本
func TestCalculateCostZeroUsage(t *testing.T) {
	model := Model{
		Cost: Cost{Input: 3.0, Output: 15.0},
	}
	usage := Usage{}

	cost := CalculateCost(model, usage)
	if cost.Total != 0 {
		t.Errorf("Expected zero cost, got %f", cost.Total)
	}
}
