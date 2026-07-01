// Package mcp implements a Go client library for the Model Context Protocol
// (MCP, https://modelcontextprotocol.io). It targets MCP 2025-06-18 and
// supports three methods — initialize, tools/list, tools/call — over two
// transports (stdio subprocess and HTTP POST with optional SSE response).
//
// Zero external dependencies (Go stdlib only). The package is designed so
// any host framework can import it without dragging in agent / harness /
// provider code.
//
// The high-level surface is the Client interface (see client.go). The
// transport-level JSON-RPC 2.0 framing lives here.
//
// Spec references:
//   - JSON-RPC 2.0: https://www.jsonrpc.org/specification
//   - MCP 2025-06-18: https://modelcontextprotocol.io/specification/2025-06-18
package mcp

import "encoding/json"

// JSONRPCVersion is the protocol version string used in every frame.
const JSONRPCVersion = "2.0"

// ProtocolVersion is the MCP protocol version this library targets.
// Update together with the spec changes you want to support.
const ProtocolVersion = "2025-06-18"

// Method names from the MCP spec (only the three we implement in v1).
const (
	MethodInitialize = "initialize"
	MethodToolsList  = "tools/list"
	MethodToolsCall  = "tools/call"
)

// Standard JSON-RPC 2.0 error codes plus the MCP-defined range.
const (
	// JSON-RPC standard errors (-32768..-32000).
	ErrCodeParse          = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603

	// MCP-defined errors (per spec, in -32000..-32099 range).
	ErrCodeRequestCancelled = -32000 // request was cancelled by client
	ErrCodeInternalMCP      = -32001 // internal MCP error
)

// Request is a JSON-RPC 2.0 request sent to the server.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Notification is a JSON-RPC 2.0 notification (no ID, no response expected).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response from the server.
//
// Exactly one of Result or Error is set on a non-nil response. Both nil
// indicates a malformed frame (the transport will return an error).
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC 2.0 error object.
//
// Error implements the error interface so RPCError can be returned
// directly from a transport or client method when wrapped in an
// error chain. Callers that want to distinguish error categories
// should use errors.As(err, &rpcErr) and check Code.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error returns a human-readable string of the RPC error.
func (e *RPCError) Error() string { return e.Message }

// InitializeParams is the parameter for the initialize method.
//
// ClientInfo identifies the caller; the server uses it for logging and
// sometimes for capability negotiation. Capabilities is the optional
// list of features the client supports (e.g. {"sampling": {}}).
type InitializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]any         `json:"capabilities"`
	ClientInfo      ImplementationInfo     `json:"clientInfo"`
}

// ImplementationInfo identifies a client or server implementation.
type ImplementationInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the server's response to initialize.
//
// ServerInfo / Capabilities are surfaced via Client.ServerInfo() after
// the handshake so consumers can log / branch on them.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      ImplementationInfo `json:"serverInfo"`
	Capabilities    map[string]any     `json:"capabilities,omitempty"`
}

// ToolsListResult is the server's response to tools/list.
type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

// Tool describes a single tool that the server exposes via tools/call.
//
// InputSchema is a JSON Schema (draft 2020-12) for the tool's arguments.
// Consumers should pass it through to their LLM as-is. Raw JSON keeps
// the library agnostic of any specific schema-validation library.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ToolsCallParams is the parameter for tools/call.
//
// Name is the tool's name (unprefixed — server-side name, not the
// manager's prefixed form). Arguments are passed as a raw JSON object
// matching the tool's InputSchema.
type ToolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult is the server's response to tools/call.
//
// Content holds the tool's output. IsError indicates the tool itself
// returned an error result (vs. a transport-level failure); callers
// check this to decide whether to surface the error to the LLM or
// retry. The transport returns (result, nil) when IsError is true
// — the error semantics live inside the protocol, not the Go error
// return.
type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Content is one item in a CallToolResult.
//
// Type is one of "text", "image", "resource", "audio" (per MCP spec).
// For "text", Text carries the string. For other types, Data carries
// the base64-encoded payload and MimeType identifies the format. The
// library intentionally does not enforce content types — callers
// branch on Type as needed.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`     // base64
	MimeType string `json:"mimeType,omitempty"`
}

// TextContent is a convenience constructor for the common text case.
func TextContent(s string) Content {
	return Content{Type: "text", Text: s}
}

// newRequest builds a Request with a JSON-marshaled params payload.
//
// Returns an error if params cannot be marshaled — callers should
// treat this as a programming bug (the params type must be JSON-safe).
func newRequest(method string, id int, params any) (*Request, error) {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	return &Request{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Method:  method,
		Params:  raw,
	}, nil
}