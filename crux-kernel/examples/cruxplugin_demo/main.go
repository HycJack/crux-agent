// Example: 用 crux-kernel 管理 crux-plugin 子进程插件
//
// 展示如何用 ProcessFiber + AutoRestart + Reload 管理子进程插件生命周期：
//   1. 编译 fake_plugin 为 exe
//   2. 用 ProcessFiber 包装为 fiber.Loader
//   3. 用 AutoRestart 包装（崩溃自动重启）
//   4. 用 fiber.PluginFiber 管理生命周期
//   5. 演示 Reload（热重载）
//
// 运行：go run ./examples/cruxplugin_demo
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	cruxplugin "github.com/hycjack/crux-plugin"
	"github.com/hycjack/crux-kernel/fiber"
	cruxrtplugin "github.com/hycjack/crux-kernel/integration/cruxplugin"
)

func main() {
	// 1. 编译 fake_plugin
	exePath := buildFakePlugin()
	defer os.Remove(exePath)

	// 2. 构造 manifest
	manifest := &cruxplugin.Manifest{
		ID:      "demo-plugin",
		Name:    "Demo Plugin",
		Version: "0.1.0",
		Type:    "tool",
		Command: exePath,
		Dir:     ".",
	}

	// 3. 创建 ProcessFiber（不带 AutoRestart）
	pf := cruxrtplugin.NewProcessFiber(manifest, map[string]any{"key": "value"}, nil)
	f := fiber.New("demo-plugin", pf, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 4. Load
	fmt.Fprintf(os.Stderr, "=== Load ===\n")
	if err := f.Load(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Load 失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "State: %s\n", f.State())
	fmt.Fprintf(os.Stderr, "Process running: %v\n", pf.Process() != nil && pf.Process().IsRunning())

	// 5. Reload（热重载）
	fmt.Fprintf(os.Stderr, "\n=== Reload ===\n")
	newCfg := map[string]any{"reloaded": true, "ts": time.Now().Unix()}
	if err := cruxrtplugin.Reload(ctx, f, newCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Reload 失败: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "Reload 后 State: %s\n", f.State())
		fmt.Fprintf(os.Stderr, "Reload 后 epoch: %s\n", f.Epoch())
	}

	// 6. Dispose
	fmt.Fprintf(os.Stderr, "\n=== Dispose ===\n")
	if err := f.Dispose(); err != nil {
		fmt.Fprintf(os.Stderr, "Dispose 失败: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "State: %s\n", f.State())

	// 7. 演示 AutoRestart
	fmt.Fprintf(os.Stderr, "\n=== AutoRestart 演示 ===\n")
	pf2 := cruxrtplugin.NewProcessFiber(manifest, nil, nil)
	loader := cruxrtplugin.AutoRestart(pf2, cruxrtplugin.AutoRestartOpts{
		MaxRestarts:    3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
	})
	f2 := fiber.New("demo-plugin-auto", loader, nil)

	if err := f2.Load(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "AutoRestart Load 失败: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "AutoRestart State: %s\n", f2.State())
		fmt.Fprintf(os.Stderr, "子进程运行: %v\n", pf2.Process() != nil && pf2.Process().IsRunning())
		_ = f2.Dispose()
		fmt.Fprintf(os.Stderr, "Dispose 后 State: %s\n", f2.State())
	}

	fmt.Fprintf(os.Stderr, "\n=== 完成 ===\n")
}

// buildFakePlugin 编译 fake_plugin 为可执行文件。
// fake_plugin 位于 examples/fake_plugin/（本示例的兄弟目录）。
func buildFakePlugin() string {
	_, thisFile, _, _ := runtime.Caller(0)
	srcDir := filepath.Dir(thisFile)
	// 上一级是 examples/，fake_plugin 是其子目录
	srcPath := filepath.Join(filepath.Dir(srcDir), "fake_plugin", "main.go")

	exePath := filepath.Join(os.TempDir(), "fake_plugin_demo.exe")
	cmd := exec.Command("go", "build", "-o", exePath, srcPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "编译 fake_plugin 失败: %v\n%s\n", err, out)
		os.Exit(1)
	}
	return exePath
}
