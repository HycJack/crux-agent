// Package kernel 提供 crux-plugin 与 crux-kernel 的桥接。
//
// 把 crux-plugin 的 *plugin.Process 包装为 crux-kernel 的 fiber.PluginFiber，
// 实现以下能力：
//
//  1. ProcessFiber — 把单个 Process 包装为 Loader，使其具备完整生命周期管理
//     （pending → loading → active → disposed）
//  2. AutoRestart — 子进程崩溃后自动重启（带指数退避）
//  3. Reload — 配置变更后热重载（dispose 旧 process + 用新 config 启动）
//
// 集成代码迁移自 crux-kernel/integration/cruxplugin。这样 crux-kernel 不再依赖
// crux-plugin；依赖方向反转为 crux-plugin → crux-kernel（底座层）。
package kernel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	cruxplugin "github.com/hycjack/crux-plugin"
	"github.com/hycjack/crux-kernel/fiber"
)

// shutdownTimeout 是 Stop 的默认超时（与 crux-plugin 保持一致）。
const shutdownTimeout = 5 * time.Second

// ProcessFiber 把 *cruxplugin.Process 包装为 fiber.Loader。
//
// Load 时启动子进程并发送 initialize；Dispose 时发送 shutdown 并杀掉子进程。
type ProcessFiber struct {
	manifest *cruxplugin.Manifest
	config   map[string]any
	logger   *slog.Logger

	// process 在 Load 成功后设置；Dispose 后置 nil
	process *cruxplugin.Process
	mu      sync.Mutex

	// crashHandler 子进程崩溃后调用（用于 AutoRestart）
	crashHandler func(err error)
}

// NewProcessFiber 创建一个 ProcessFiber。
//
// manifest 必须非 nil；config 是 initialize 时传给子进程的配置（可为 nil）；
// logger 为 nil 时使用 slog.Default()。
func NewProcessFiber(m *cruxplugin.Manifest, config map[string]any, logger *slog.Logger) *ProcessFiber {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProcessFiber{
		manifest: m,
		config:   config,
		logger:   logger.With("plugin", m.ID),
	}
}

// Load 实现 fiber.Loader 接口。
//
// 启动子进程 → 发送 initialize RPC → 返回 disposer。
// 失败时确保子进程被清理。
func (p *ProcessFiber) Load(ctx context.Context) (func() error, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.process != nil {
		return nil, fmt.Errorf("cruxplugin: process already started for %q", p.manifest.ID)
	}

	proc := cruxplugin.NewProcess(p.manifest, p.logger)
	if err := proc.Start(ctx); err != nil {
		return nil, fmt.Errorf("cruxplugin: start process %q: %w", p.manifest.ID, err)
	}

	cfg := p.config
	if cfg == nil {
		cfg = map[string]any{}
	}
	if _, err := proc.Call(ctx, cruxplugin.MethodInitialize, cruxplugin.InitializeParams{Config: cfg}); err != nil {
		// 初始化失败，回滚：杀子进程
		proc.Stop(shutdownTimeout)
		return nil, fmt.Errorf("cruxplugin: initialize %q: %w", p.manifest.ID, err)
	}

	p.process = proc
	p.logger.Info("cruxplugin: process loaded", "pid", proc.Manifest().ID)

	// disposer：调用 shutdown 并停止子进程
	disposer := func() error {
		p.mu.Lock()
		proc := p.process
		p.process = nil
		p.mu.Unlock()

		if proc == nil {
			return nil
		}
		proc.Stop(shutdownTimeout)
		return nil
	}
	return disposer, nil
}

// Process 返回当前 *cruxplugin.Process（active 状态下非 nil）。
// 调用方可用它发送 Call/Notify。
func (p *ProcessFiber) Process() *cruxplugin.Process {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.process
}

// SetCrashHandler 设置子进程崩溃回调（由 AutoRestart 使用）。
func (p *ProcessFiber) SetCrashHandler(h func(err error)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.crashHandler = h
}

// --- AutoRestart ---

// AutoRestart 把一个 *ProcessFiber 包装为带自动重启能力的 fiber.Loader。
//
// 子进程退出（IsRunning() 为 false）后会触发回调链：
//  1. 调用原始 disposer（清理 process 状态）
//  2. 调用 backoff 等待
//  3. 重新 Load
//
// 重启次数达到 maxRestarts 后停止，进入 failed 状态。
//
// 返回值是一个新的 fiber.Loader，调用方应把它传给 fiber.New()。
func AutoRestart(inner *ProcessFiber, opts AutoRestartOpts) fiber.Loader {
	if opts.MaxRestarts == 0 {
		opts.MaxRestarts = 3
	}
	if opts.InitialBackoff == 0 {
		opts.InitialBackoff = 100 * time.Millisecond
	}
	if opts.MaxBackoff == 0 {
		opts.MaxBackoff = 5 * time.Second
	}

	return fiber.LoaderFunc(func(ctx context.Context) (func() error, error) {
		// 记录崩溃检测 goroutine 的 cancel
		var restarts atomic.Int64
		var stopCrashDetector atomic.Bool
		var innerDisposer func() error

		disposer, err := inner.Load(ctx)
		if err != nil {
			return nil, err
		}
		innerDisposer = disposer

		// 启动崩溃检测 goroutine
		detectorDone := make(chan struct{})
		go func() {
			defer close(detectorDone)
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()

			for {
				if stopCrashDetector.Load() {
					return
				}
				proc := inner.Process()
				if proc != nil && !proc.IsRunning() {
					// 子进程崩溃
					if int(restarts.Add(1)) > opts.MaxRestarts {
						inner.logger.Error("cruxplugin: max restarts exceeded",
							"plugin", inner.manifest.ID,
							"restarts", restarts.Load()-1)
						return
					}

					// 调用原始 disposer 清理状态
					if innerDisposer != nil {
						_ = innerDisposer()
					}

					// backoff
					backoff := opts.InitialBackoff << (restarts.Load() - 1)
					if backoff > opts.MaxBackoff {
						backoff = opts.MaxBackoff
					}
					select {
					case <-time.After(backoff):
					case <-ctx.Done():
						return
					}

					// 重新 Load
					d, err := inner.Load(ctx)
					if err != nil {
						inner.logger.Error("cruxplugin: restart failed",
							"plugin", inner.manifest.ID,
							"err", err)
						return
					}
					innerDisposer = d
					inner.logger.Info("cruxplugin: restarted",
						"plugin", inner.manifest.ID,
						"restarts", restarts.Load())
					continue
				}
				<-ticker.C
			}
		}()

		// 返回组合 disposer
		return func() error {
			stopCrashDetector.Store(true)
			<-detectorDone
			if innerDisposer != nil {
				return innerDisposer()
			}
			return nil
		}, nil
	})
}

// AutoRestartOpts 配置 AutoRestart 行为。
type AutoRestartOpts struct {
	// MaxRestarts 最大重启次数（0 表示默认 3）。
	// 达到上限后不再重启，fiber 进入 active 状态但子进程已死。
	MaxRestarts int

	// InitialBackoff 首次重启退避（0 表示 100ms）。
	InitialBackoff time.Duration

	// MaxBackoff 退避上限（0 表示 5s）。
	MaxBackoff time.Duration
}

// --- Reload ---

// Reload 热重载插件：dispose 旧 fiber + 用新 config 重新 Load。
//
// 典型场景：plugin.json 或 config.yaml 变更后调用此函数。
//
// 若 fiber 当前状态不是 active，返回错误。
// 若 newConfig 为 nil，沿用旧 config（但不会跳过 Load）。
func Reload(ctx context.Context, f *fiber.PluginFiber, newConfig map[string]any) error {
	if f == nil {
		return errors.New("cruxplugin: fiber is nil")
	}
	if f.State() != fiber.StateActive && f.State() != fiber.StateFailed {
		return fmt.Errorf("cruxplugin: cannot reload from state %s", f.State())
	}

	// 注意：newConfig 直接传给 Reload，fiber 内部不会用它构造 Loader
	// 需要在 Fiber 创建时把 config 通过闭包传给 ProcessFiber
	return f.Reload(ctx, nil, newConfig, fmt.Sprintf("reload-%d", time.Now().UnixNano()))
}

// ErrNotRunning 子进程未运行时调用 Call 返回。
var ErrNotRunning = errors.New("cruxplugin: process not running")
