// Package plugin implements a JSON-RPC 2.0 plugin system for cruxd.
//
// Plugins are independent subprocesses that communicate with the host via
// stdin/stdout using newline-delimited JSON-RPC 2.0 frames. stderr is
// reserved for plugin logs.
//
// Protocol reference: docs/modules/29-crux-plugin.md
package plugin

import (
	"encoding/json"
	"time"
)

// JSON-RPC 2.0 protocol constants.
const (
	JSONRPCVersion = "2.0"

	// Standard JSON-RPC error codes (subset we use).
	ErrCodeParse          = -32700
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603
)

// Standard JSON-RPC method names (host → plugin).
const (
	MethodInitialize   = "initialize"
	MethodShutdown     = "shutdown"
	MethodChannelSend  = "channel.send"
	MethodToolList     = "tool.list"
	MethodToolExecute  = "tool.execute"
	MethodHookRegister = "hook.register"
	MethodHookFire     = "hook.fire"

	// Reverse direction methods (plugin → host notifications, no id).
	MethodMessageInbound = "message.inbound"
	MethodChatSend      = "chat.send"
)

// Hook point names (snake_case, wire format).
// v1 only supports the 3 points that map to existing crux hooks.Event types.
// BeforeModelCall / AfterModelCall require Event schema extension (v2).
const (
	HookPointBeforeToolCall = "before_tool_call"
	HookPointAfterToolCall  = "after_tool_call"
	HookPointTurnEnd        = "turn_end"
)

// HookCallTimeout is the deadline for synchronous plugin RPC calls
// (tool.execute, hook.fire sync mode). Async calls (Notify) have no deadline.
const HookCallTimeout = 10 * time.Second

// Request is a JSON-RPC 2.0 request from host → plugin.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      int             `json:"id"`
}

// Response is a JSON-RPC 2.0 response from plugin → host.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	ID      int             `json:"id"`
}

// Notification is a JSON-RPC 2.0 notification (no id) plugin → host.
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *RPCError) Error() string { return e.Message }

// --- initialize ---

// InitializeParams is sent with the initialize method.
type InitializeParams struct {
	Config map[string]interface{} `json:"config"`
}

// InitializeResult is returned from initialize.
type InitializeResult struct {
	Status string `json:"status"`
}

// --- channel.send ---

// ChannelSendParams is sent with channel.send.
type ChannelSendParams struct {
	ChatID string `json:"chatId"`
	Text   string `json:"text"`
}

// --- tool.list / tool.execute ---

// ToolListResult is returned from tool.list.
type ToolListResult struct {
	Tools []ToolDef `json:"tools"`
}

// ToolDef describes a tool provided by a plugin.
type ToolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

// ToolExecuteParams is sent with tool.execute.
type ToolExecuteParams struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// ToolExecuteResult is returned from tool.execute.
type ToolExecuteResult struct {
	Result string `json:"result"`
}

// --- hook.register / hook.fire ---

// HookRegisterResult is returned from hook.register.
// The plugin advertises which hook points it wants to receive.
type HookRegisterResult struct {
	Points []string `json:"points"`
}

// HookFireParams is sent with hook.fire (host → plugin).
//
// Maps to crux hooks.Event for tool/turn lifecycle events.
type HookFireParams struct {
	Point     string `json:"point"`
	AgentName string `json:"agentName,omitempty"`
	Channel   string `json:"channel,omitempty"`
	AccountID string `json:"accountId,omitempty"`
	ChatID    string `json:"chatId,omitempty"`
	UserID    string `json:"userId,omitempty"`

	// Tool fields (BeforeToolCall / AfterToolCall).
	ToolName   string `json:"toolName,omitempty"`
	ToolArgs   string `json:"toolArgs,omitempty"`
	ToolResult string `json:"toolResult,omitempty"`
}

// HookFireResult is returned from hook.fire (sync hooks).
//
// Maps to crux hooks.Decision. Plugin uses Block to deny the tool call,
// Modify to rewrite arguments (advisory in crux v1).
type HookFireResult struct {
	Block  bool           `json:"block,omitempty"`
	Reason string         `json:"reason,omitempty"`
	Modify map[string]any `json:"modify,omitempty"`
}

// --- message.inbound (plugin → host notification) ---

// InboundMessageParams is sent by channel plugins via message.inbound.
type InboundMessageParams struct {
	Channel    string `json:"channel"`
	ChatID     string `json:"chatId"`
	UserID     string `json:"userId"`
	Text       string `json:"text"`
	PeerKind   string `json:"peerKind,omitempty"`
	SenderName string `json:"senderName,omitempty"`
}

// --- chat.send (plugin → host notification) ---

// ChatSendParams: plugin pushes an outbound message to a chat.
//
// Distinct from message.inbound (which triggers an agent turn): chat.send
// delivers to the user without invoking the agent again. Used by post-turn
// hook plugins (TTS, translation, summary).
type ChatSendParams struct {
	Channel   string          `json:"channel"`
	AccountID string          `json:"accountId,omitempty"`
	ChatID    string          `json:"chatId"`
	AgentID   string          `json:"agentId,omitempty"`
	Text      string          `json:"text,omitempty"`
	Media     []ChatSendMedia `json:"media,omitempty"`
}

// ChatSendMedia is one attachment in ChatSendParams. BytesB64 holds the
// file's bytes base64-encoded so JSON-RPC can ship binary over stdin/stdout.
type ChatSendMedia struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType,omitempty"`
	BytesB64    string `json:"bytesB64"`
}

// --- helpers ---

// NewRequest constructs a JSON-RPC 2.0 request.
func NewRequest(method string, params interface{}, id int) (*Request, error) {
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
		Method:  method,
		Params:  raw,
		ID:      id,
	}, nil
}

// NewNotification constructs a JSON-RPC 2.0 notification (no id).
func NewNotification(method string, params interface{}) (*Notification, error) {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	return &Notification{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  raw,
	}, nil
}