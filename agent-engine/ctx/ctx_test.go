package ctx

import (
	"sync"
	"testing"

	"github.com/hycjack/crux-kernel/container"
	"github.com/hycjack/crux-kernel/plugin"
	core "github.com/hycjack/crux-ai/core"
)

// ─── 测试用假插件 ────────────────────────────────────────────────────────────

type fakePlugin struct {
	name    string
	onMount func(x *Ctx) (func() error, error)
}

func (f *fakePlugin) Name() string { return f.name }
func (f *fakePlugin) Mount(x *Ctx) (func() error, error) {
	if f.onMount == nil {
		return nil, nil
	}
	return f.onMount(x)
}

// ─── TestMountAggregatesHooks：多个插件把能力挂到统一主干 ──────────────────

func TestMountAggregatesHooks(t *testing.T) {
	x := New()

	// 插件 A：挂 TransformContext
	x.Mount(&fakePlugin{name: "a", onMount: func(x *Ctx) (func() error, error) {
		x.MergeHooks(plugin.Hooks{
			TransformContext: func(msgs []core.Message) []core.Message { return msgs },
		})
		return nil, nil
	}})

	// 插件 B：挂 GetApiKey
	x.Mount(&fakePlugin{name: "b", onMount: func(x *Ctx) (func() error, error) {
		x.MergeHooks(plugin.Hooks{
			GetApiKey: func() string { return "sk-test" },
		})
		return nil, nil
	}})

	h := x.Hooks()
	if h.TransformContext == nil {
		t.Fatal("TransformContext 未被插件 A 挂载")
	}
	if h.GetApiKey == nil || h.GetApiKey() != "sk-test" {
		t.Fatal("GetApiKey 未被插件 B 挂载")
	}
}

// ─── TestScopedIsolate：scoped 上下文继承父服务、hooks 相互独立 ────────────

type markerSvc struct{ v string }

func TestScopedIsolate(t *testing.T) {
	root := New()
	root.Register(&markerSvc{v: "root"})

	root.Mount(&fakePlugin{name: "root-hook", onMount: func(x *Ctx) (func() error, error) {
		x.MergeHooks(plugin.Hooks{GetApiKey: func() string { return "root-key" }})
		return nil, nil
	}})

	child := root.Scoped("agent-1")
	if child == nil {
		t.Fatal("Scoped 返回 nil")
	}

	// 子上下文能 Get 父服务（继承只读）
	var got *markerSvc
	if err := child.Get(&got); err != nil {
		t.Fatalf("子上下文应继承父服务: %v", err)
	}
	if got == nil || got.v != "root" {
		t.Fatalf("继承的服务值不对: %v", got)
	}

	// 子上下文 hooks 独立：父的 GetApiKey 不影响子
	child.Mount(&fakePlugin{name: "child-hook", onMount: func(x *Ctx) (func() error, error) {
		x.MergeHooks(plugin.Hooks{GetApiKey: func() string { return "child-key" }})
		return nil, nil
	}})
	if child.Hooks().GetApiKey() != "child-key" {
		t.Fatal("子上下文 hooks 应独立")
	}
}

// ─── TestDisposeReverseOrder：Dispose 逆序调用各插件清理函数 ────────────────

func TestDisposeReverseOrder(t *testing.T) {
	x := New()
	var mu sync.Mutex
	var order []string

	x.Mount(&fakePlugin{name: "first", onMount: func(x *Ctx) (func() error, error) {
		return func() error {
			mu.Lock()
			order = append(order, "first")
			mu.Unlock()
			return nil
		}, nil
	}})
	x.Mount(&fakePlugin{name: "second", onMount: func(x *Ctx) (func() error, error) {
		return func() error {
			mu.Lock()
			order = append(order, "second")
			mu.Unlock()
			return nil
		}, nil
	}})

	if err := x.Dispose(); err != nil {
		t.Fatalf("Dispose: %v", err)
	}
	if x.State() != container.StateDisposed {
		t.Fatalf("Dispose 后应为 StateDisposed, got %v", x.State())
	}

	if len(order) != 2 {
		t.Fatalf("期望两个清理函数都被调用, got %d", len(order))
	}
	if order[0] != "second" || order[1] != "first" {
		t.Fatalf("Dispose 应逆序调用清理函数, got %v", order)
	}
}
