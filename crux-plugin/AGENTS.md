# crux-plugin

Pure plugin core. JSON-RPC 2.0 over stdio, process boundary per plugin.

## Zero dependencies

This module depends only on Go stdlib. No external imports. Any host
framework can vendor it without pulling in crux-harness / hooks / etc.

## What lives here

| File | Lines | Purpose |
|---|---|---|
| `protocol.go` | ~235 | JSON-RPC 2.0 frames + 9 method constants + Param/Result structs + HookCallTimeout |
| `process.go` | ~325 | Subprocess fork + stdin/stdout/stderr pipes + pending request map + scanner |
| `manager.go` | ~300 | Discover + ApplyConfig + StartAll + StopAll + handleNotification + Logger() |
| `tooladapter.go` | ~85 | ToolAdapter struct + Manager.RegisterPluginTools (pure plugin concept, no hooks) |

## What does NOT live here

- `HookPluginConsumer` (adapts plugin to `hooks.Consumer`) → lives in
  `crux-harness/plugin/` because it depends on `crux-harness/hooks`.
- Tool/hook registration into a specific host framework (skill registry,
  dispatch, fanout) — the caller decides where ToolAdapter / Consumer land.

## Public API (what a host will call)

```go
mgr := plugin.NewManager(logger)
mgr.Discover([]string{"~/.myapp/plugins"})

mgr.ApplyConfig(map[string]plugin.PluginConfig{
    "my-plugin": {Enabled: true, Config: map[string]interface{}{"key": "value"}},
})

ctx, cancel := context.WithCancel(context.Background())
defer cancel()
mgr.StartAll(ctx)
defer mgr.StopAll()

// Tools (host registers into its own framework):
tools, _ := mgr.RegisterPluginTools(ctx)
for _, t := range tools {
    // t.Name: "<pluginID>.<toolName>"
    // t.Execute(ctx, args) → result string
}

// Hooks (host wraps into its own consumer interface, e.g. hooks.Consumer):
// → use a host-specific adapter package (see crux-harness/plugin for an example)
```

## Protocol (9 methods, JSON-RPC 2.0)

Host → plugin (request, has id):
- `initialize` — handshake + config injection
- `shutdown` — graceful stop
- `channel.send` — outbound message through plugin channel
- `tool.list` — query plugin's tools
- `tool.execute` — invoke plugin tool
- `hook.register` — query desired hook points
- `hook.fire` — trigger hook (sync or async by id presence)

Plugin → host (notification, no id):
- `message.inbound` — channel plugin reports user message
- `chat.send` — hook plugin pushes follow-up message

`HookCallTimeout` (10s) is the deadline for synchronous RPC calls. Async
notifications have no deadline.

## Hook points (v1, 3 of 5 fastclaw supports)

| Hook point | Mode | Constant |
|---|---|---|
| `before_tool_call` | sync | `HookPointBeforeToolCall` |
| `after_tool_call` | async | `HookPointAfterToolCall` |
| `turn_end` | async | `HookPointTurnEnd` |

v1 does NOT support `before_model_call` / `after_model_call` — requires
host-side `hooks.Event` schema extension with Messages. v2 work.

## Concurrency

| Operation | Concurrent-safe? |
|---|---|
| `Process.Call` | yes (mutex + atomic ID) |
| `Process.Notify` | yes |
| `Process.Stop` | idempotent (checks `running`) |
| `Manager.Discover` | no (startup-only) |
| `Manager.ApplyConfig` | yes (RLock) |
| `Manager.StartAll` / `StopAll` | no (startup/shutdown) |

## Stop() deadlock gotcha

`Stop()` must NOT hold `p.mu` while calling `Call()` (would deadlock —
Call needs the lock to write stdin). The implementation captures state
under lock, releases, then calls shutdown RPC outside the lock.

## How to plug into a host

Generic pattern:

```go
import "github.com/hycjack/crux-plugin"

mgr := plugin.NewManager(logger)
mgr.Discover([]string{"~/.myapp/plugins"})
mgr.StartAll(ctx)

// Tools → your tool registry:
for _, t := range mgr.RegisterPluginTools(ctx) {
    myRegistry.Register(t.Name, t.Description, t.Parameters, t.Execute)
}

// Hooks → your event consumer interface (here's a hooks.Fanout example):
for _, c := range myHookAdapter.WrapPlugins(mgr) {
    fanout.Add(c)
}
```

The adapter for `hooks.Consumer` lives in `crux-harness/plugin` —
copy that pattern for any other consumer interface.

## Reference plugin

`/root/crux/plugins/crux-plugin-mem/` is a 250-line Go binary that logs
every hook.fire to `~/.crux/demo-memory.log`. Build with
`go build -o crux-plugin-mem .` and drop into a plugin directory.

## Test command

```bash
cd /root/crux && go build -o /tmp/plugin-host ./crux-harness/cmd/plugin-host/
/tmp/plugin-host -plugins-dir /root/crux/plugins -emit turn_end
/tmp/plugin-host -plugins-dir /root/crux/plugins -emit before_tool_call
/tmp/plugin-host -plugins-dir /root/crux/plugins -emit after_tool_call
cat ~/.crux/demo-memory.log  # each event recorded as JSON
```

## v2 candidates (explicit)

Tracked in docs/modules/29-crux-plugin.md:
- V2-1: `before_model_call` / `after_model_call` (extend host Event schema)
- V2-2: `provider.list` / `provider.execute`
- V2-3: cross-language SDKs (Python/Node)
- V2-4: sandbox (wasm/firecracker)
- V2-5: hot-reload
- V2-6: Hub registry
- V2-7: npm bridge
- V2-8: plugin inter-call
- V2-9: per-hook sync/async config
- V2-10: manifest signature verification
- V2-11: chat.send → bus.Outbound wire-up
- V2-12: multi-user isolation