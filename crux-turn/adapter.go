package turn

import (
	"encoding/json"
)

// TurnMsg is a type alias for `any` representing any consumer-supplied
// message type. The FSM operates on this opaquely; concrete semantics
// (Role, Content, ToolCalls) are extracted via Adapter.
type TurnMsg = any

// TurnCall is a type alias for `any` representing any consumer-supplied
// tool call type. Extracted via Adapter.
type TurnCall = any

// TurnResult is a type alias for `any` representing any consumer-supplied
// tool result type. Extracted via Adapter.
type TurnResult = any

// ToolSchema is the JSON-schema-style description of a tool that the
// LLM can call. Call is the consumer's tool call type — but ToolSchema
// is independent of Call (just describes what the LLM sees).
type ToolSchema[Call any] struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema for args
}

// Adapter bridges consumer message/call/result types to the FSM.
//
// Why: crux-harness/turn/ used to import crux-ai.Message directly,
// making the FSM unusable outside of crux-ai consumers. Adapter decouples
// the FSM core from any specific message format. Consumers implement
// these methods (typically as a zero-size struct: `type MyAdapter struct{}`).
//
// All methods are pure functions over the consumer's types — no hidden
// state, no goroutines, no I/O. The FSM is responsible for sequencing;
// Adapter is responsible for type translation.
type Adapter[Msg, Call, Result any] interface {
	// --- Message accessors ---
	MessageRole(msg Msg) string
	MessageContent(msg Msg) string
	MessageToolCalls(msg Msg) []Call

	// --- Message constructors (used by FSM when adding new messages) ---
	NewAssistantMessage(content string, calls []Call) Msg
	NewToolMessage(callID, name, content string) Msg

	// --- TurnMsg <-> Msg conversion (FSM stores []TurnMsg, handlers take []Msg) ---
	AsMsg(tm TurnMsg) (Msg, bool)            // FSM: TurnMsg -> Msg; ok=false on type mismatch
	AsMessages(tms []TurnMsg) []Msg          // FSM: bulk convert for handler calls

	// --- Tool call accessors ---
	CallID(call Call) string
	CallName(call Call) string
	CallArgs(call Call) json.RawMessage

	// --- Tool result accessors ---
	ResultID(result Result) string
	ResultContent(result Result) string
	ResultIsError(result Result) bool
}

// Dispatcher is the minimal interface for tool dispatch with optional
// approval gating. Consumers (e.g. crux-harness) implement this to
// plug in policy + hooks + execute.
type Dispatcher[Call, Result any] interface {
	Dispatch(ctx any, call Call) (DispatchResult[Result], error)
}

// DispatchOutcome is a mutually exclusive classification of a
// single dispatch decision. Exactly one of these applies per call:
//
//   - OutcomeAllow:    policy allowed and tool executed; Result is the output
//   - OutcomeApproval: policy requires human approval; gate before executing
//   - OutcomeDeny:     policy denied or hook vetoed; do not execute, fail the turn
//   - OutcomeBlocked:  a hook Block=true vetoed execution after policy allowed
//
// This replaces the prior two-bool-and-string pattern (Approved +
// Denied + BlockedBy) where Allowed=Approved=false&Denied=false was
// the "no, just executed" case — easy to misconstruct (e.g. both
// bools true) and the dispatching handler had to check three fields
// in priority order. The enum makes the cases explicit and the
// switch in the dispatcher exhaustiveness-checkable.
//
// BlockedBy / PolicyRule on DispatchResult retain their string
// diagnostic values regardless of outcome — useful for log lines
// and the `dispatching: denied by %s` error message.
type DispatchOutcome string

const (
	OutcomeAllow    DispatchOutcome = "allow"
	OutcomeApproval DispatchOutcome = "approval"
	OutcomeDeny     DispatchOutcome = "deny"
	OutcomeBlocked  DispatchOutcome = "blocked"
)

// DispatchResult is the result of a single dispatch.
type DispatchResult[Result any] struct {
	Result     Result
	Outcome    DispatchOutcome // mutually exclusive: see DispatchOutcome constants
	BlockedBy  string          // which layer produced the decision (e.g. "policy", "fanout:<hook>"); empty if OutcomeAllow
	PolicyRule string          // the rule that triggered Deny / Approval (empty for Allow/Blocked)
}