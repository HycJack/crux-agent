package container

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	rt "github.com/hycjack/crux-kernel"
	"github.com/hycjack/crux-kernel/events"
	"github.com/hycjack/crux-kernel/fiber"
)

// 测试用服务类型
type Logger struct{ name string }
type Model struct{ vendor string }

func TestContainer_RegisterGet(t *testing.T) {
	c := New()
	logger := &Logger{name: "test"}
	if err := c.Register(logger); err != nil {
		t.Fatalf("Register 失败: %v", err)
	}

	var got *Logger
	if err := c.Get(&got); err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got == nil || got.name != "test" {
		t.Fatalf("Get 返回错误，got %v", got)
	}
}

func TestContainer_GetNotFound(t *testing.T) {
	c := New()
	var got *Logger
	if err := c.Get(&got); !errors.Is(err, rt.ErrServiceNotFound) {
		t.Fatalf("应返回 ErrServiceNotFound，got %v", err)
	}
}

func TestContainer_RegisterNil(t *testing.T) {
	c := New()
	if err := c.Register(nil); err == nil {
		t.Fatal("Register nil 应返回错误")
	}
}

func TestContainer_DuplicateRegister(t *testing.T) {
	c := New()
	_ = c.Register(&Logger{name: "old"})
	// 覆盖
	if err := c.Register(&Logger{name: "new"}); err != nil {
		t.Fatalf("重复 Register 失败: %v", err)
	}

	var got *Logger
	_ = c.Get(&got)
	if got.name != "new" {
		t.Fatalf("应拿到新值，got %s", got.name)
	}
}

// Stringer 测试用接口
type Stringer interface {
	String() string
}

// NamedLogger 实现 Stringer
func (l *Logger) String() string { return l.name }

func TestContainer_RegisterAs(t *testing.T) {
	c := New()
	logger := &Logger{name: "test"}

	// 按具体类型注册（Register）+ 按接口类型注册（RegisterAs）
	if err := c.Register(logger); err != nil {
		t.Fatalf("Register 失败: %v", err)
	}
	if err := c.RegisterAs(logger, (*Stringer)(nil)); err != nil {
		t.Fatalf("RegisterAs 失败: %v", err)
	}

	// 按具体类型查找
	var got *Logger
	if err := c.Get(&got); err != nil {
		t.Fatalf("按具体类型 Get 失败: %v", err)
	}
	if got.name != "test" {
		t.Fatalf("具体类型值错误，got %s", got.name)
	}

	// 按接口类型查找
	var s Stringer
	if err := c.Get(&s); err != nil {
		t.Fatalf("按接口类型 Get 失败: %v", err)
	}
	if s.String() != "test" {
		t.Fatalf("接口类型值错误，got %s", s.String())
	}
}

func TestContainer_RegisterAsNil(t *testing.T) {
	c := New()
	if err := c.RegisterAs(nil, (*Stringer)(nil)); err == nil {
		t.Fatal("RegisterAs nil 应返回错误")
	}
	if err := c.RegisterAs(&Logger{}, nil); err == nil {
		t.Fatal("RegisterAs 到 nil 类型应返回错误")
	}
}

func TestContainer_Plugin(t *testing.T) {
	c := New()
	disposed := false

	f := c.Plugin("test", func(c *Container) (func() error, error) {
		_ = c.Register(&Logger{name: "from-plugin"})
		return func() error { disposed = true; return nil }, nil
	})

	if f.State() != fiber.StateActive {
		t.Fatalf("Plugin 加载后应为 active，got %s", f.State())
	}

	var got *Logger
	_ = c.Get(&got)
	if got == nil || got.name != "from-plugin" {
		t.Fatalf("Plugin 应注册服务，got %v", got)
	}

	// Dispose
	if err := c.Dispose(); err != nil {
		t.Fatalf("Dispose 失败: %v", err)
	}
	if !disposed {
		t.Fatal("Plugin 的 disposer 未被调用")
	}
	if c.State() != StateDisposed {
		t.Fatalf("Container 应为 disposed，got %s", c.State())
	}
}

func TestContainer_PluginLoadFailed(t *testing.T) {
	c := New()
	loadErr := errors.New("load failed")

	f := c.Plugin("test", func(c *Container) (func() error, error) {
		return nil, loadErr
	})

	if f.State() != fiber.StateFailed {
		t.Fatalf("失败后应为 failed，got %s", f.State())
	}
	if !errors.Is(f.Err(), loadErr) {
		t.Fatalf("Err 应返回 loadErr，got %v", f.Err())
	}
}

func TestContainer_PluginDuplicateName(t *testing.T) {
	c := New()
	_ = c.Plugin("test", func(c *Container) (func() error, error) { return nil, nil })

	// 同名插件应失败
	f2 := c.Plugin("test", func(c *Container) (func() error, error) { return nil, nil })
	if f2.State() != fiber.StateFailed {
		t.Fatalf("同名插件应 failed，got %s", f2.State())
	}
}

func TestContainer_DisposeOrder(t *testing.T) {
	c := New()
	var order []string

	// 注册顺序：A → B → C
	// Dispose 顺序应为 C → B → A
	_ = c.Plugin("A", func(c *Container) (func() error, error) {
		return func() error { order = append(order, "A"); return nil }, nil
	})
	_ = c.Plugin("B", func(c *Container) (func() error, error) {
		return func() error { order = append(order, "B"); return nil }, nil
	})
	_ = c.Plugin("C", func(c *Container) (func() error, error) {
		return func() error { order = append(order, "C"); return nil }, nil
	})

	if err := c.Dispose(); err != nil {
		t.Fatalf("Dispose 失败: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("应调用 3 个 disposer，got %d", len(order))
	}
	if order[0] != "C" || order[1] != "B" || order[2] != "A" {
		t.Fatalf("Dispose 顺序错误，应为 C→B→A，got %v", order)
	}
}

func TestContainer_DisposeIdempotent(t *testing.T) {
	c := New()
	calls := 0
	_ = c.Plugin("test", func(c *Container) (func() error, error) {
		return func() error { calls++; return nil }, nil
	})

	_ = c.Dispose()
	_ = c.Dispose()
	_ = c.Dispose()

	if calls != 1 {
		t.Fatalf("Dispose 应幂等，disposer 只调用 1 次，got %d", calls)
	}
}

func TestContainer_DisposedRejectsOps(t *testing.T) {
	c := New()
	_ = c.Dispose()

	if err := c.Register(&Logger{}); !errors.Is(err, rt.ErrContainerDisposed) {
		t.Fatalf("disposed 后 Register 应返回 ErrContainerDisposed，got %v", err)
	}

	var got *Logger
	if err := c.Get(&got); !errors.Is(err, rt.ErrContainerDisposed) {
		t.Fatalf("disposed 后 Get 应返回 ErrContainerDisposed，got %v", err)
	}
}

func TestContainer_ReloadPlugin(t *testing.T) {
	c := New()
	var oldDisposed, newDisposed int32

	_ = c.Plugin("test", func(c *Container) (func() error, error) {
		return func() error { atomic.AddInt32(&oldDisposed, 1); return nil }, nil
	})

	// Reload
	if err := c.ReloadPlugin("test", func(c *Container) (func() error, error) {
		return func() error { atomic.AddInt32(&newDisposed, 1); return nil }, nil
	}, nil, "v2"); err != nil {
		t.Fatalf("ReloadPlugin 失败: %v", err)
	}

	if atomic.LoadInt32(&oldDisposed) != 1 {
		t.Fatal("Reload 后旧 disposer 应被调用")
	}

	// 容器 Dispose 时新 disposer 被调用
	_ = c.Dispose()
	if atomic.LoadInt32(&newDisposed) != 1 {
		t.Fatal("Dispose 后新 disposer 应被调用")
	}
}

func TestContainer_ReloadPluginNotFound(t *testing.T) {
	c := New()
	err := c.ReloadPlugin("notexist", func(c *Container) (func() error, error) { return nil, nil }, nil, "v1")
	if !errors.Is(err, rt.ErrPluginNotFound) {
		t.Fatalf("应返回 ErrPluginNotFound，got %v", err)
	}
}

func TestContainer_Isolate(t *testing.T) {
	root := New()
	_ = root.Register(&Model{vendor: "openai"})   // 共享服务

	child := root.Isolate("user-1")
	_ = child.Register(&Logger{name: "user1-logger"}) // 子容器独有

	// 子容器能拿到父容器的 Model
	var model *Model
	if err := child.Get(&model); err != nil {
		t.Fatalf("子容器应能继承父服务，got %v", err)
	}
	if model.vendor != "openai" {
		t.Fatalf("Model 应为 openai，got %s", model.vendor)
	}

	// 子容器能拿到自己的 Logger
	var logger *Logger
	if err := child.Get(&logger); err != nil {
		t.Fatalf("子容器 Get 自有服务失败: %v", err)
	}
	if logger.name != "user1-logger" {
		t.Fatalf("Logger 应为 user1-logger，got %s", logger.name)
	}

	// 父容器拿不到子容器的 Logger
	var parentLogger *Logger
	if err := root.Get(&parentLogger); !errors.Is(err, rt.ErrServiceNotFound) {
		t.Fatalf("父容器不应拿到子容器服务，got %v", err)
	}
}

func TestContainer_IsolateOverride(t *testing.T) {
	root := New()
	_ = root.Register(&Model{vendor: "openai"})

	child := root.Isolate("user-1")
	// 子容器覆盖父容器的 Model
	_ = child.Register(&Model{vendor: "anthropic"})

	var model *Model
	_ = child.Get(&model)
	if model.vendor != "anthropic" {
		t.Fatalf("子容器应拿到覆盖后的值，got %s", model.vendor)
	}

	// 父容器不受影响
	var parentModel *Model
	_ = root.Get(&parentModel)
	if parentModel.vendor != "openai" {
		t.Fatalf("父容器应不变，got %s", parentModel.vendor)
	}
}

func TestContainer_IsolateCascadeDispose(t *testing.T) {
	root := New()
	childDisposed := false

	child := root.Isolate("user-1")
	_ = child.Plugin("session", func(c *Container) (func() error, error) {
		return func() error { childDisposed = true; return nil }, nil
	})

	// 父容器 Dispose 应级联到子容器
	_ = root.Dispose()

	if !childDisposed {
		t.Fatal("父容器 Dispose 应级联卸载子容器")
	}
	if child.State() != StateDisposed {
		t.Fatalf("子容器应为 disposed，got %s", child.State())
	}
}

func TestContainer_Start(t *testing.T) {
	c := New()
	if c.State() != StateStarting {
		t.Fatalf("初始状态应为 starting，got %s", c.State())
	}

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if c.State() != StateActive {
		t.Fatalf("Start 后应为 active，got %s", c.State())
	}
}

func TestContainer_Events(t *testing.T) {
	c := New()
	bus := c.Events()
	if bus == nil {
		t.Fatal("Events 不应返回 nil")
	}

	called := false
	bus.On("test", events.DispatchSerial, func(_ context.Context, _ events.Event) (any, error) {
		called = true
		return nil, nil
	})

	_, _ = bus.Emit(context.Background(), "test", events.SimpleEvent{
		EventType: "test",
	})
	if !called {
		t.Fatal("事件应被触发")
	}
}

func TestContainer_GetPlugin(t *testing.T) {
	c := New()
	f := c.Plugin("test", func(c *Container) (func() error, error) { return nil, nil })

	if got := c.GetPlugin("test"); got != f {
		t.Fatal("GetPlugin 应返回注册的 fiber")
	}
	if got := c.GetPlugin("notexist"); got != nil {
		t.Fatal("不存在的插件应返回 nil")
	}
}

// === Phase 5: 多租户隔离补充测试 ===

// TestContainer_IsolateEventBus 验证子容器有独立 EventBus，
// 父容器的事件不会泄漏到子容器，反之亦然。
func TestContainer_IsolateEventBus(t *testing.T) {
	root := New()
	child := root.Isolate("user-1")

	rootCalled := false
	childCalled := false

	root.Events().On("ping", events.DispatchSerial, func(_ context.Context, _ events.Event) (any, error) {
		rootCalled = true
		return nil, nil
	})
	child.Events().On("ping", events.DispatchSerial, func(_ context.Context, _ events.Event) (any, error) {
		childCalled = true
		return nil, nil
	})

	// 在 root emit，child 不应收到
	_, _ = root.Events().Emit(context.Background(), "ping", nil)
	if !rootCalled {
		t.Fatal("root emit 应触发 root handler")
	}
	if childCalled {
		t.Fatal("root emit 不应触发 child handler")
	}

	// 重置后在 child emit
	rootCalled = false
	_, _ = child.Events().Emit(context.Background(), "ping", nil)
	if rootCalled {
		t.Fatal("child emit 不应触发 root handler")
	}
	if !childCalled {
		t.Fatal("child emit 应触发 child handler")
	}
}

// TestContainer_IsolateMultiLevel 验证多层 Isolate（root → tenant → session）的继承链。
func TestContainer_IsolateMultiLevel(t *testing.T) {
	root := New()
	_ = root.Register(&Model{vendor: "openai"})

	// 第一层：租户
	tenant := root.Isolate("tenant-a")
	_ = tenant.Register(&Logger{name: "tenant-logger"})

	// 第二层：会话（继承自租户，租户又继承自 root）
	session := tenant.Isolate("session-1")
	_ = session.Register(&Logger{name: "session-logger"}) // 覆盖租户的 Logger

	// session 能拿到 root 的 Model（隔代继承）
	var model *Model
	if err := session.Get(&model); err != nil {
		t.Fatalf("session 应能隔代继承 root 的 Model: %v", err)
	}
	if model.vendor != "openai" {
		t.Fatalf("Model 应为 openai，got %s", model.vendor)
	}

	// session 覆盖了 tenant 的 Logger
	var logger *Logger
	if err := session.Get(&logger); err != nil {
		t.Fatalf("session Get Logger 失败: %v", err)
	}
	if logger.name != "session-logger" {
		t.Fatalf("session 应拿到自己的 Logger，got %s", logger.name)
	}

	// tenant 拿到的是自己的 Logger（不被 session 影响）
	var tenantLogger *Logger
	if err := tenant.Get(&tenantLogger); err != nil {
		t.Fatalf("tenant Get Logger 失败: %v", err)
	}
	if tenantLogger.name != "tenant-logger" {
		t.Fatalf("tenant 应拿到自己的 Logger，got %s", tenantLogger.name)
	}
}

// TestContainer_IsolateReuse 验证同名 Isolate 返回同一实例。
func TestContainer_IsolateReuse(t *testing.T) {
	root := New()
	child1 := root.Isolate("user-1")
	child2 := root.Isolate("user-1") // 同名

	if child1 != child2 {
		t.Fatal("同名 Isolate 应返回同一实例")
	}
	if child1.Name() != "user-1" {
		t.Fatalf("Name 应为 user-1，got %s", child1.Name())
	}
}

// TestContainer_IsolateDifferentNames 验证不同名 Isolate 互相隔离。
func TestContainer_IsolateDifferentNames(t *testing.T) {
	root := New()
	alice := root.Isolate("alice")
	bob := root.Isolate("bob")

	_ = alice.Register(&Logger{name: "alice-logger"})
	_ = bob.Register(&Logger{name: "bob-logger"})

	// alice 拿不到 bob 的 Logger
	var aliceLogger *Logger
	_ = alice.Get(&aliceLogger)
	if aliceLogger.name != "alice-logger" {
		t.Fatalf("alice 应拿到自己的 Logger，got %s", aliceLogger.name)
	}

	// bob 拿不到 alice 的 Logger
	var bobLogger *Logger
	_ = bob.Get(&bobLogger)
	if bobLogger.name != "bob-logger" {
		t.Fatalf("bob 应拿到自己的 Logger，got %s", bobLogger.name)
	}
}

// TestContainer_IsolatePluginIndependent 验证子容器的插件互相独立。
func TestContainer_IsolatePluginIndependent(t *testing.T) {
	root := New()

	aliceDisposed := false
	bobDisposed := false

	alice := root.Isolate("alice")
	_ = alice.Plugin("session", func(c *Container) (func() error, error) {
		return func() error { aliceDisposed = true; return nil }, nil
	})

	bob := root.Isolate("bob")
	_ = bob.Plugin("session", func(c *Container) (func() error, error) {
		return func() error { bobDisposed = true; return nil }, nil
	})

	// 卸载 alice，bob 不应受影响
	_ = alice.Dispose()
	if !aliceDisposed {
		t.Fatal("alice 的插件应被卸载")
	}
	if bobDisposed {
		t.Fatal("bob 的插件不应被 alice dispose 影响")
	}

	// bob 仍可用
	if bob.State() != StateActive && bob.State() != StateStarting {
		t.Fatalf("bob 应仍可用，got %s", bob.State())
	}

	// 清理
	_ = root.Dispose()
}
