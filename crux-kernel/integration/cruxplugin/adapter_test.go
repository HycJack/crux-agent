package cruxplugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	cruxplugin "github.com/hycjack/crux-plugin"
	"github.com/hycjack/crux-kernel/fiber"
)

// buildFakePlugin 编译 fake_plugin 为可执行文件，返回其路径。
// 用 exe 路径作为 manifest.Command，避免 "go run" 路径含空格被 strings.Fields 拆分。
func buildFakePlugin(t *testing.T) string {
	t.Helper()
	_, testFile, _, _ := runtime.Caller(0)
	srcDir := filepath.Dir(testFile)
	srcPath := filepath.Join(srcDir, "..", "..", "examples", "fake_plugin", "main.go")
	if _, err := os.Stat(srcPath); err != nil {
		t.Fatalf("fake_plugin 源码不存在: %v", err)
	}

	exePath := filepath.Join(t.TempDir(), "fake_plugin.exe")
	cmd := exec.Command("go", "build", "-o", exePath, srcPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译 fake_plugin 失败: %v\n%s", err, out)
	}
	return exePath
}

func newFakeManifest(t *testing.T) *cruxplugin.Manifest {
	return &cruxplugin.Manifest{
		ID:      "fake-plugin",
		Name:    "Fake Plugin",
		Version: "0.1.0",
		Type:    "tool",
		Command: buildFakePlugin(t),
		Dir:     ".",
	}
}

// --- ProcessFiber 基础生命周期 ---

func TestProcessFiber_LoadAndDispose(t *testing.T) {
	manifest := newFakeManifest(t)
	pf := NewProcessFiber(manifest, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Load
	disposer, err := pf.Load(ctx)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if pf.Process() == nil {
		t.Fatal("Load 后 Process 应非 nil")
	}
	if !pf.Process().IsRunning() {
		t.Fatal("子进程应处于运行状态")
	}

	// Dispose
	if err := disposer(); err != nil {
		t.Fatalf("disposer 出错: %v", err)
	}
	if pf.Process() != nil {
		t.Fatal("Dispose 后 Process 应为 nil")
	}
}

func TestProcessFiber_DoubleLoad(t *testing.T) {
	manifest := newFakeManifest(t)
	pf := NewProcessFiber(manifest, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	disposer, err := pf.Load(ctx)
	if err != nil {
		t.Fatalf("初次 Load 失败: %v", err)
	}
	defer func() {
		if disposer != nil {
			disposer()
		}
	}()

	// 第二次 Load 应失败
	_, err = pf.Load(ctx)
	if err == nil {
		t.Fatal("双重 Load 应返回错误")
	}
}

func TestProcessFiber_WithConfig(t *testing.T) {
	manifest := newFakeManifest(t)
	pf := NewProcessFiber(manifest, map[string]any{"key": "value"}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	disposer, err := pf.Load(ctx)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	defer disposer()
}

// --- PluginFiber 集成 ---

func TestPluginFiber_WithProcessFiber(t *testing.T) {
	manifest := newFakeManifest(t)
	pf := NewProcessFiber(manifest, map[string]any{"key": "value"}, nil)

	// 包装为 fiber.PluginFiber
	f := fiber.New("fake-plugin", pf, nil)

	if f.State() != fiber.StatePending {
		t.Fatalf("初始状态应为 pending，got %s", f.State())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Load
	if err := f.Load(ctx); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if f.State() != fiber.StateActive {
		t.Fatalf("Load 后应为 active，got %s", f.State())
	}

	// Dispose
	if err := f.Dispose(); err != nil {
		t.Fatalf("Dispose 失败: %v", err)
	}
	if f.State() != fiber.StateDisposed {
		t.Fatalf("Dispose 后应为 disposed，got %s", f.State())
	}
}

func TestPluginFiber_Reload(t *testing.T) {
	manifest := newFakeManifest(t)
	pf := NewProcessFiber(manifest, nil, nil)
	f := fiber.New("fake-plugin", pf, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 初次 Load
	if err := f.Load(ctx); err != nil {
		t.Fatalf("初次 Load 失败: %v", err)
	}
	if f.State() != fiber.StateActive {
		t.Fatalf("应为 active，got %s", f.State())
	}

	// Reload（用新 config）
	newCfg := map[string]any{"reloaded": true}
	if err := Reload(ctx, f, newCfg); err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}
	if f.State() != fiber.StateActive {
		t.Fatalf("Reload 后应为 active，got %s", f.State())
	}

	// 验证 epoch 已变更
	if f.Epoch() == "" {
		t.Fatal("Reload 后 epoch 应非空")
	}

	// 清理
	_ = f.Dispose()
}

func TestPluginFiber_LoadFailed(t *testing.T) {
	// 用一个不存在的 command 触发 Load 失败
	manifest := &cruxplugin.Manifest{
		ID:      "bad-plugin",
		Name:    "Bad",
		Version: "0.1.0",
		Type:    "tool",
		Command: "nonexistent-command-xxx-yyy",
		Dir:     ".",
	}
	pf := NewProcessFiber(manifest, nil, nil)
	f := fiber.New("bad-plugin", pf, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := f.Load(ctx)
	if err == nil {
		t.Fatal("Load 应失败")
	}
	if f.State() != fiber.StateFailed {
		t.Fatalf("应为 failed，got %s", f.State())
	}
	if f.Err() == nil {
		t.Fatal("Err 应非 nil")
	}
}

// --- AutoRestart 包装（不模拟崩溃，只验证包装后能正常 Load/Dispose）---

func TestAutoRestart_LoadAndDispose(t *testing.T) {
	manifest := newFakeManifest(t)
	pf := NewProcessFiber(manifest, nil, nil)

	loader := AutoRestart(pf, AutoRestartOpts{
		MaxRestarts:    3,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
	})

	f := fiber.New("fake-plugin", loader, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := f.Load(ctx); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if f.State() != fiber.StateActive {
		t.Fatalf("应为 active，got %s", f.State())
	}
	if pf.Process() == nil || !pf.Process().IsRunning() {
		t.Fatal("子进程应运行")
	}

	// Dispose 应停止崩溃检测器 + 子进程
	if err := f.Dispose(); err != nil {
		t.Fatalf("Dispose 失败: %v", err)
	}
	if f.State() != fiber.StateDisposed {
		t.Fatalf("应为 disposed，got %s", f.State())
	}
}
