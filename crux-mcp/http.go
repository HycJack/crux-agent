package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// HTTPClient is an MCP client that speaks JSON-RPC 2.0 over HTTP POST.
//
// Supports two response modes per the MCP spec:
//   - JSON mode (default): Content-Type: application/json, body is the
//     JSON-RPC response object.
//   - SSE mode: Content-Type: text/event-stream, body is a stream of
//     "data: {json}\\n\\n" events. The event with id matching the request
//     is the response (intermediate events are progress notifications,
//     not used in v1).
type HTTPClient struct {
	url     string
	headers map[string]string

	clientName    string
	clientVersion string

	client *http.Client

	mu     sync.Mutex
	nextID int32
	info   ServerInfo

	closed atomic.Bool
}

// WithHTTPTimeout sets the per-request timeout for HTTPClient. Zero
// (default) means no timeout — relying on the parent ctx.
//
// Apply at construction via Option pattern:
//
//	NewHTTPClient(url, headers, "client", "v", WithHTTPTimeout(30*time.Second))
func WithHTTPTimeout(d time.Duration) Option {
	return func(o *ClientOptions) {
		o.HTTPTimeoutSecs = int(d.Seconds())
	}
}

// NewHTTPClient constructs an HTTP MCP client.
//
// url is the JSON-RPC endpoint (POST). headers is a flat map;
// values starting with "$" are expanded against os.Getenv at
// connect time (e.g. {"Authorization": "Bearer $TOKEN"}).
//
// For OAuth bearer auth, pass {"Authorization": "Bearer <token>"}
// or use the env-var expansion form to keep the token out of
// config files.
func NewHTTPClient(url string, headers map[string]string, clientName, clientVersion string, opts ...Option) *HTTPClient {
	o := ClientOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	httpClient := &http.Client{}
	if o.HTTPTimeoutSecs > 0 {
		httpClient.Timeout = time.Duration(o.HTTPTimeoutSecs) * time.Second
	}
	return &HTTPClient{
		url:           url,
		headers:       expandHeaders(headers),
		clientName:    clientName,
		clientVersion: clientVersion,
		client:        httpClient,
		nextID:        1,
	}
}

// expandHeaders resolves "$VAR" values against os.Getenv. Values
// without the "$" prefix are passed through unchanged.
func expandHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if strings.HasPrefix(v, "$") {
			if expanded := os.Getenv(v[1:]); expanded != "" {
				v = expanded
			}
		}
		out[k] = v
	}
	return out
}

// Connect sends the initialize handshake and stores server info.
//
// Unlike stdio, HTTP is stateless — Connect is a single POST. After
// Connect returns nil, the ServerInfo is populated and subsequent
// ListTools / CallTool calls each POST to the endpoint (some servers
// use a session id header after initialize; v2 may track that).
func (c *HTTPClient) Connect(ctx context.Context) error {
	if c.closed.Load() {
		return fmt.Errorf("mcp: Connect called on closed client")
	}

	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo: ImplementationInfo{
			Name:    c.clientName,
			Version: c.clientVersion,
		},
	}
	resp, err := c.send(ctx, MethodInitialize, params)
	if err != nil {
		return fmt.Errorf("mcp: initialize: %w", err)
	}

	var initResult InitializeResult
	if err := json.Unmarshal(resp.Result, &initResult); err != nil {
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

// ListTools calls tools/list via HTTP POST.
func (c *HTTPClient) ListTools(ctx context.Context) ([]Tool, error) {
	if !c.info.initialized {
		return nil, fmt.Errorf("mcp: ListTools called before Connect")
	}
	resp, err := c.send(ctx, MethodToolsList, struct{}{})
	if err != nil {
		return nil, fmt.Errorf("mcp: tools/list: %w", err)
	}
	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("mcp: parse tools/list result: %w", err)
	}
	return result.Tools, nil
}

// CallTool invokes a tool via HTTP POST.
func (c *HTTPClient) CallTool(ctx context.Context, name string, args json.RawMessage) (*CallToolResult, error) {
	if !c.info.initialized {
		return nil, fmt.Errorf("mcp: CallTool called before Connect")
	}
	params := ToolsCallParams{Name: name, Arguments: args}
	resp, err := c.send(ctx, MethodToolsCall, params)
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
func (c *HTTPClient) ServerInfo() ServerInfo { return c.info }

// Close is a no-op for HTTP clients (no subprocess to kill).
// Idempotent. The underlying http.Client may be GC'd eventually.
func (c *HTTPClient) Close() error {
	c.closed.Store(true)
	return nil
}

// send POSTs one JSON-RPC request and returns the response. Detects
// the response mode (JSON vs SSE) by Content-Type and routes to the
// appropriate reader.
//
// Concurrency: c.mu serializes Send calls. c.nextID is the request
// ID counter (atomic). The mutex is needed because two concurrent
// requests would interleave on c.nextID otherwise.
func (c *HTTPClient) send(ctx context.Context, method string, params any) (*Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed.Load() {
		return nil, fmt.Errorf("mcp: send on closed client")
	}

	id := int(atomic.AddInt32(&c.nextID, 1))
	req, err := newRequest(method, id, params)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp: POST %s: %w", method, err)
	}
	defer httpResp.Body.Close()

	contentType := httpResp.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/event-stream") {
		return readSSEResponse(httpResp.Body, id)
	}

	// Default: JSON response.
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("mcp: read response body: %w", err)
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mcp: HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	var rpcResp Response
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("mcp: parse response: %w", err)
	}
	if rpcResp.Error != nil {
		return &rpcResp, rpcResp.Error
	}
	return &rpcResp, nil
}

// readSSEResponse parses an SSE response stream and returns the
// JSON-RPC response for the given request ID.
//
// MCP SSE format: lines of "data: {json}\\n\\n". The event whose
// JSON parses to an object with matching ID is the response.
// Intermediate events (notifications, progress) have no ID or a
// non-matching ID and are skipped (v1 ignores them).
func readSSEResponse(body io.Reader, wantID int) (*Response, error) {
	scanner := bufio.NewScanner(body)
	// SSE messages can be larger than the default 64KB buffer for
	// big tool outputs. 1MB matches stdio's upper bound.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()

		// SSE event boundary (blank line) flushes the current event.
		if line == "" {
			if len(dataLines) == 0 {
				continue
			}
			payload := strings.Join(dataLines, "\n")
			dataLines = dataLines[:0]

			var rpcResp Response
			if err := json.Unmarshal([]byte(payload), &rpcResp); err != nil {
				// Skip non-JSON-RPC events (e.g. ping comments).
				continue
			}
			if rpcResp.ID == wantID {
				if rpcResp.Error != nil {
					return &rpcResp, rpcResp.Error
				}
				return &rpcResp, nil
			}
			// Different ID — could be a notification or out-of-order
			// response. Skip in v1; v2 may route by ID.
			continue
		}

		// Collect "data:" lines. Other SSE fields (event:, id:, retry:)
		// are ignored in v1.
		if strings.HasPrefix(line, "data:") {
			// Per SSE spec, "data:" may have one optional leading space.
			payload := strings.TrimPrefix(line, "data:")
			payload = strings.TrimPrefix(payload, " ")
			dataLines = append(dataLines, payload)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("mcp: read SSE stream: %w", err)
	}
	return nil, fmt.Errorf("mcp: SSE stream ended without response for id=%d", wantID)
}