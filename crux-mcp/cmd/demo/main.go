// Command demo exercises the crux-mcp library end-to-end without any
// external MCP server. It spawns the bundled fakeserver (a tiny in-tree
// Go binary implementing the 3 supported methods) over both transports
// and runs a fixed set of scenarios.
//
// Scenarios:
//
//	01-stdio-connect  : StdioClient connects, lists 3 tools
//	02-stdio-echo     : tools/call "echo" returns the args
//	03-stdio-add      : tools/call "add" returns sum
//	04-stdio-iserror  : tools/call "fail" returns IsError=true (no Go error)
//	05-http-connect   : HTTPClient connects, lists 3 tools (JSON mode)
//	06-http-sse       : Separate fake server that replies in SSE mode
//	07-manager-prefix : Manager aggregates both servers, tools are prefixed
//	08-manager-call   : Manager.Call dispatches by prefix to correct server
//	09-manager-partial: One bad + one good server → partial success
//
// Run:
//
//	go run ./crux-mcp/cmd/demo
//
// Exit code 0 = all scenarios passed; 1 = at least one failed.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/hycjack/crux-mcp"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results := []scenarioResult{}
	run := func(name string, fn func() error) {
		fmt.Printf("\n=== %s ===\n", name)
		err := fn()
		if err != nil {
			fmt.Printf("  FAIL: %v\n", err)
			results = append(results, scenarioResult{name, false, err.Error()})
		} else {
			fmt.Printf("  PASS\n")
			results = append(results, scenarioResult{name, true, ""})
		}
	}

	// Build the fakeserver binary once and reuse.
	binPath, err := buildFakeServer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build fakeserver: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(binPath)

	// 01-04: stdio transport via a manager with one stdio server.
	run("01-stdio-connect", func() error {
		mgr, err := mcp.NewManager(ctx, []mcp.ServerSpec{
			{Name: "stdio1", Stdio: &mcp.StdioSpec{Command: binPath, Args: []string{}}},
		})
		if err != nil {
			return fmt.Errorf("NewManager: %w", err)
		}
		defer mgr.Close()
		tools, err := mgr.Tools(ctx)
		if err != nil {
			return fmt.Errorf("Tools: %w", err)
		}
		if len(tools) != 3 {
			return fmt.Errorf("got %d tools, want 3", len(tools))
		}
		fmt.Printf("  tools: %d (e.g. %s)\n", len(tools), tools[0].Name)
		return nil
	})

	run("02-stdio-echo", func() error {
		mgr, err := mcp.NewManager(ctx, []mcp.ServerSpec{
			{Name: "stdio1", Stdio: &mcp.StdioSpec{Command: binPath, Args: []string{}}},
		})
		if err != nil {
			return fmt.Errorf("NewManager: %w", err)
		}
		defer mgr.Close()
		result, err := mgr.Call(ctx, "mcp_stdio1_echo", json.RawMessage(`{"msg":"hello"}`))
		if err != nil {
			return fmt.Errorf("Call: %w", err)
		}
		if result.IsError {
			return fmt.Errorf("result.IsError=true, want false")
		}
		if len(result.Content) == 0 || result.Content[0].Text == "" {
			return fmt.Errorf("empty content")
		}
		fmt.Printf("  echo: %s\n", result.Content[0].Text)
		return nil
	})

	run("03-stdio-add", func() error {
		mgr, err := mcp.NewManager(ctx, []mcp.ServerSpec{
			{Name: "stdio1", Stdio: &mcp.StdioSpec{Command: binPath, Args: []string{}}},
		})
		if err != nil {
			return fmt.Errorf("NewManager: %w", err)
		}
		defer mgr.Close()
		result, err := mgr.Call(ctx, "mcp_stdio1_add", json.RawMessage(`{"a":17,"b":25}`))
		if err != nil {
			return fmt.Errorf("Call: %w", err)
		}
		if result.Content[0].Text != "42" {
			return fmt.Errorf("got %q, want 42", result.Content[0].Text)
		}
		fmt.Printf("  17 + 25 = %s\n", result.Content[0].Text)
		return nil
	})

	run("04-stdio-iserror", func() error {
		mgr, err := mcp.NewManager(ctx, []mcp.ServerSpec{
			{Name: "stdio1", Stdio: &mcp.StdioSpec{Command: binPath, Args: []string{}}},
		})
		if err != nil {
			return fmt.Errorf("NewManager: %w", err)
		}
		defer mgr.Close()
		result, err := mgr.Call(ctx, "mcp_stdio1_fail", json.RawMessage(`{}`))
		if err != nil {
			return fmt.Errorf("Call returned transport error: %w", err)
		}
		if !result.IsError {
			return fmt.Errorf("IsError=false, want true")
		}
		fmt.Printf("  fail: isError=%v text=%q\n", result.IsError, result.Content[0].Text)
		return nil
	})

	// 05-06: HTTP transport via the real fakeserver binary in --http mode.
	srv1, err := startHTTPFakeServer(ctx, binPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start http fake: %v\n", err)
		os.Exit(1)
	}
	defer srv1.Close()

	run("05-http-connect", func() error {
		mgr, err := mcp.NewManager(ctx, []mcp.ServerSpec{
			{Name: "http1", HTTP: &mcp.HTTPSpec{URL: srv1.URLPath("/mcp")}},
		})
		if err != nil {
			return fmt.Errorf("NewManager: %w", err)
		}
		defer mgr.Close()
		tools, err := mgr.Tools(ctx)
		if err != nil {
			return fmt.Errorf("Tools: %w", err)
		}
		if len(tools) != 3 {
			return fmt.Errorf("got %d tools, want 3", len(tools))
		}
		fmt.Printf("  tools: %d\n", len(tools))
		return nil
	})

	// 06: SSE response mode via an httptest fake that wraps the real
	// fakeserver's responses in SSE format. Tests the readSSEResponse
	// code path in http.go.
	srv2 := startSSEModeHTTPServer(binPath)
	defer srv2.Close()

	run("06-http-sse", func() error {
		mgr, err := mcp.NewManager(ctx, []mcp.ServerSpec{
			{Name: "sse1", HTTP: &mcp.HTTPSpec{URL: srv2.URL + "/mcp"}},
		})
		if err != nil {
			return fmt.Errorf("NewManager: %w", err)
		}
		defer mgr.Close()
		tools, err := mgr.Tools(ctx)
		if err != nil {
			return fmt.Errorf("Tools: %w", err)
		}
		if len(tools) != 3 {
			return fmt.Errorf("got %d tools, want 3", len(tools))
		}
		fmt.Printf("  SSE mode: %d tools\n", len(tools))
		return nil
	})

	// 07-08: Manager aggregates stdio + http servers.
	run("07-manager-prefix", func() error {
		mgr, err := mcp.NewManager(ctx, []mcp.ServerSpec{
			{Name: "stdio1", Stdio: &mcp.StdioSpec{Command: binPath, Args: []string{}}},
			{Name: "http1", HTTP: &mcp.HTTPSpec{URL: srv1.URLPath("/mcp")}},
		})
		if err != nil {
			return fmt.Errorf("NewManager: %w", err)
		}
		defer mgr.Close()
		tools, err := mgr.Tools(ctx)
		if err != nil {
			return fmt.Errorf("Tools: %w", err)
		}
		// Expect 6 tools total: 3 from each server, all prefixed.
		if len(tools) != 6 {
			return fmt.Errorf("got %d tools, want 6", len(tools))
		}
		// Verify every tool name is prefixed.
		for _, tool := range tools {
			if len(tool.Name) < 4 || tool.Name[:4] != "mcp_" {
				return fmt.Errorf("unprefixed tool name: %s", tool.Name)
			}
		}
		fmt.Printf("  %d prefixed tools: e.g. %s, %s\n", len(tools), tools[0].Name, tools[3].Name)
		return nil
	})

	run("08-manager-call", func() error {
		mgr, err := mcp.NewManager(ctx, []mcp.ServerSpec{
			{Name: "stdio1", Stdio: &mcp.StdioSpec{Command: binPath, Args: []string{}}},
			{Name: "http1", HTTP: &mcp.HTTPSpec{URL: srv1.URLPath("/mcp")}},
		})
		if err != nil {
			return fmt.Errorf("NewManager: %w", err)
		}
		defer mgr.Close()
		// Call a tool from each server to prove routing works.
		r1, err := mgr.Call(ctx, "mcp_stdio1_add", json.RawMessage(`{"a":100,"b":200}`))
		if err != nil {
			return fmt.Errorf("Call stdio: %w", err)
		}
		if r1.Content[0].Text != "300" {
			return fmt.Errorf("stdio add: got %q, want 300", r1.Content[0].Text)
		}
		r2, err := mgr.Call(ctx, "mcp_http1_add", json.RawMessage(`{"a":7,"b":8}`))
		if err != nil {
			return fmt.Errorf("Call http: %w", err)
		}
		if r2.Content[0].Text != "15" {
			return fmt.Errorf("http add: got %q, want 15", r2.Content[0].Text)
		}
		fmt.Printf("  stdio add(100,200)=%s  http add(7,8)=%s\n", r1.Content[0].Text, r2.Content[0].Text)
		return nil
	})

	run("09-manager-partial", func() error {
		mgr, err := mcp.NewManager(ctx, []mcp.ServerSpec{
			{Name: "good", Stdio: &mcp.StdioSpec{Command: binPath, Args: []string{}}},
			{Name: "bad", Stdio: &mcp.StdioSpec{Command: "/nonexistent/binary/xyz"}},
		})
		if err == nil {
			mgr.Close()
			return fmt.Errorf("expected PartialSuccessError, got nil")
		}
		if !mcp.IsPartialSuccess(err) {
			return fmt.Errorf("got %T, want PartialSuccessError", err)
		}
		defer mgr.Close()
		tools, terr := mgr.Tools(ctx)
		if terr != nil {
			return fmt.Errorf("Tools after partial: %w", terr)
		}
		if len(tools) == 0 {
			return fmt.Errorf("Tools empty after partial success")
		}
		fmt.Printf("  partial: %v; %d tools from 'good' server\n", err, len(tools))
		return nil
	})

	// Summary.
	fmt.Printf("\n=== RESULTS ===\n")
	pass, fail := 0, 0
	for _, r := range results {
		status := "PASS"
		if !r.pass {
			status = "FAIL"
			fail++
		} else {
			pass++
		}
		fmt.Printf("  %s  %s\n", status, r.name)
	}
	fmt.Printf("\n  %d passed, %d failed\n", pass, fail)
	if fail > 0 {
		os.Exit(1)
	}
}

type scenarioResult struct {
	name string
	pass bool
	err  string
}

// buildFakeServer compiles the in-tree fakeserver binary and returns
// its path. Uses `go build` to ensure the binary matches the host's
// Go toolchain (no CGO, no cross-compile concerns).
func buildFakeServer() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// The demo runs from any cwd; resolve the package path relative
	// to this source file's location at compile time.
	pkgPath := "github.com/hycjack/crux-mcp/cmd/demo/fakeserver"
	binPath := filepath.Join(os.TempDir(), "crux-mcp-fakeserver")
	cmd := exec.Command("go", "build", "-o", binPath, pkgPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build %s: %w", pkgPath, err)
	}
	_ = exe // unused, but might be useful for relative resolution later
	return binPath, nil
}

// startHTTPFakeServer launches the real fakeserver binary in --http
// mode and waits until it accepts connections. Returns a handle
// whose Close() kills the subprocess and closes the test wrapper.
func startHTTPFakeServer(ctx context.Context, binPath string) (*subprocessHTTPServer, error) {
	port, err := pickFreePort()
	if err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	cmd := exec.CommandContext(ctx, binPath, "--http", addr)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start fakeserver: %w", err)
	}

	// Poll until the server is reachable.
	deadline := time.Now().Add(2 * time.Second)
	url := "http://" + addr
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/")
		if err == nil {
			resp.Body.Close()
			return &subprocessHTTPServer{cmd: cmd, url: url}, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return nil, fmt.Errorf("fakeserver did not start within 2s")
}

// subprocessHTTPServer is a minimal handle around a subprocess-launched
// HTTP server. Close() kills the subprocess.
type subprocessHTTPServer struct {
	cmd *exec.Cmd
	url string
}

// URL returns the base URL (no path).
func (s *subprocessHTTPServer) URLPath(p string) string { return s.url + p }

// Close kills the subprocess.
func (s *subprocessHTTPServer) Close() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	}
}

// pickFreePort asks the kernel for an unused TCP port. There's a
// small race window between this returning and the subprocess
// binding, but it's good enough for the demo.
func pickFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// startSSEModeHTTPServer wraps the real fakeserver's responses in
// SSE format. The forwarder reads the JSON response and re-emits it
// as a single SSE event. This tests the readSSEResponse code path
// without requiring the real fakeserver to know about SSE.
func startSSEModeHTTPServer(fakeserverBin string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		// Re-emit the response from a separate stdio fakeserver instance
		// as SSE. We invoke the same binary over stdio in a goroutine
		// per request — wasteful but keeps the demo self-contained.
		body, _ := io.ReadAll(r.Body)

		// Spawn fakeserver in stdio mode for this one request.
		cmd := exec.Command(fakeserverBin)
		cmd.Stdin = bytes.NewReader(body)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// out contains one JSON line.
		line := bytes.TrimSpace(out.Bytes())
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", line)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	return httptest.NewServer(mux)
}

// startFakeHTTPServer is retained as a no-op alias for callers that
// were using the old (broken) hardcoded-response fake. Now all HTTP
// tests should use startHTTPFakeServer with the real fakeserver.
// Deprecated: use startHTTPFakeServer.
func startFakeHTTPServer(formatTemplate string, sseMode bool) *httptest.Server {
	return httptest.NewServer(http.NewServeMux())
}