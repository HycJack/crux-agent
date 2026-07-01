package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/hycjack/crux-mcp"
)

// fakeServerScript is a tiny POSIX-shell MCP server used as the
// stdio subprocess for client tests. It implements the three
// methods we care about (initialize / tools/list / tools/call) plus
// a built-in "method not found" reply for error-path testing.
//
// The script writes responses line-by-line (one JSON object per line)
// to stdout. The StdioClient's scanner reads them and matches by ID.
const fakeServerScript = `#!/bin/sh
# Tiny MCP server for tests. Reads JSON-RPC requests on stdin,
# writes responses on stdout. Implements 3 methods + error case.

read_request() {
    # Read one line from stdin.
    read -r line
    echo "$line"
}

send_response() {
    # $1 = id, $2 = result JSON
    printf '{"jsonrpc":"2.0","id":%s,"result":%s}\n' "$1" "$2"
}

send_error() {
    # $1 = id, $2 = code, $3 = message
    printf '{"jsonrpc":"2.0","id":%s,"error":{"code":%s,"message":"%s"}}\n' "$1" "$2" "$3"
}

while read -r line; do
    [ -z "$line" ] && continue

    # Extract method + id with grep/sed (no jq dependency).
    method=$(echo "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
    id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')

    case "$method" in
        initialize)
            send_response "$id" '{"protocolVersion":"2025-06-18","serverInfo":{"name":"fake","version":"0.1.0"},"capabilities":{"tools":{}}}'
            ;;
        tools/list)
            send_response "$id" '{"tools":[{"name":"echo","description":"echoes args","inputSchema":{"type":"object"}},{"name":"fail","description":"always fails","inputSchema":{"type":"object"}}]}'
            ;;
        tools/call)
            # Extract tool name from params.
            tool=$(echo "$line" | sed -n 's/.*"name":"\([^"]*\)".*/\1/p')
            if [ "$tool" = "fail" ]; then
                send_response "$id" '{"content":[{"type":"text","text":"intentional failure"}],"isError":true}'
            else
                send_response "$id" '{"content":[{"type":"text","text":"hello"}]}'
            fi
            ;;
        *)
            send_error "$id" "-32601" "method not found"
            ;;
    esac
done
`

// writeFakeServerScript writes the fake script to a temp file and
// returns the path. The caller is responsible for cleanup.
func writeFakeServerScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/fake-mcp.sh"
	if err := writeFile(path, fakeServerScript, 0o755); err != nil {
		t.Fatalf("write fake script: %v", err)
	}
	return path
}

// writeFile is a small helper that wraps os.WriteFile so tests don't
// have to import os directly (keeps the imports tidy).
func writeFile(path, content string, mode uint32) error {
	return writeFileImpl(path, []byte(content), mode)
}

// StdioClient tests use /bin/sh which is available on every Unix
// we target. The fake script exercises the happy path, the
// tools/list, and the IsError=true return path.

func TestStdioClient_ConnectAndServerInfo(t *testing.T) {
	script := writeFakeServerScript(t)
	c := mcp.NewStdioClient("/bin/sh", []string{script}, nil, "test-client", "0.0.1")
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	info := c.ServerInfo()
	if info.Name != "fake" {
		t.Errorf("ServerInfo.Name = %q, want %q", info.Name, "fake")
	}
	if info.Version != "0.1.0" {
		t.Errorf("ServerInfo.Version = %q, want %q", info.Version, "0.1.0")
	}
	if info.ProtocolVersion != "2025-06-18" {
		t.Errorf("ServerInfo.ProtocolVersion = %q, want %q", info.ProtocolVersion, "2025-06-18")
	}
	if _, ok := info.Capabilities["tools"]; !ok {
		t.Errorf("ServerInfo.Capabilities missing 'tools' key: %+v", info.Capabilities)
	}
}

func TestStdioClient_ListTools(t *testing.T) {
	script := writeFakeServerScript(t)
	c := mcp.NewStdioClient("/bin/sh", []string{script}, nil, "test-client", "0.0.1")
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("len(tools) = %d, want 2", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Errorf("tools[0].Name = %q, want %q", tools[0].Name, "echo")
	}
	if tools[1].Name != "fail" {
		t.Errorf("tools[1].Name = %q, want %q", tools[1].Name, "fail")
	}
}

func TestStdioClient_CallToolHappyPath(t *testing.T) {
	script := writeFakeServerScript(t)
	c := mcp.NewStdioClient("/bin/sh", []string{script}, nil, "test-client", "0.0.1")
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	result, err := c.CallTool(ctx, "echo", json.RawMessage(`{"msg":"hi"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Errorf("result.IsError = true, want false")
	}
	if len(result.Content) != 1 {
		t.Fatalf("len(result.Content) = %d, want 1", len(result.Content))
	}
	if result.Content[0].Text != "hello" {
		t.Errorf("result.Content[0].Text = %q, want %q", result.Content[0].Text, "hello")
	}
}

func TestStdioClient_CallToolIsErrorPropagated(t *testing.T) {
	script := writeFakeServerScript(t)
	c := mcp.NewStdioClient("/bin/sh", []string{script}, nil, "test-client", "0.0.1")
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// "fail" tool returns IsError=true. Transport returns (result, nil)
	// — the caller checks IsError to decide how to surface.
	result, err := c.CallTool(ctx, "fail", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallTool returned transport error: %v", err)
	}
	if !result.IsError {
		t.Errorf("result.IsError = false, want true")
	}
	if len(result.Content) == 0 || result.Content[0].Text != "intentional failure" {
		t.Errorf("result.Content = %+v, want [intentional failure]", result.Content)
	}
}

func TestStdioClient_RPCErrorWrapped(t *testing.T) {
	// Use a fake server that responds to initialize then loops,
	// replying to every other method with -32601 (method not found).
	const rpcErrScript = `#!/bin/sh
while read -r line; do
    [ -z "$line" ] && continue
    method=$(echo "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
    id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
    if [ "$method" = "initialize" ]; then
        printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"e","version":"0"}}}\n' "$id"
    else
        printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found"}}\n' "$id"
    fi
done
`
	dir := t.TempDir()
	path := dir + "/err.sh"
	if err := writeFile(path, rpcErrScript, 0o755); err != nil {
		t.Fatal(err)
	}

	c := mcp.NewStdioClient("/bin/sh", []string{path}, nil, "test", "0")
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	_, err := c.ListTools(ctx)
	if err == nil {
		t.Fatal("ListTools: expected error, got nil")
	}
	// The error message should contain "method not found" (from RPCError.Message).
	if !strings.Contains(err.Error(), "method not found") {
		t.Errorf("error = %v, want contains 'method not found'", err)
	}
}

func TestStdioClient_SubprocessExitReturnsError(t *testing.T) {
	// Server that responds to initialize, then exits. The second
	// call sees either a broken pipe (write to stdin) or an EOF on
	// stdout depending on timing. Either is a valid "subprocess gone"
	// signal — the caller doesn't care which one fired first.
	const exitScript = `#!/bin/sh
read -r line
id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"e","version":"0"}}}\n' "$id"
exit 0
`
	dir := t.TempDir()
	path := dir + "/exit.sh"
	if err := writeFile(path, exitScript, 0o755); err != nil {
		t.Fatal(err)
	}

	c := mcp.NewStdioClient("/bin/sh", []string{path}, nil, "test", "0")
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	_, err := c.ListTools(ctx)
	if err == nil {
		t.Fatal("ListTools: expected error after subprocess exit, got nil")
	}
	// Accept either "exited without response" (stdout EOF) or
	// "broken pipe" / "write to stdin" (write side closed first).
	msg := err.Error()
	if !strings.Contains(msg, "exited without response") &&
		!strings.Contains(msg, "broken pipe") &&
		!strings.Contains(msg, "write to stdin") {
		t.Errorf("error = %v, want contains one of: exited without response / broken pipe / write to stdin", err)
	}
}

func TestStdioClient_BadCommandReturnsConnectError(t *testing.T) {
	c := mcp.NewStdioClient("/nonexistent/binary/that/does/not/exist", nil, nil, "t", "0")
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatal("Connect to nonexistent binary: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "start subprocess") {
		t.Errorf("error = %v, want contains 'start subprocess'", err)
	}
}

func TestStdioClient_CallBeforeConnectFails(t *testing.T) {
	script := writeFakeServerScript(t)
	c := mcp.NewStdioClient("/bin/sh", []string{script}, nil, "test", "0")
	defer c.Close()

	// Connect was never called.
	_, err := c.CallTool(context.Background(), "x", nil)
	if err == nil {
		t.Fatal("CallTool before Connect: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "before Connect") {
		t.Errorf("error = %v, want contains 'before Connect'", err)
	}
}

// silence unused import warnings (net, exec, io are used by future
// HTTP tests that will land in a separate file).
var (
	_ = net.Listener(nil)
	_ = (*exec.Cmd)(nil)
	_ io.Reader
	_ = fmt.Sprintf
)