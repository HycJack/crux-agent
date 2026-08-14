package events

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func makeEvent(typ string) Event {
	return SimpleEvent{
		EventType: typ,
		EventTime: time.Now(),
	}
}

func TestEventBus_Broadcast(t *testing.T) {
	bus := New()
	var calls int32

	bus.On("test", DispatchBroadcast, func(_ context.Context, _ Event) (any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, nil
	})

	_, _ = bus.Emit(context.Background(), "test", makeEvent("test"))

	// Broadcast 不等待，但要给 goroutine 一点时间
	time.Sleep(50 * time.Millisecond)

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("Broadcast 应调用 1 次，got %d", got)
	}
}

func TestEventBus_Parallel(t *testing.T) {
	bus := New()
	var calls int32

	for i := 0; i < 3; i++ {
		bus.On("test", DispatchParallel, func(_ context.Context, _ Event) (any, error) {
			atomic.AddInt32(&calls, 1)
			return nil, nil
		})
	}

	_, err := bus.Emit(context.Background(), "test", makeEvent("test"))
	if err != nil {
		t.Fatalf("Parallel 不应返回错误: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("Parallel 应调用 3 次，got %d", got)
	}
}

func TestEventBus_Parallel_AggregatesErrors(t *testing.T) {
	bus := New()
	err1 := errors.New("err1")
	err2 := errors.New("err2")

	bus.On("test", DispatchParallel, func(_ context.Context, _ Event) (any, error) { return nil, err1 })
	bus.On("test", DispatchParallel, func(_ context.Context, _ Event) (any, error) { return nil, err2 })

	_, err := bus.Emit(context.Background(), "test", makeEvent("test"))
	if err == nil {
		t.Fatal("Parallel 应聚合错误")
	}
}

func TestEventBus_Serial(t *testing.T) {
	bus := New()
	var order []int
	var mu sync.Mutex

	for i := 0; i < 3; i++ {
		idx := i
		bus.On("test", DispatchSerial, func(_ context.Context, _ Event) (any, error) {
			mu.Lock()
			order = append(order, idx)
			mu.Unlock()
			return nil, nil
		})
	}

	_, _ = bus.Emit(context.Background(), "test", makeEvent("test"))

	if len(order) != 3 {
		t.Fatalf("Serial 应调用 3 次，got %d", len(order))
	}
	for i, v := range order {
		if v != i {
			t.Fatalf("Serial 顺序错误，order=%v", order)
		}
	}
}

func TestEventBus_Serial_StopsOnError(t *testing.T) {
	bus := New()
	var calls int32

	bus.On("test", DispatchSerial, func(_ context.Context, _ Event) (any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("stop")
	})
	bus.On("test", DispatchSerial, func(_ context.Context, _ Event) (any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, nil
	})

	_, _ = bus.Emit(context.Background(), "test", makeEvent("test"))

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("Serial 遇错应停止，只调用 1 次，got %d", got)
	}
}

func TestEventBus_Bail(t *testing.T) {
	bus := New()
	var calls int32
	var mu sync.Mutex

	// 第一个返回 nil（通过）
	bus.On("test", DispatchBail, func(_ context.Context, _ Event) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, nil
	})

	// 第二个返回 error（短路）
	bus.On("test", DispatchBail, func(_ context.Context, _ Event) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, errors.New("blocked")
	})

	// 第三个不应执行
	bus.On("test", DispatchBail, func(_ context.Context, _ Event) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, nil
	})

	_, err := bus.Emit(context.Background(), "test", makeEvent("test"))
	if err == nil {
		t.Fatal("Bail 应返回错误")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("Bail 短路后应只调用 2 次，got %d", got)
	}
}

func TestEventBus_Bail_ReturnsResult(t *testing.T) {
	bus := New()
	bus.On("test", DispatchBail, func(_ context.Context, _ Event) (any, error) {
		return "blocked-result", nil
	})

	// 第二个不应执行
	bus.On("test", DispatchBail, func(_ context.Context, _ Event) (any, error) {
		return "should-not-reach", nil
	})

	res, _ := bus.Emit(context.Background(), "test", makeEvent("test"))
	if res != "blocked-result" {
		t.Fatalf("Bail 应返回首个非 nil 结果，got %v", res)
	}
}

func TestEventBus_Waterfall(t *testing.T) {
	bus := New()
	bus.On("test", DispatchWaterfall, func(_ context.Context, _ Event) (any, error) {
		return "step1", nil
	})
	bus.On("test", DispatchWaterfall, func(_ context.Context, _ Event) (any, error) {
		return "step2", nil
	})
	bus.On("test", DispatchWaterfall, func(_ context.Context, _ Event) (any, error) {
		return "step3", nil
	})

	res, _ := bus.Emit(context.Background(), "test", makeEvent("test"))
	if res != "step3" {
		t.Fatalf("Waterfall 应返回最后结果，got %v", res)
	}
}

func TestEventBus_Waterfall_StopsOnError(t *testing.T) {
	bus := New()
	bus.On("test", DispatchWaterfall, func(_ context.Context, _ Event) (any, error) {
		return "step1", nil
	})
	bus.On("test", DispatchWaterfall, func(_ context.Context, _ Event) (any, error) {
		return nil, errors.New("waterfall error")
	})
	bus.On("test", DispatchWaterfall, func(_ context.Context, _ Event) (any, error) {
		return "should-not-reach", nil
	})

	res, err := bus.Emit(context.Background(), "test", makeEvent("test"))
	if err == nil {
		t.Fatal("Waterfall 遇错应返回 error")
	}
	if res != nil {
		t.Fatalf("遇错时应返回 nil，got %v", res)
	}
}

func TestEventBus_Off(t *testing.T) {
	bus := New()
	var calls int32

	id := bus.On("test", DispatchSerial, func(_ context.Context, _ Event) (any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, nil
	})

	// 先触发一次
	_, _ = bus.Emit(context.Background(), "test", makeEvent("test"))
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("Off 前应调用 1 次，got %d", got)
	}

	// 取消
	bus.Off(id)

	// 再触发不应调用
	_, _ = bus.Emit(context.Background(), "test", makeEvent("test"))
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("Off 后不应再调用，got %d", got)
	}
}

func TestEventBus_Once(t *testing.T) {
	bus := New()
	var calls int32

	bus.Once("test", DispatchSerial, func(_ context.Context, _ Event) (any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, nil
	})

	_, _ = bus.Emit(context.Background(), "test", makeEvent("test"))
	_, _ = bus.Emit(context.Background(), "test", makeEvent("test"))

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("Once 应只调用 1 次，got %d", got)
	}
	if bus.ListenerCount("test", DispatchSerial) != 0 {
		t.Fatal("Once 触发后应被移除")
	}
}

func TestEventBus_HasListeners(t *testing.T) {
	bus := New()
	if bus.HasListeners("test") {
		t.Fatal("空总线不应有 listener")
	}

	bus.On("test", DispatchBroadcast, func(_ context.Context, _ Event) (any, error) { return nil, nil })
	if !bus.HasListeners("test") {
		t.Fatal("注册后应有 listener")
	}
}

func TestEventBus_ListenerCount(t *testing.T) {
	bus := New()
	bus.On("test", DispatchBroadcast, func(_ context.Context, _ Event) (any, error) { return nil, nil })
	bus.On("test", DispatchSerial, func(_ context.Context, _ Event) (any, error) { return nil, nil })

	if got := bus.ListenerCount("test", -1); got != 2 {
		t.Fatalf("总数应为 2，got %d", got)
	}
	if got := bus.ListenerCount("test", DispatchBroadcast); got != 1 {
		t.Fatalf("Broadcast 应有 1 个，got %d", got)
	}
}

func TestEventBus_DifferentModes(t *testing.T) {
	// 同一 eventType 下不同 mode 应各自派发
	bus := New()
	var bCalls, sCalls int32

	bus.On("test", DispatchBroadcast, func(_ context.Context, _ Event) (any, error) {
		atomic.AddInt32(&bCalls, 1)
		return nil, nil
	})
	bus.On("test", DispatchSerial, func(_ context.Context, _ Event) (any, error) {
		atomic.AddInt32(&sCalls, 1)
		return nil, nil
	})

	_, _ = bus.Emit(context.Background(), "test", makeEvent("test"))
	time.Sleep(50 * time.Millisecond)

	if got := atomic.LoadInt32(&bCalls); got != 1 {
		t.Fatalf("Broadcast handler 应调用 1 次，got %d", got)
	}
	if got := atomic.LoadInt32(&sCalls); got != 1 {
		t.Fatalf("Serial handler 应调用 1 次，got %d", got)
	}
}
