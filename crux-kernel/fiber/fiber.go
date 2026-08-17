// Package fiber 实现插件生命周期状态机。
//
// PluginFiber 管理单个插件的生命周期，状态流转：
//
//	pending → loading → active → disposed
//	                   ↘ failed
//
// 时间维度：应用级（插件从加载到卸载的整个生命周期），
// 与 crux-turn 的 Turn FSM（对话回合级）、agent-engine 的 Agent 运行状态（会话级）互不重叠。
package fiber

import (
	"context"
	"fmt"
	"sync"
)

// State 插件生命周期状态。
type State string

const (
	// StatePending 已注册，未加载。
	StatePending State = "pending"

	// StateLoading 加载中（PluginFunc 正在执行）。
	StateLoading State = "loading"

	// StateActive 已激活，disposer 已就绪。
	StateActive State = "active"

	// StateFailed 加载失败。
	StateFailed State = "failed"

	// StateDisposed 已卸载（disposer 已调用，不可再用）。
	StateDisposed State = "disposed"
)

// String 人类可读的状态描述。
func (s State) String() string { return string(s) }

// Loader 插件加载函数接口。返回 disposer（清理函数），用于 Dispose/Reload 时调用。
//
// 实现者应在 Loader 内完成所有初始化（注册服务、打开资源、启动协程等），
// 并返回一个 disposer 在被调用时清理这些资源。
type Loader interface {
	// Load 加载插件。ctx 可用于超时/取消。disposer 为 nil 表示无需清理。
	Load(ctx context.Context) (disposer func() error, err error)
}

// LoaderFunc 函数适配 Loader 接口。
type LoaderFunc func(ctx context.Context) (disposer func() error, err error)

// Load 实现 Loader 接口。
func (f LoaderFunc) Load(ctx context.Context) (func() error, error) { return f(ctx) }

// PluginFiber 管理单个插件的生命周期。
//
// 一个 PluginFiber 实例对应一个已加载的插件实例，
// 在 Container 内由 Container.Plugin/ReloadPlugin 间接管理，
// 调用方通常只读取其 State / Err 用于观察。
type PluginFiber struct {
	name    string
	state   State
	config  any    // 插件配置（用于 Reload 比较，可为 nil）
	epoch   string // 配置版本号，Reload 时变更；相同 epoch 可跳过重载
	loader  Loader
	disposer func() error
	err      error
	mu       sync.Mutex
}

// New 创建一个 pending 状态的 PluginFiber。
// name 是插件标识；loader 是加载函数；config 是插件配置（可为 nil）。
func New(name string, loader Loader, config any) *PluginFiber {
	return &PluginFiber{
		name:    name,
		state:   StatePending,
		config:  config,
		loader:  loader,
	}
}

// Name 返回插件名。
func (f *PluginFiber) Name() string { return f.name }

// State 返回当前状态（并发安全读取）。
func (f *PluginFiber) State() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

// Config 返回插件配置（只读，调用方不应修改）。
func (f *PluginFiber) Config() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.config
}

// Epoch 返回配置版本号。
func (f *PluginFiber) Epoch() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.epoch
}

// Err 返回失败原因（仅 StateFailed 时非 nil）。
func (f *PluginFiber) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// Load 执行加载流程：pending → loading → active / failed。
// 从 failed 状态也可重新 Load（重试）。多次从 active 状态 Load 会报错。
func (f *PluginFiber) Load(ctx context.Context) error {
	f.mu.Lock()
	if f.state != StatePending && f.state != StateFailed {
		f.mu.Unlock()
		return fmt.Errorf("fiber: cannot load plugin %q from state %s", f.name, f.state)
	}
	f.state = StateLoading
	f.err = nil
	loader := f.loader
	f.mu.Unlock()

	disposer, err := loader.Load(ctx)

	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		f.state = StateFailed
		f.err = err
		return fmt.Errorf("fiber: load plugin %q failed: %w", f.name, err)
	}
	f.disposer = disposer
	f.state = StateActive
	return nil
}

// Dispose 调用 disposer 并进入 disposed 状态。
// 多次调用安全（幂等）。从 active/failed 状态可调用。
func (f *PluginFiber) Dispose() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == StateDisposed {
		return nil
	}
	if f.state != StateActive && f.state != StateFailed {
		return fmt.Errorf("fiber: cannot dispose plugin %q from state %s", f.name, f.state)
	}
	var err error
	if f.disposer != nil {
		err = f.disposer()
		f.disposer = nil
	}
	f.state = StateDisposed
	return err
}

// Reload 重新加载：先 dispose 旧的（如 active），再用新 loader/config 加载。
//
// 新 loader 为 nil 时复用旧 loader；新 config 非 nil 时覆盖旧 config 并递增 epoch。
// 相同 epoch（newConfig == nil 且 epoch 未变）会跳过，返回 nil。
// 这只是组合操作，不引入额外的中间状态。
func (f *PluginFiber) Reload(ctx context.Context, newLoader Loader, newConfig any, newEpoch string) error {
	f.mu.Lock()
	if f.state != StateActive && f.state != StateFailed {
		err := fmt.Errorf("fiber: cannot reload plugin %q from state %s", f.name, f.state)
		f.mu.Unlock()
		return err
	}

	// epoch 比较：相同且未显式传入新 config → 跳过
	if newConfig == nil && newEpoch != "" && newEpoch == f.epoch {
		f.mu.Unlock()
		return nil
	}

	// 替换 loader / config / epoch
	if newLoader != nil {
		f.loader = newLoader
	}
	if newConfig != nil {
		f.config = newConfig
	}
	if newEpoch != "" {
		f.epoch = newEpoch
	}

	// 先 dispose 旧资源（如有）
	var disposeErr error
	if f.state == StateActive && f.disposer != nil {
		d := f.disposer
		f.disposer = nil
		f.state = StatePending // 临时回退，加载成功后转 active
		f.mu.Unlock()
		disposeErr = d() // capture dispose error
	} else {
		f.state = StatePending
		f.mu.Unlock()
	}

	// 重新加载
	loadErr := f.Load(ctx)

	// If both dispose and load failed, return a combined error
	if disposeErr != nil && loadErr != nil {
		return fmt.Errorf("fiber: reload plugin %q failed: dispose error: %v, load error: %v", f.name, disposeErr, loadErr)
	}
	if disposeErr != nil {
		return fmt.Errorf("fiber: reload plugin %q: dispose error: %w", f.name, disposeErr)
	}
	return loadErr
}

// IsActive 便捷判断是否处于 active 状态。
func (f *PluginFiber) IsActive() bool { return f.State() == StateActive }
