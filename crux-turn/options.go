package turn

import (
	"context"
	"log/slog"
)

// StreamFn is the LLM streaming function. The agent loop calls
// it once per LLM round; the function returns the final assistant
// message.
type StreamFn[Msg any] func(ctx context.Context, msgs []Msg, tools []ToolSchema[TurnCall]) (Msg, error)

// ToolFn executes a single tool call directly. Used when no Dispatcher is set.
type ToolFn[Call, Result any] func(ctx context.Context, call Call) Result

// ApprovalService is the minimal interface for human-in-the-loop approval.
// Consumers (e.g. crux-harness/approval.Service) implement this; the FSM
// stores the resulting request ID in turn.Metadata["awaiting_approval_id"]
// so HTTP resolvers can find it.
type ApprovalService[Call any] interface {
	Create(ctx context.Context, turnID, sessionID, userID string, call Call, rawArgs string) (id string, err error)
	Resolve(ctx context.Context, id, decidedBy, reason string, approved bool) error
	Get(ctx context.Context, id string) (ApprovalRequest[Call], error)
}

// ApprovalRequest is the persisted record of an awaiting-approval call.
type ApprovalRequest[Call any] struct {
	ID         string
	TurnID     string
	SessionID  string
	UserID     string
	Call       Call
	RawArgs    string
	Decision   string // "pending" | "approved" | "denied" (new field, defaults to "pending")
	Resolved   bool
	Approved   bool
	DecidedBy  string
	Reason     string
	CreatedAt  string
	ResolvedAt string
}

// Config holds the resolved set of Machine options. Built by applyOptions
// from the variadic Option list passed to New.
type Config[Msg, Call, Result any] struct {
	streamFn    StreamFn[Msg]
	tools       []ToolSchema[TurnCall]
	toolFn      ToolFn[Call, Result]
	dispatcher  Dispatcher[Call, Result]
	maxRounds   int
	approval    ApprovalService[Call]
	logger      *slog.Logger
	trigger     Trigger
	onStreaming func(string)
	onToolCall  func(Call)
}

// Option configures a Machine at construction time.
type Option[Msg, Call, Result any] func(*Config[Msg, Call, Result])

// WithMaxRounds caps the number of LLM calls per turn. Default 50.
func WithMaxRounds[Msg, Call, Result any](n int) Option[Msg, Call, Result] {
	return func(c *Config[Msg, Call, Result]) { c.maxRounds = n }
}

// WithStreamFn sets the LLM streaming function. Required.
func WithStreamFn[Msg, Call, Result any](fn StreamFn[Msg]) Option[Msg, Call, Result] {
	return func(c *Config[Msg, Call, Result]) { c.streamFn = fn }
}

// WithTools sets the tool schemas exposed to the LLM.
func WithTools[Msg, Call, Result any](tools []ToolSchema[TurnCall]) Option[Msg, Call, Result] {
	return func(c *Config[Msg, Call, Result]) { c.tools = tools }
}

// WithToolFn sets a direct tool executor (no Dispatcher / approval).
func WithToolFn[Msg, Call, Result any](fn ToolFn[Call, Result]) Option[Msg, Call, Result] {
	return func(c *Config[Msg, Call, Result]) { c.toolFn = fn }
}

// WithDispatcher sets the dispatch chokepoint (policy + hooks + execute).
func WithDispatcher[Msg, Call, Result any](d Dispatcher[Call, Result]) Option[Msg, Call, Result] {
	return func(c *Config[Msg, Call, Result]) { c.dispatcher = d }
}

// WithApprovalService sets the human-in-the-loop approval service.
func WithApprovalService[Msg, Call, Result any](a ApprovalService[Call]) Option[Msg, Call, Result] {
	return func(c *Config[Msg, Call, Result]) { c.approval = a }
}

// WithLogger sets the structured logger. Defaults to slog.Default().
func WithLogger[Msg, Call, Result any](l *slog.Logger) Option[Msg, Call, Result] {
	return func(c *Config[Msg, Call, Result]) { c.logger = l }
}

// WithTrigger sets the trigger (default: in-process ChannelTrigger).
func WithTrigger[Msg, Call, Result any](t Trigger) Option[Msg, Call, Result] {
	return func(c *Config[Msg, Call, Result]) { c.trigger = t }
}

// WithOnStreaming registers a callback for each streamed content chunk.
func WithOnStreaming[Msg, Call, Result any](fn func(string)) Option[Msg, Call, Result] {
	return func(c *Config[Msg, Call, Result]) { c.onStreaming = fn }
}

// WithOnToolCall registers a callback for each tool call dispatched.
func WithOnToolCall[Msg, Call, Result any](fn func(Call)) Option[Msg, Call, Result] {
	return func(c *Config[Msg, Call, Result]) { c.onToolCall = fn }
}

// applyOptions applies a list of options to a fresh Config, with defaults.
func applyOptions[Msg, Call, Result any](opts []Option[Msg, Call, Result]) Config[Msg, Call, Result] {
	c := Config[Msg, Call, Result]{maxRounds: 50}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}