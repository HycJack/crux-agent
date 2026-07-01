# crux-mcp

A standalone, zero-dependency Go client library for the **Model
Context Protocol (MCP)** — the open standard (modelcontextprotocol.io)
for connecting LLMs to external tools and data sources. Implements
3 methods (initialize / tools/list / tools/call) over 2 transports
(stdio subprocess + HTTP POST with optional SSE response).

## Zero dependencies

This module depends only on Go stdlib (`encoding/json`, `bufio`,
`net/http`, `os/exec`, `sync`). Any host framework can vendor it
without dragging in crux-harness / hooks / providers / ai.

## Module layout

```
crux-mcp/
  protocol.go         # JSON-RPC 2.0 + MCP types (~180 lines)
  client.go           # Client/Transport interfaces (~90 lines)
  stdio.go            # StdioClient — subprocess + scanner + ID loop (~270 lines)
  http.go             # HTTPClient — POST + SSE response parsing (~250 lines)
  manager.go          # Multi-server Manager + tool prefixing (~220 lines)
  protocol_test.go    # 8 stdio scenarios
  http_test.go        # 6 HTTP scenarios + 7 Manager scenarios
  testutil_test.go    # Shared test helpers
  cmd/demo/
    main.go           # 9 end-to-end scenarios
    fakeserver/
      main.go         # Tiny in-tree MCP server (stdio + http modes)
  AGENTS.md           # this file
```

Total: ~700 lines lib + 250 lines demo + 50 lines fakeserver + 600 lines tests.

## Public API

```go
import "github.com/hycjack/crux-mcp"

// High-level entry point — recommended for hosts.
mgr, err := mcp.NewManager(ctx, []mcp.ServerSpec{
    {Name: "github", Stdio: &mcp.StdioSpec{Command: "gh-mcp"}},
    {Name: "search", HTTP: &mcp.HTTPSpec{URL: "https://mcp.example.com/mcp",
        Headers: map[string]string{"Authorization": "Bearer $TOKEN"}}},
})
if mcp.IsPartialSuccess(err) { /* some servers failed, manager is still usable */ }

tools, _ := mgr.Tools(ctx)                  // prefixed: "mcp_github_*", "mcp_search_*"
result, _ := mgr.Call(ctx, "mcp_github_create_issue", json.RawMessage(`{"title":"x"}`))

defer mgr.Close()

// Low-level — one Client at a time.
client := mcp.NewHTTPClient(url, headers, "client-name", "v1.0", mcp.WithHTTPTimeout(30*time.Second))
client.Connect(ctx)
defer client.Close()
```

## Protocol surface (MCP 2025-06-18)

| Method | Direction | Implementation |
|---|---|---|
| `initialize` | client → server | `Client.Connect` |
| `tools/list` | client → server | `Client.ListTools` |
| `tools/call` | client → server | `Client.CallTool` |

Out of scope for v1 (deferred): `resources/*`, `prompts/*`,
notifications, sampling, elicitation.

## Transports

**Stdio** — `NewStdioClient(command, args, env, name, version)`. Spawns
a subprocess, writes one JSON-RPC request per line to stdin, scans
stdout line-by-line, matches responses to requests by ID. `$VAR`
expansion in env values at connect time.

**HTTP** — `NewHTTPClient(url, headers, name, version)`. POST with
JSON-RPC body. Response mode detected via `Content-Type`:

- `application/json` → unmarshal directly
- `text/event-stream` → scan `data:` events, match by ID (last event with matching ID is the response)

`WithHTTPTimeout(d)` option for per-request deadlines (zero = no timeout).

## Tool prefixing

Manager prefixes every tool as `mcp_<sanitizedServer>_<tool>`. Server
name is sanitized: non-`[a-zA-Z0-9_]` chars → `_`. This prevents
name collisions when multiple servers each expose a `search` tool.

Examples:

| Server | Tool | Prefixed |
|---|---|---|
| `github` | `create_issue` | `mcp_github_create_issue` |
| `my-server` | `x` | `mcp_my_server_x` |
| `weird.name` | `x` | `mcp_weird_name_x` |

`Manager.Call(prefixedName, args)` reverses the prefix to find the
right client + original name. Unknown prefix → error.

## Error handling

| Source | Returned as |
|---|---|
| JSON-RPC `error` field | `*RPCError` (Code + Message) — caller's `err` is non-nil |
| Transport-level (subprocess exit, HTTP non-2xx) | wrapped error |
| Schema validation (decode failure) | wrapped error |
| Unknown prefixed tool name | `fmt.Errorf("unknown tool %q ...")` |
| Tool returned `IsError: true` | `(result, nil)` — caller checks `result.IsError` |

## Concurrency

| Operation | Safe? |
|---|---|
| `Client.Connect` | No (one-shot state transition) |
| `Client.ListTools` / `CallTool` | Yes (mutex-serialized) |
| `Client.Close` | Yes (idempotent) |
| `Manager.Tools` / `ToolMap` / `Call` | Yes (RLock + per-client mutex) |
| `Manager.Close` | Yes |

## Test commands

```bash
# Unit tests (21 scenarios, ~1s, all green)
go test -race ./crux-mcp/...

# End-to-end demo (9 scenarios, ~2s)
go run ./crux-mcp/cmd/demo/

# Coverage
go test -race -cover ./crux-mcp/...
```

## Test strategy

Stdlib subprocess fake servers (POSIX-shell scripts for stdio, a
real Go binary `cmd/demo/fakeserver` for HTTP) drive 21 unit tests
+ 9 demo scenarios. No external MCP server required.

## Reference inspiration

Modeled on fastclaw's `internal/mcp/` (4 files, ~525 lines), but
extracted to a zero-dep module. Improvements over fastclaw:

- HTTP timeout option (fastclaw: bare `&http.Client{}`, no timeout)
- SSE response parsing (fastclaw: JSON only)
- Generic `ServerSpec` (fastclaw: coupled to `config.MCPServerConfig`)
- Partial-success semantics (fastclaw: any failure stops startup)

## v2 candidates

- Resources (`resources/list`, `resources/read`)
- Prompts (`prompts/list`, `prompts/get`)
- Server → client notifications
- WebSocket transport
- OAuth flow helpers
- Tool annotations (`readOnlyHint`, `destructiveHint`)
- Per-call timeout (not just transport-level)