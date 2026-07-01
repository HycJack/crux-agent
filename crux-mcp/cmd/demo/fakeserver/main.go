// Command fakeserver is a tiny in-process MCP server used by the
// crux-mcp demo CLI. It implements the 3 methods the client
// supports (initialize / tools/list / tools/call) over both stdio
// (default) and HTTP (when --http is passed).
//
// Tools advertised:
//   - echo: returns the args as text content
//   - add:  computes a + b from args, returns the sum
//   - fail: always returns isError=true (for testing error paths)
//
// Usage:
//
//	fakeserver                    # stdio mode
//	fakeserver --http :7777       # HTTP mode on port 7777
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
)

// Request is the subset of mcp.Request we need to read.
// (Defined inline to avoid an mcp import cycle.)
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	httpAddr := flag.String("http", "", "if set, serve HTTP on this address (e.g. ':7777')")
	flag.Parse()

	if *httpAddr != "" {
		runHTTP(*httpAddr)
		return
	}
	runStdio()
}

func runStdio() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintf(os.Stderr, "fakeserver: bad json: %v\n", err)
			continue
		}
		resp := handle(req)
		out, _ := json.Marshal(resp)
		os.Stdout.Write(out)
		os.Stdout.Write([]byte("\n"))
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "fakeserver: scan: %v\n", err)
	}
}

func runHTTP(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := handle(req)
		w.Header().Set("Content-Type", "application/json")
		out, _ := json.Marshal(resp)
		w.Write(out)
	})
	log.Printf("fakeserver listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func handle(req Request) Response {
	switch req.Method {
	case "initialize":
		return ok(req.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"serverInfo": map[string]string{
				"name":    "fakeserver",
				"version": "1.0.0",
			},
			"capabilities": map[string]any{"tools": map[string]any{}},
		})
	case "tools/list":
		return ok(req.ID, map[string]any{
			"tools": []map[string]any{
				{"name": "echo", "description": "echoes args back", "inputSchema": map[string]any{"type": "object"}},
				{"name": "add", "description": "adds a + b", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]string{"type": "number"}, "b": map[string]string{"type": "number"}}}},
				{"name": "fail", "description": "always returns isError", "inputSchema": map[string]any{"type": "object"}},
			},
		})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &params)
		switch params.Name {
		case "echo":
			return ok(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "echo: " + string(params.Arguments)}},
			})
		case "add":
			var args struct {
				A float64 `json:"a"`
				B float64 `json:"b"`
			}
			if err := json.Unmarshal(params.Arguments, &args); err != nil {
				return errResp(req.ID, -32602, "invalid arguments: "+err.Error())
			}
			return ok(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": strconv.FormatFloat(args.A+args.B, 'f', -1, 64)}},
			})
		case "fail":
			return ok(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "intentional failure"}},
				"isError": true,
			})
		default:
			return errResp(req.ID, -32601, "tool not found: "+params.Name)
		}
	default:
		return errResp(req.ID, -32601, "method not found: "+req.Method)
	}
}

func ok(id int, result any) Response {
	raw, _ := json.Marshal(result)
	return Response{JSONRPC: "2.0", ID: id, Result: raw}
}

func errResp(id, code int, msg string) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	}
}