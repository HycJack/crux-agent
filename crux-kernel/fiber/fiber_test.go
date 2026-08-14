package fiber

import (
	"context"
	"errors"
	"testing"
)

// fakeLoader 测试用 loader，可控制返回的 disposer 和 error。
type fakeLoader struct {
	disposer func() error
	err      error
	loaded   int // Load 调用次数
}

func (l *fakeLoader) Load(_ context.Context) (func() error, error) {
	l.loaded++
	return l.disposer, l.err
}

func TestPluginFiber_Load_Success(t *testing.T) {
	disposed := false
	loader := &fakeLoader{disposer: func() error { disposed = true; return nil }}
	f := New("test", loader, nil)

	if got := f.State(); got != StatePending {
		t.Fatalf("初始状态应为 pending，got %s", got)
	}

	if err := f.Load(context.Background()); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	if got := f.State(); got != StateActive {
		t.Fatalf("加载后状态应为 active，got %s", got)
	}
	if loader.loaded != 1 {
		t.Fatalf("Load 应被调用 1 次，got %d", loader.loaded)
	}

	// Dispose
	if err := f.Dispose(); err != nil {
		t.Fatalf("Dispose 失败: %v", err)
	}
	if !disposed {
		t.Fatal("disposer 未被调用")
	}
	if got := f.State(); got != StateDisposed {
		t.Fatalf("Dispose 后状态应为 disposed，got %s", got)
	}
}

func TestPluginFiber_Load_Failed(t *testing.T) {
	loadErr := errors.New("load failed")
	loader := &fakeLoader{err: loadErr}
	f := New("test", loader, nil)

	if err := f.Load(context.Background()); err == nil {
		t.Fatal("Load 应返回错误")
	}

	if got := f.State(); got != StateFailed {
		t.Fatalf("失败后状态应为 failed，got %s", got)
	}
	if err := f.Err(); !errors.Is(err, loadErr) {
		t.Fatalf("Err 应返回 loadErr，got %v", err)
	}

	// 从 failed 可重新 Load
	loader.err = nil
	loader.disposer = func() error { return nil }
	if err := f.Load(context.Background()); err != nil {
		t.Fatalf("从 failed 重新 Load 失败: %v", err)
	}
	if got := f.State(); got != StateActive {
		t.Fatalf("重试后状态应为 active，got %s", got)
	}
}

func TestPluginFiber_Load_FromActive_ShouldFail(t *testing.T) {
	loader := &fakeLoader{disposer: func() error { return nil }}
	f := New("test", loader, nil)
	_ = f.Load(context.Background())

	if err := f.Load(context.Background()); err == nil {
		t.Fatal("从 active 状态 Load 应返回错误")
	}
}

func TestPluginFiber_Dispose_Idempotent(t *testing.T) {
	calls := 0
	loader := &fakeLoader{disposer: func() error { calls++; return nil }}
	f := New("test", loader, nil)
	_ = f.Load(context.Background())

	_ = f.Dispose()
	_ = f.Dispose()
	_ = f.Dispose()

	if calls != 1 {
		t.Fatalf("disposer 应只被调用 1 次，got %d", calls)
	}
	if got := f.State(); got != StateDisposed {
		t.Fatalf("状态应为 disposed，got %s", got)
	}
}

func TestPluginFiber_Reload(t *testing.T) {
	oldDisposed := false
	newDisposed := false

	loader1 := &fakeLoader{disposer: func() error { oldDisposed = true; return nil }}
	f := New("test", loader1, nil)
	_ = f.Load(context.Background())

	// Reload：换新 loader
	loader2 := &fakeLoader{disposer: func() error { newDisposed = true; return nil }}
	if err := f.Reload(context.Background(), loader2, nil, "v2"); err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}

	if !oldDisposed {
		t.Fatal("旧 disposer 应被调用")
	}
	if got := f.State(); got != StateActive {
		t.Fatalf("Reload 后状态应为 active，got %s", got)
	}
	if f.Epoch() != "v2" {
		t.Fatalf("Epoch 应为 v2，got %s", f.Epoch())
	}

	// Dispose 验证新 disposer 被调用
	_ = f.Dispose()
	if !newDisposed {
		t.Fatal("新 disposer 应被调用")
	}
}

func TestPluginFiber_Reload_SameEpoch_Skipped(t *testing.T) {
	disposerCalls := 0
	loader := &fakeLoader{disposer: func() error { disposerCalls++; return nil }}
	f := New("test", loader, nil)
	_ = f.Load(context.Background())
	// 手动设置 epoch（通过 Reload 设置）
	if err := f.Reload(context.Background(), loader, nil, "v1"); err != nil {
		t.Fatalf("第一次 Reload 失败: %v", err)
	}

	// 第二次 Reload 相同 epoch，应跳过
	loader2 := &fakeLoader{disposer: func() error { return nil }}
	beforeCalls := disposerCalls
	err := f.Reload(context.Background(), loader2, nil, "v1")
	if err != nil {
		t.Fatalf("Reload 不应失败: %v", err)
	}
	if disposerCalls != beforeCalls {
		t.Fatalf("相同 epoch 应跳过，disposer 调用次数不应变化，before=%d after=%d", beforeCalls, disposerCalls)
	}
}
