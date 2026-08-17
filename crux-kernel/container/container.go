// Package container 实现统一服务容器。
//
// Container 聚合服务注册/查询、插件生命周期（通过 fiber.PluginFiber）、
// 事件总线（通过 events.EventBus）与多租户隔离（Isolate）。
//
// 设计要点：
//   - 服务按 Go 类型注册（reflect.Type 索引），同类型覆盖时旧实例的 disposer 会被调用。
//   - 插件通过 Plugin() 注册，获得 PluginFiber 状态机管理。
//   - Dispose 逆序调用所有 disposer，保证依赖关系正确。
//   - Isolate 派生容器继承父容器服务（只读），可覆盖。
//
// 时间维度：应用级（容器从 Start 到 Dispose 的整个生命周期），
// 与 crux-turn 的 Turn FSM（对话回合级）、agent-engine 的 Agent 运行状态（会话级）互不重叠。
package container

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	rt "github.com/hycjack/crux-kernel"
	"github.com/hycjack/crux-kernel/events"
	"github.com/hycjack/crux-kernel/fiber"
)

// State 容器自身的最小生命周期状态（不是插件状态机）。
type State int

const (
	// StateStarting 注册中，未启动。
	StateStarting State = iota

	// StateActive 已启动，可使用。
	StateActive

	// StateDisposed 已卸载，不可再用。
	StateDisposed
)

// String 人类可读的状态描述。
func (s State) String() string {
	switch s {
	case StateStarting:
		return "starting"
	case StateActive:
		return "active"
	case StateDisposed:
		return "disposed"
	default:
		return "unknown"
	}
}

// Container 是 Agent 运行时的统一容器。
// 替代 AgentLoopConfig 的平铺字段，提供有状态的服务聚合 + 资源清理。
type Container struct {
	mu        sync.RWMutex
	services  map[reflect.Type]any        // 按类型注册的服务
	fibers    map[string]*fiber.PluginFiber // 按名称注册的插件
	order     []string                   // 插件注册顺序（用于 Dispose 逆序）
	events    *events.EventBus            // 事件总线
	isolates  map[string]*Container       // 派生的隔离容器
	parent    *Container                  // 父容器（isolate 场景）
	name      string                       // 容器名（root 为空，isolate 为 isolate name）
	state     State                        // 容器自身状态
}

// New 创建一个 starting 状态的空容器。
func New() *Container {
	return &Container{
		services: make(map[reflect.Type]any),
		fibers:   make(map[string]*fiber.PluginFiber),
		order:    make([]string, 0),
		events:   events.New(),
		isolates: make(map[string]*Container),
		state:    StateStarting,
	}
}

// --- 服务注册 ---

// Register 注册服务实例。按值的 Go 类型索引。
//
// 重复注册同类型会覆盖旧的：如果旧值实现了 Disposer 接口或通过 Plugin 注册，
// 旧实例的清理函数不会被自动调用（清理由 PluginFiber 管理）。
// Register 只负责服务表，不负责插件 disposer。
//
// 容器已 disposed 时返回 ErrContainerDisposed。
func (c *Container) Register(svc any) error {
	if svc == nil {
		return fmt.Errorf("container: cannot register nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == StateDisposed {
		return rt.ErrContainerDisposed
	}
	t := reflect.TypeOf(svc)
	c.services[t] = svc
	return nil
}

// RegisterAs 注册服务实例到指定类型。
//
// 典型用途：把具体实现注册到接口类型，便于按接口查询。
//   c.RegisterAs(ctxPipeline, (*plugin.ContextPlugin)(nil))
//
// typ 通常是指向接口的 nil 指针（(*Interface)(nil)）或指向具体类型的 nil 指针。
// 同一实例可以多次 RegisterAs 到不同接口类型。
//
// 容器已 disposed 时返回 ErrContainerDisposed。
func (c *Container) RegisterAs(svc any, typ any) error {
	if svc == nil {
		return fmt.Errorf("container: cannot register nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == StateDisposed {
		return rt.ErrContainerDisposed
	}
	t := reflect.TypeOf(typ)
	if t == nil {
		return fmt.Errorf("container: RegisterAs target type is nil")
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	c.services[t] = svc
	return nil
}

// Get 按类型获取服务。svc 必须是指针或接口指针（如 *Logger 或 *core.Model）。
//
// 未注册时返回 ErrServiceNotFound。
// 容器已 disposed 时返回 ErrContainerDisposed。
//
// 若当前容器未注册该类型，且有父容器（Isolate 场景），会向上查找。
func (c *Container) Get(svc any) error {
	if svc == nil {
		return fmt.Errorf("container: Get target is nil")
	}
	c.mu.RLock()
	if c.state == StateDisposed {
		c.mu.RUnlock()
		return rt.ErrContainerDisposed
	}
	t := reflect.TypeOf(svc)
	// svc 应该是 *T，我们取 Elem() 作为查找 key
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// 先在当前容器查找
	v, ok := c.services[t]
	if !ok && c.parent != nil {
		c.mu.RUnlock()
		return c.parent.Get(svc)
	}
	c.mu.RUnlock()
	if !ok {
		return rt.ErrServiceNotFound
	}
	// 把 v 写入 svc 指向的变量
	dst := reflect.ValueOf(svc)
	if dst.Kind() != reflect.Ptr {
		return fmt.Errorf("container: Get target must be a pointer, got %T", svc)
	}
	dst.Elem().Set(reflect.ValueOf(v))
	return nil
}

// MustGet 同 Get，未注册时 panic（仅用于启动期）。
// 返回值是已设置好的 svc（便于链式调用）。
func (c *Container) MustGet(svc any) any {
	if err := c.Get(svc); err != nil {
		panic(err)
	}
	return svc
}

// --- 插件管理 ---

// PluginFunc 插件加载函数。
// 返回的 disposer 会在 Container.Dispose() 时逆序调用；
// 为 nil 表示该插件无需清理。
type PluginFunc func(c *Container) (disposer func() error, err error)

// pluginFuncAdapter 把 PluginFunc 适配为 fiber.Loader。
type pluginFuncAdapter struct {
	c  *Container
	fn PluginFunc
}

func (a *pluginFuncAdapter) Load(ctx context.Context) (func() error, error) {
	return a.fn(a.c)
}

// Plugin 注册插件并管理其生命周期。
//
// 立即执行 fn 进行加载（同步），加载成功后插件进入 active 状态，
// 返回的 *PluginFiber 可用于后续 Reload/Dispose 或状态查询。
//
// 若同名插件已存在，返回的 PluginFiber 处于 failed 状态（Err 字段含原因）。
// 容器已 disposed 时返回的 PluginFiber 也处于 failed 状态。
// 调用方应检查返回的 fiber.State() 和 fiber.Err()。
func (c *Container) Plugin(name string, fn PluginFunc) *fiber.PluginFiber {
	return c.PluginWithConfig(name, nil, fn)
}

// PluginWithConfig 注册带配置的插件。config 会记录在 PluginFiber 中，
// 用于 Reload 时的 epoch 比较。
//
// 如果插件加载失败，返回的 PluginFiber 处于 failed 状态，
// 调用方应检查 fiber.State() 和 fiber.Err()。
func (c *Container) PluginWithConfig(name string, config any, fn PluginFunc) *fiber.PluginFiber {
	c.mu.Lock()
	if c.state == StateDisposed {
		c.mu.Unlock()
		// 返回一个 failed 状态的 fiber 以便调用方检查
		f := newFailedFiber(name, config, rt.ErrContainerDisposed)
		return f
	}
	if _, exists := c.fibers[name]; exists {
		c.mu.Unlock()
		f := newFailedFiber(name, config, fmt.Errorf("%w: plugin %q already exists", rt.ErrPluginNotActive, name))
		return f
	}
	f := fiber.New(name, &pluginFuncAdapter{c: c, fn: fn}, config)
	c.fibers[name] = f
	c.order = append(c.order, name)
	c.mu.Unlock()

	// 在锁外执行加载（可能耗时）
	// Note: Load error is reflected in fiber.State()/fiber.Err(),
	// callers should check the returned fiber's state.
	_ = f.Load(context.Background())
	return f
}

// newFailedFiber 创建一个直接进入 failed 状态的 fiber。
// 通过一个总是返回 err 的 loader 实现。
func newFailedFiber(name string, config any, err error) *fiber.PluginFiber {
	f := fiber.New(name, failedLoader{err: err}, config)
	_ = f.Load(context.Background())
	return f
}

// failedLoader 总是返回指定 error 的 loader。
type failedLoader struct{ err error }

func (l failedLoader) Load(_ context.Context) (func() error, error) {
	return nil, l.err
}

// ReloadPlugin 热重载：先调用旧 disposer 卸载，再用新 fn 加载。
//
// 这不是状态转换，就是"先卸后装"的组合操作。
// 若 newConfig 非 nil 且 epoch 一致，会跳过（无变化）。
// 插件不存在时返回 ErrPluginNotFound。
func (c *Container) ReloadPlugin(name string, fn PluginFunc, newConfig any, epoch string) error {
	c.mu.RLock()
	if c.state == StateDisposed {
		c.mu.RUnlock()
		return rt.ErrContainerDisposed
	}
	f, ok := c.fibers[name]
	c.mu.RUnlock()
	if !ok {
		return rt.ErrPluginNotFound
	}
	newLoader := &pluginFuncAdapter{c: c, fn: fn}
	return f.Reload(context.Background(), newLoader, newConfig, epoch)
}

// GetPlugin 按名称获取插件的 PluginFiber（用于状态查询）。
// 不存在时返回 nil。
func (c *Container) GetPlugin(name string) *fiber.PluginFiber {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.fibers[name]
}

// --- 隔离 ---

// Isolate 派生隔离容器。
//
// 新容器继承父容器的服务（只读），但可以注册自己的同名服务覆盖父容器。
// 新容器有自己的事件总线和插件列表。
// 父容器的 Dispose 会级联卸载所有 isolate。
func (c *Container) Isolate(name string) *Container {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == StateDisposed {
		return nil
	}
	if existing, ok := c.isolates[name]; ok {
		return existing
	}
	child := &Container{
		services: make(map[reflect.Type]any),
		fibers:   make(map[string]*fiber.PluginFiber),
		order:    make([]string, 0),
		events:   events.New(),
		isolates: make(map[string]*Container),
		parent:   c,
		name:     name,
		state:    StateStarting,
	}
	c.isolates[name] = child
	return child
}

// Name 返回容器名（root 为空，isolate 为 isolate name）。
func (c *Container) Name() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.name
}

// Parent 返回父容器（root 返回 nil）。
func (c *Container) Parent() *Container {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.parent
}

// --- 生命周期 ---

// Start 启动容器：状态 starting → active。
//
// 注意：Plugin 注册时立即执行 fn，Start 不触发加载。
// Start 主要用于状态标记 + 触发 "container_ready" 事件。
func (c *Container) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.state == StateDisposed {
		c.mu.Unlock()
		return rt.ErrContainerDisposed
	}
	c.state = StateActive
	c.mu.Unlock()

	// 触发 container_ready 事件
	_, _ = c.events.Emit(ctx, "container_ready", events.SimpleEvent{
		EventType:  "container_ready",
		EventTime:   time.Now(),
		EventData:   c.name,
	})
	return nil
}

// Dispose 卸载容器：逆序调用所有插件的 disposer，状态 → disposed。
//
// 级联卸载所有 isolate 子容器。
// 之后 Container 不可再用，Get/Register/Plugin 返回 ErrContainerDisposed。
// 多次调用安全（幂等）。
func (c *Container) Dispose() error {
	c.mu.Lock()
	if c.state == StateDisposed {
		c.mu.Unlock()
		return nil
	}
	c.state = StateDisposed
	// 复制一份 isolate 列表和插件顺序
	isolates := make([]*Container, 0, len(c.isolates))
	for _, child := range c.isolates {
		isolates = append(isolates, child)
	}
	order := make([]string, len(c.order))
	copy(order, c.order)
	fibers := make(map[string]*fiber.PluginFiber, len(c.fibers))
	for k, v := range c.fibers {
		fibers[k] = v
	}
	c.mu.Unlock()

	var errs []error

	// 1. 先级联卸载所有 isolate 子容器
	for _, child := range isolates {
		if err := child.Dispose(); err != nil {
			errs = append(errs, fmt.Errorf("dispose isolate %q: %w", child.Name(), err))
		}
	}

	// 2. 逆序调用插件 disposer
	for i := len(order) - 1; i >= 0; i-- {
		name := order[i]
		f := fibers[name]
		if f == nil {
			continue
		}
		if err := f.Dispose(); err != nil {
			errs = append(errs, fmt.Errorf("dispose plugin %q: %w", name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("container dispose errors: %v", errs)
	}
	return nil
}

// State 返回容器当前状态。
func (c *Container) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// --- 事件 ---

// Events 返回事件总线。
func (c *Container) Events() *events.EventBus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.events
}
