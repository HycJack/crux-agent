package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// ServerInfo is the metadata returned by an MCP server during the
// initialize handshake.
//
// Consumers access this via Client.ServerInfo() after Connect. It is
// empty until Connect has been called successfully.
type ServerInfo struct {
	Name             string         // server's self-reported name
	Version          string         // server's self-reported version
	ProtocolVersion  string         // negotiated protocol version
	Capabilities     map[string]any // server capability map
	initialized      bool           // internal: Connect completed
}

// Transport is the wire-level abstraction over stdio vs HTTP.
//
// One Transport per Client. Transports are not safe for concurrent
// Send calls — the Client serializes them. Transports are safe for
// concurrent Close (idempotent).
//
// Implementations: StdioTransport (subprocess), HTTPTransport (POST + SSE).
type Transport interface {
	// Connect performs any setup needed before Send is callable.
	// For stdio: spawn subprocess, send initialize, store server info.
	// For HTTP: send initialize, store server info.
	Connect(ctx context.Context, handshake InitializeParams, clientInfo *ImplementationInfo) (ServerInfo, error)

	// Send exchanges one JSON-RPC request/response pair.
	// The transport must not retry on transport errors — the Client
	// decides whether to retry. The transport MAY retry on network
	// glitches internally if it can do so without losing semantics.
	Send(ctx context.Context, req *Request) (*Response, error)

	// Close shuts down the transport. Idempotent. Returns any
	// unrecoverable cleanup error (subprocess kill failure, etc).
	Close() error
}

// Client is the high-level interface consumers use.
//
// Lifecycle: NewXxxClient → Connect → ListTools / CallTool (any number) → Close.
//
// Connect performs the initialize handshake; the ServerInfo returned
// by the handshake is then available via ServerInfo().
//
// All methods are safe for concurrent use except Connect (which
// transitions state from disconnected to connected exactly once).
type Client interface {
	Connect(ctx context.Context) error
	ListTools(ctx context.Context) ([]Tool, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (*CallToolResult, error)
	ServerInfo() ServerInfo
	Close() error
}

// ClientOptions configures transport-specific behavior. Empty for
// the no-op default; populated by With* options in stdio.go and http.go.
type ClientOptions struct {
	// HTTP timeout (applies only to HTTP transport). Zero = no timeout.
	HTTPTimeoutSecs int
}

// Option configures a Client at construction time.
type Option func(*ClientOptions)

// StdioSpec describes one stdio MCP server.
//
// Command + Args + Env mirror os/exec.Cmd. Env supports "$VAR"
// expansion at connect time (e.g. "$HOME/.local/bin:$PATH" → resolved
// against os.Getenv).
type StdioSpec struct {
	Command string
	Args    []string
	Env     map[string]string
}

// HTTPSpec describes one HTTP MCP server.
//
// URL is the JSON-RPC endpoint. Headers is a flat map of name → value;
// values starting with "$" are expanded against os.Getenv at connect
// time. Auth: pass {"Authorization": "Bearer $TOKEN"} for OAuth.
type HTTPSpec struct {
	URL     string
	Headers map[string]string
}

// validateServerName enforces the prefix sanitization rules used by
// the Manager. Server names that cannot be safely prefixed (e.g. empty
// after sanitization) are rejected at manager construction.
func validateServerName(name string) error {
	if name == "" {
		return fmt.Errorf("mcp: empty server name")
	}
	return nil
}