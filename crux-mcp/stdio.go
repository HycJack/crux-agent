package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// StdioClient is an MCP client that speaks JSON-RPC 2.0 over a subprocess's
// stdin/stdout. One StdioClient per MCP server.
//
// Lifecycle:
//
//	c := NewStdioClient("python", []string{"mcp_server.py"}, nil)
//	if err := c.Connect(ctx); err != nil { ... }
//	tools, _ := c.ListTools(ctx)
//	result, _ := c.CallTool(ctx, "fetch", json.RawMessage(`{"url":"x"}`))
//	c.Close()
type StdioClient struct {
	command string
	args    []string
	env     map[string]string

	// handshake identity (sent on Connect)
	clientName    string
	clientVersion string

	opts ClientOptions

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner

	mu     sync.Mutex // serializes Send calls
	nextID int32
	info   ServerInfo

	closed atomic.Bool
}

// NewStdioClient constructs a stdio MCP client.
//
// clientName / clientVersion are sent in the initialize handshake
// so the server can log / branch on the caller identity.
//
// env supports "$VAR" expansion against os.Getenv at connect time
// (useful for "$HOME/.local/bin:$PATH" style values in config).
func NewStdioClient(command string, args []string, env map[string]string, clientName, clientVersion string, opts ...Option) *StdioClient {
	o := ClientOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	return &StdioClient{
		command:       command,
		args:          args,
		env:           env,
		clientName:    clientName,
		clientVersion: clientVersion,
		opts:          o,
		nextID:        1,
	}
}

// Connect spawns the subprocess and performs the initialize handshake.
//
// The handshake is synchronous: Connect blocks until the server replies
// (or ctx expires). After Connect returns nil, ServerInfo() returns
// the server's metadata and ListTools / CallTool are callable.
//
// On handshake failure the subprocess is killed and the error
// is returned; the Client is unusable and Close should still be called.
func (c *StdioClient) Connect(ctx context.Context) error {
	if c.closed.Load() {
		return fmt.Errorf("mcp: Connect called on closed client")
	}

	c.cmd = exec.CommandContext(ctx, c.command, c.args...)

	// Inherit parent env, then overlay configured env (with $VAR expansion).
	c.cmd.Env = os.Environ()
	for k, v := range c.env {
		if strings.HasPrefix(v, "$") {
			if expanded := os.Getenv(v[1:]); expanded != "" {
				v = expanded
			}
		}
		c.cmd.Env = append(c.cmd.Env, k+"="+v)
	}

	var err error
	c.stdin, err = c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp: stdin pipe: %w", err)
	}

	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp: stdout pipe: %w", err)
	}

	// Mirror subprocess stderr to ours by default; future v2 could
	// surface it via a configurable hook.
	c.cmd.Stderr = os.Stderr

	// 1MB per-line buffer — large enough for typical MCP responses
	// (most are <100KB) but bounded to prevent OOM on a misbehaving
	// server that emits huge lines without newlines.
	c.scanner = bufio.NewScanner(stdout)
	c.scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("mcp: start subprocess %q: %w", c.command, err)
	}

	// Send initialize and read the response.
	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo: ImplementationInfo{
			Name:    c.clientName,
			Version: c.clientVersion,
		},
	}
	resp, err := c.send(ctx, MethodInitialize, 0, params)
	if err != nil {
		_ = c.killSubprocess()
		return fmt.Errorf("mcp: initialize: %w", err)
	}

	var initResult InitializeResult
	if err := json.Unmarshal(resp.Result, &initResult); err != nil {
		_ = c.killSubprocess()
		return fmt.Errorf("mcp: parse initialize result: %w", err)
	}

	c.info = ServerInfo{
		Name:            initResult.ServerInfo.Name,
		Version:         initResult.ServerInfo.Version,
		ProtocolVersion: initResult.ProtocolVersion,
		Capabilities:    initResult.Capabilities,
		initialized:     true,
	}
	return nil
}

// ListTools calls the server's tools/list method.
//
// Tools are returned unprefixed (server-side names). The Manager
// applies the prefix when aggregating across servers.
func (c *StdioClient) ListTools(ctx context.Context) ([]Tool, error) {
	if !c.info.initialized {
		return nil, fmt.Errorf("mcp: ListTools called before Connect")
	}
	resp, err := c.send(ctx, MethodToolsList, 0, struct{}{})
	if err != nil {
		return nil, fmt.Errorf("mcp: tools/list: %w", err)
	}
	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("mcp: parse tools/list result: %w", err)
	}
	return result.Tools, nil
}

// CallTool invokes a tool by name with the given arguments.
//
// args must be a JSON object (or null). The tool's InputSchema
// describes the expected shape; this library does not validate.
//
// If the tool returns IsError=true, the result is returned with
// err == nil — the caller decides how to surface tool-level errors
// to the LLM. RPC / transport errors return (nil, err).
func (c *StdioClient) CallTool(ctx context.Context, name string, args json.RawMessage) (*CallToolResult, error) {
	if !c.info.initialized {
		return nil, fmt.Errorf("mcp: CallTool called before Connect")
	}
	params := ToolsCallParams{Name: name, Arguments: args}
	resp, err := c.send(ctx, MethodToolsCall, 0, params)
	if err != nil {
		return nil, fmt.Errorf("mcp: tools/call %q: %w", name, err)
	}
	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("mcp: parse tools/call result: %w", err)
	}
	return &result, nil
}

// ServerInfo returns the metadata collected during Connect.
// Returns the zero value if Connect has not been called.
func (c *StdioClient) ServerInfo() ServerInfo { return c.info }

// Close shuts down the subprocess and releases the scanner.
// Idempotent — safe to call multiple times.
func (c *StdioClient) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	var firstErr error
	if c.stdin != nil {
		if err := c.stdin.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := c.killSubprocess(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (c *StdioClient) killSubprocess() error {
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	// Best-effort kill + wait. Subprocess may already be dead.
	_ = c.cmd.Process.Kill()
	// Wait but don't propagate the "wait on killed process" error
	// if the kill itself succeeded — the kill is what we wanted.
	_ = c.cmd.Wait()
	return nil
}

// send serializes one JSON-RPC request/response exchange.
//
// id == 0 means "server-assigned" (used for notifications). For
// request/response we use a positive ID assigned by nextID.
//
// The reader loop matches responses to requests by ID. Lines that
// don't parse as JSON (e.g. the server leaks a log line to stdout)
// are silently skipped, matching fastclaw's behavior.
//
// On subprocess exit (scanner returns false with no error), the
// caller gets a "process exited without response" error.
func (c *StdioClient) send(ctx context.Context, method string, idHint int, params any) (*Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed.Load() {
		return nil, fmt.Errorf("mcp: send on closed client")
	}

	id := idHint
	if id == 0 {
		id = int(atomic.AddInt32(&c.nextID, 1))
	}

	req, err := newRequest(method, id, params)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}
	data = append(data, '\n')

	if _, err := c.stdin.Write(data); err != nil {
		return nil, fmt.Errorf("mcp: write to stdin: %w", err)
	}

	// Read response lines until we get one with matching ID.
	// Honors ctx cancellation by polling the done channel between reads.
	done := make(chan struct{})
	go func() {
		// We can't actually interrupt bufio.Scanner.Scan, so we just
		// abandon this goroutine if ctx fires. The Send call will
		// still block on Scan, but a subprocess-level timeout via
		// exec.CommandContext will kill the subprocess eventually.
		<-ctx.Done()
		close(done)
	}()

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !c.scanner.Scan() {
			if err := c.scanner.Err(); err != nil {
				return nil, fmt.Errorf("mcp: read stdout: %w", err)
			}
			return nil, fmt.Errorf("mcp: process exited without response (method=%s, id=%d)", method, id)
		}

		line := c.scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			// Skip non-JSON lines (server logs leaking to stdout).
			continue
		}

		if resp.ID != id {
			// Notification or response for a different request;
			// skip (notifications from server are not supported in v1).
			continue
		}

		if resp.Error != nil {
			return &resp, resp.Error
		}
		return &resp, nil
	}
}

// _ = time.Second keeps the time import available for future per-call
// timeout knobs (v2 may add ClientOptions.StdioTimeout).
var _ = time.Second