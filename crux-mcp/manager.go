package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Manager aggregates multiple MCP server Clients and routes tool calls
// to the right server based on a prefixed tool name.
//
// Tool name format: "mcp_<sanitizedServerName>_<originalToolName>".
// The prefix prevents name collisions when several servers expose tools
// with the same name (e.g. each MCP server has its own "search").
//
// Manager is the recommended entry point for hosts — instantiate one
// per config snapshot, then expose its Tools() to the LLM and route
// Call() requests from the tool dispatcher.
type Manager struct {
	clients []namedClient
	// route maps prefixed tool name → (client index, original tool name).
	route map[string]toolRoute
	mu     sync.RWMutex
}

type namedClient struct {
	name   string
	client Client
}

type toolRoute struct {
	clientIdx int
	toolName  string
}

// failedSpec records a single server's connect failure inside
// NewManager. Promoted to package scope so PartialSuccessError
// and joinErrors can reference it without an anonymous struct.
type failedSpec struct {
	Name string
	Err  error
}

// ServerSpec is the common shape for declaring a server to the Manager.
// Either Stdio or HTTP fields should be populated per entry, but not both.
type ServerSpec struct {
	Name  string
	Stdio *StdioSpec
	HTTP  *HTTPSpec

	// Client identity (sent in initialize handshake).
	ClientName    string
	ClientVersion string
}

// NewManager connects to all configured servers, fetches their tool
// lists, and builds the prefix-routing table.
//
// ServerSpec.Name is the prefix key (must be non-empty). Exactly one
// of Stdio / HTTP should be populated per spec.
//
// A server that fails to connect is logged via the returned error
// chain only if ALL servers fail. Partial-success: if at least one
// server connects, the Manager is usable for the servers that did
// connect and a non-nil error reports the failed ones (caller can
// log/inspect). Use IsPartialSuccess to distinguish.
//
// Client name defaults to "crux-mcp" if empty. Version defaults to "0".
func NewManager(ctx context.Context, specs []ServerSpec, opts ...Option) (*Manager, error) {
	m := &Manager{
		route: make(map[string]toolRoute),
	}
	if len(specs) == 0 {
		return m, nil
	}

	var failures []failedSpec
	successCount := 0

	for _, spec := range specs {
		if err := validateServerName(spec.Name); err != nil {
			failures = append(failures, failedSpec{Name: spec.Name, Err: err})
			continue
		}
		client, err := buildClient(spec, opts)
		if err != nil {
			failures = append(failures, failedSpec{Name: spec.Name, Err: err})
			continue
		}
		if err := client.Connect(ctx); err != nil {
			failures = append(failures, failedSpec{Name: spec.Name, Err: err})
			continue
		}
		tools, err := client.ListTools(ctx)
		if err != nil {
			_ = client.Close()
			failures = append(failures, failedSpec{Name: spec.Name, Err: err})
			continue
		}

		clientIdx := len(m.clients)
		m.clients = append(m.clients, namedClient{spec.Name, client})
		for _, t := range tools {
			prefixed := prefixToolName(spec.Name, t.Name)
			m.route[prefixed] = toolRoute{
				clientIdx: clientIdx,
				toolName:  t.Name,
			}
		}
		successCount++
	}

	if successCount == 0 && len(failures) > 0 {
		return m, fmt.Errorf("mcp: all %d servers failed to connect: %w", len(specs), joinErrors(failures))
	}
	if len(failures) > 0 {
		return m, &PartialSuccessError{Failures: failures, Successes: successCount}
	}
	return m, nil
}

// buildClient constructs the appropriate Client based on the spec.
// Returns an error if neither Stdio nor HTTP is set, or if both are.
func buildClient(spec ServerSpec, opts []Option) (Client, error) {
	name := spec.ClientName
	if name == "" {
		name = "crux-mcp"
	}
	version := spec.ClientVersion
	if version == "" {
		version = "0"
	}
	switch {
	case spec.Stdio != nil && spec.HTTP != nil:
		return nil, fmt.Errorf("mcp: server %q has both Stdio and HTTP set", spec.Name)
	case spec.Stdio != nil:
		return NewStdioClient(spec.Stdio.Command, spec.Stdio.Args, spec.Stdio.Env, name, version, opts...), nil
	case spec.HTTP != nil:
		return NewHTTPClient(spec.HTTP.URL, spec.HTTP.Headers, name, version, opts...), nil
	default:
		return nil, fmt.Errorf("mcp: server %q has neither Stdio nor HTTP set", spec.Name)
	}
}

// PartialSuccessError is returned by NewManager when at least one
// server connected but some failed. Use errors.As to inspect.
type PartialSuccessError struct {
	Successes int
	Failures  []failedSpec
}

func (e *PartialSuccessError) Error() string {
	names := make([]string, len(e.Failures))
	for i, f := range e.Failures {
		names[i] = fmt.Sprintf("%s (%v)", f.Name, f.Err)
	}
	return fmt.Sprintf("mcp: %d succeeded, %d failed: %s",
		e.Successes, len(e.Failures), strings.Join(names, "; "))
}

// IsPartialSuccess reports whether err is a PartialSuccessError.
// Convenience for callers that want to log "some servers failed"
// without importing errors.
func IsPartialSuccess(err error) bool {
	_, ok := err.(*PartialSuccessError)
	return ok
}

// Tools returns the prefixed tool list aggregated across all connected
// servers. Names are in the form "mcp_<server>_<tool>".
//
// The list is fresh per call (re-queries each server) — useful for
// dynamic configurations. For a static snapshot, callers should cache
// the result.
func (m *Manager) Tools(ctx context.Context) ([]Tool, error) {
	m.mu.RLock()
	clients := m.clients
	m.mu.RUnlock()

	var out []Tool
	for i, nc := range clients {
		serverTools, err := nc.client.ListTools(ctx)
		if err != nil {
			// One server's ListTools failing shouldn't kill the
			// whole snapshot — skip with the prefix still applied.
			continue
		}
		for _, t := range serverTools {
			out = append(out, Tool{
				Name:        prefixToolName(nc.name, t.Name),
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
			_ = i // route uses clientIdx; keeping for clarity
		}
	}
	return out, nil
}

// ToolMap returns the current routing table as a snapshot map of
// prefixed name → server-supplied Tool definition. Cheaper than
// repeated Tools() calls when the manager is stable.
func (m *Manager) ToolMap(ctx context.Context) (map[string]Tool, error) {
	m.mu.RLock()
	clients := m.clients
	m.mu.RUnlock()

	out := make(map[string]Tool)
	for _, nc := range clients {
		serverTools, err := nc.client.ListTools(ctx)
		if err != nil {
			continue
		}
		for _, t := range serverTools {
			out[prefixToolName(nc.name, t.Name)] = t
		}
	}
	return out, nil
}

// Call routes a prefixed tool call to the correct server. Returns
// (nil, error) if the prefixed name is unknown or the underlying
// CallTool fails. Returns (result, nil) when IsError=true on the
// result (the caller checks).
func (m *Manager) Call(ctx context.Context, prefixedName string, args json.RawMessage) (*CallToolResult, error) {
	m.mu.RLock()
	route, ok := m.route[prefixedName]
	var client Client
	if ok && route.clientIdx < len(m.clients) {
		client = m.clients[route.clientIdx].client
	}
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("mcp: unknown tool %q (use Manager.Tools to discover)", prefixedName)
	}
	return client.CallTool(ctx, route.toolName, args)
}

// ServerInfos returns the ServerInfo for every connected server,
// in the order they were added to NewManager.
func (m *Manager) ServerInfos() []ServerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ServerInfo, len(m.clients))
	for i, nc := range m.clients {
		out[i] = nc.client.ServerInfo()
	}
	return out
}

// Close shuts down every connected client. Idempotent.
func (m *Manager) Close() error {
	m.mu.Lock()
	clients := m.clients
	m.clients = nil
	m.mu.Unlock()

	var firstErr error
	for _, nc := range clients {
		if err := nc.client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// prefixToolName produces "mcp_<safe>_<tool>". Safe replaces any
// non-[a-zA-Z0-9_] in the server name with "_". This sanitization
// is essential because server names typically come from YAML/JSON
// config and may contain hyphens, dots, or other characters.
func prefixToolName(serverName, toolName string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, serverName)
	return "mcp_" + safe + "_" + toolName
}

func joinErrors(failures []failedSpec) error {
	if len(failures) == 1 {
		return failures[0].Err
	}
	msgs := make([]string, len(failures))
	for i, f := range failures {
		msgs[i] = fmt.Sprintf("%s: %v", f.Name, f.Err)
	}
	return fmt.Errorf("[%s]", strings.Join(msgs, "; "))
}