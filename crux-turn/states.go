package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// StatesConfig wires the default state handlers. Used internally by New();
// callers normally pass Options instead, but Tests can call
// RegisterDefaultStates directly with a custom StatesConfig.
type StatesConfig[Msg, Call, Result any] struct {
	StreamFn    StreamFn[Msg]
	Tools       []ToolSchema[TurnCall]
	ToolFn      ToolFn[Call, Result]
	Dispatcher  Dispatcher[Call, Result]
	MaxRounds   int
	Approval    ApprovalService[Call]
	Adapter     Adapter[Msg, Call, Result]
	OnStreaming func(string)
	OnToolCall  func(Call)
	Logger      *slog.Logger
}

// RegisterDefaultStates installs the default state handlers into m.
// Returns m for chaining.
//
// This is the legacy per-state handler set (streaming → dispatching →
// executing → steering). For AgentRunner-style collapse into a single
// StateAgentRunning, callers should register their own handler instead
// of calling this.
func RegisterDefaultStates[Msg, Call, Result any](m *Machine, cfg StatesConfig[Msg, Call, Result]) *Machine {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxRounds == 0 {
		cfg.MaxRounds = 50
	}
	if cfg.Adapter == nil {
		panic("turn: RegisterDefaultStates requires a non-nil Adapter")
	}

	m.Register(StateReceived, receivedHandler[Msg, Call, Result](cfg))
	m.Register(StateProvisioning, provisioningHandler[Msg, Call, Result](cfg))
	m.Register(StateStreaming, streamingHandler[Msg, Call, Result](cfg))
	m.Register(StateDispatching, dispatchingHandler[Msg, Call, Result](cfg))
	m.Register(StateExecuting, executingHandler[Msg, Call, Result]())
	m.Register(StateAwaitingApproval, awaitingApprovalHandler[Msg, Call, Result](cfg, StateDispatching))
	m.Register(StateSteering, steeringHandler[Msg, Call, Result](cfg))

	return m
}

// received: validate turn, → provisioning.
func receivedHandler[Msg, Call, Result any](cfg StatesConfig[Msg, Call, Result]) StateHandler {
	return func(ctx context.Context, t *Turn, e Event) (State, error) {
		if e.Type != EventStart && e.Type != "resume" {
			return t.State, fmt.Errorf("received: unexpected event %q", e.Type)
		}
		if t.Metadata == nil {
			t.Metadata = make(map[string]string)
		}
		t.Metadata["received_at"] = time.Now().Format(time.RFC3339)
		return StateProvisioning, nil
	}
}

// provisioning: mark provisioned, → streaming.
func provisioningHandler[Msg, Call, Result any](cfg StatesConfig[Msg, Call, Result]) StateHandler {
	return func(ctx context.Context, t *Turn, e Event) (State, error) {
		if t.Metadata == nil {
			t.Metadata = make(map[string]string)
		}
		t.Metadata["provisioned_at"] = time.Now().Format(time.RFC3339)
		return StateStreaming, nil
	}
}

// streaming: call LLM, → dispatching or completed.
func streamingHandler[Msg, Call, Result any](cfg StatesConfig[Msg, Call, Result]) StateHandler {
	return func(ctx context.Context, t *Turn, e Event) (State, error) {
		if cfg.StreamFn == nil {
			return t.State, fmt.Errorf("streaming: no StreamFn configured")
		}
		if t.Round >= cfg.MaxRounds {
			return StateCompleted, nil
		}
		t.Round++

		resp, err := cfg.StreamFn(ctx, cfg.Adapter.AsMessages(t.Messages), cfg.Tools)
		if err != nil {
			return StateFailed, fmt.Errorf("streaming: llm call failed: %w", err)
		}

		t.Messages = append(t.Messages, resp)

		if cfg.OnStreaming != nil && cfg.Adapter.MessageContent(resp) != "" {
			cfg.OnStreaming(cfg.Adapter.MessageContent(resp))
		}
		if cfg.OnToolCall != nil {
			for _, c := range cfg.Adapter.MessageToolCalls(resp) {
				cfg.OnToolCall(c)
			}
		}

		calls := cfg.Adapter.MessageToolCalls(resp)
		if len(calls) > 0 {
			t.Pending = make([]TurnCall, len(calls))
			for i, c := range calls {
				t.Pending[i] = c
			}
			return StateDispatching, nil
		}
		return StateCompleted, nil
	}
}

// dispatching: process all pending tool calls.
//
// Limitation: only one call may enter awaiting_approval per dispatch
// loop. If the LLM returns N tool calls in one assistant message and
// the Nth one needs approval, calls 1..N-1 are NOT processed this
// round — they are dropped from t.Pending when we record awaiting_id
// and return. To support batch approval, the dispatching handler
// would need to either:
//   (a) snapshot t.Pending before the gate, then restore (minus the
//       gated one) on approval resume, or
//   (b) process calls one at a time, awaiting between each.
//
// Neither is implemented today. Single-call approval works correctly;
// batch approval silently drops earlier calls. Document this in any
// caller that constructs multi-call assistant messages.
func dispatchingHandler[Msg, Call, Result any](cfg StatesConfig[Msg, Call, Result]) StateHandler {
	return func(ctx context.Context, t *Turn, e Event) (State, error) {
		if len(t.Pending) == 0 {
			return StateSteering, nil
		}

		for _, pending := range t.Pending {
			call, ok := pending.(Call)
			if !ok {
				return StateFailed, fmt.Errorf("dispatching: pending call is not of type %T", *new(Call))
			}

			result, next, err := dispatchOneTool(ctx, cfg, call)
			if err != nil {
				return next, err
			}
			if next == StateAwaitingApproval {
				if t.Metadata == nil {
					t.Metadata = make(map[string]string)
				}
				t.Metadata["awaiting_call_id"] = cfg.Adapter.CallID(call)
				t.Metadata["awaiting_tool_name"] = cfg.Adapter.CallName(call)

				if cfg.Approval != nil {
					rawArgs := string(cfg.Adapter.CallArgs(call))
					id, err := cfg.Approval.Create(ctx, t.ID, t.SessionID, t.UserID, call, rawArgs)
					if err != nil {
						cfg.Logger.Warn("approval create failed", "err", err, "turn", t.ID)
					} else {
						t.Metadata["awaiting_approval_id"] = id
					}
				}

				t.Messages = append(t.Messages, cfg.Adapter.NewToolMessage(
					cfg.Adapter.CallID(call),
					cfg.Adapter.CallName(call),
					"AWAITING_APPROVAL",
				))
				return StateAwaitingApproval, nil
			}
			t.Messages = append(t.Messages, cfg.Adapter.NewToolMessage(
				cfg.Adapter.ResultID(result),
				cfg.Adapter.CallName(call),
				cfg.Adapter.ResultContent(result),
			))
		}

		t.Pending = nil
		return StateSteering, nil
	}
}

// dispatchOneTool runs a single tool call through dispatcher or tool fn.
//
// Dispatcher semantics (via the DispatchOutcome enum):
//   - OutcomeAllow    → Result is the tool output, continue loop
//   - OutcomeApproval → user-in-the-loop: go to StateAwaitingApproval
//   - OutcomeDeny     → fail turn (policy rejected, no execution)
//   - OutcomeBlocked  → fail turn (hook vetoed after policy allowed)
//
// ToolFn fallback: when no Dispatcher is configured, ToolFn runs the call
// directly with no gating. Use ToolFn for sandbox-free prototyping.
func dispatchOneTool[Msg, Call, Result any](ctx context.Context, cfg StatesConfig[Msg, Call, Result], call Call) (Result, State, error) {
	if cfg.Dispatcher != nil {
		// Dispatcher.Dispatch takes `any` ctx by design (type-agnostic FSM).
		// Pass through the real context.Context; dispatcher implementations
		// do `if c, ok := ctx.(context.Context); ok { ... }`.
		dr, err := cfg.Dispatcher.Dispatch(ctx, call)
		if err != nil {
			var zero Result
			return zero, StateFailed, fmt.Errorf("dispatching: dispatcher error: %w", err)
		}
		switch dr.Outcome {
		case OutcomeDeny:
			return dr.Result, StateFailed, fmt.Errorf("dispatching: denied by %s (rule=%s)", dr.BlockedBy, dr.PolicyRule)
		case OutcomeBlocked:
			return dr.Result, StateFailed, fmt.Errorf("dispatching: blocked by %s (rule=%s)", dr.BlockedBy, dr.PolicyRule)
		case OutcomeApproval:
			// Gate: user must approve before execution. Caller (dispatchingHandler)
			// records this in t.Metadata and switches to StateAwaitingApproval.
			return dr.Result, StateAwaitingApproval, nil
		case OutcomeAllow, "":
			// Empty Outcome is treated as Allow for backward compatibility with
			// Dispatcher implementations that haven't been updated yet. New
			// dispatchers MUST set Outcome explicitly.
			return dr.Result, StateDispatching, nil
		default:
			var zero Result
			return zero, StateFailed, fmt.Errorf("dispatching: unknown DispatchOutcome %q", dr.Outcome)
		}
	}
	if cfg.ToolFn != nil {
		result := cfg.ToolFn(ctx, call)
		return result, StateDispatching, nil
	}
	var zero Result
	return zero, StateDispatching, fmt.Errorf("no executor configured")
}

// executing: passthrough (sandboxed execution reserved for future).
func executingHandler[Msg, Call, Result any]() StateHandler {
	return func(ctx context.Context, t *Turn, e Event) (State, error) {
		return StateDispatching, nil
	}
}

// awaiting_approval: wait for external trigger event.
func awaitingApprovalHandler[Msg, Call, Result any](cfg StatesConfig[Msg, Call, Result], postApproval State) StateHandler {
	return func(ctx context.Context, t *Turn, e Event) (State, error) {
		switch e.Type {
		case EventApprovalResolved:
			callID := t.Metadata["awaiting_call_id"]
			if callID == "" {
				return StateFailed, fmt.Errorf("approval resolved but no awaiting_call_id recorded")
			}
			// Pop the AWAITING_APPROVAL placeholder message.
			if len(t.Messages) > 0 {
				last := t.Messages[len(t.Messages)-1]
				if lm, ok := last.(Msg); ok && cfg.Adapter.MessageRole(lm) == "tool" && cfg.Adapter.MessageContent(lm) == "AWAITING_APPROVAL" {
					t.Messages = t.Messages[:len(t.Messages)-1]
				}
			}
			// Find the original tool call from the assistant message(s).
			for i := len(t.Messages) - 1; i >= 0; i-- {
				m, ok := t.Messages[i].(Msg)
				if !ok || cfg.Adapter.MessageRole(m) != "assistant" {
					continue
				}
				for _, c := range cfg.Adapter.MessageToolCalls(m) {
					if cfg.Adapter.CallID(c) == callID {
						t.Pending = []TurnCall{c}
						t.Metadata["awaiting_call_id"] = ""
						t.Metadata["awaiting_tool_name"] = ""
						if postApproval == "" {
							return StateDispatching, nil
						}
						return postApproval, nil
					}
				}
			}
			return StateFailed, fmt.Errorf("approval resolved but original tool call %s not found", callID)
		case EventApprovalDenied:
			return StateFailed, fmt.Errorf("denied by user")
		case EventAbort:
			return StateFailed, fmt.Errorf("aborted")
		default:
			return t.State, fmt.Errorf("awaiting_approval: unexpected event %q", e.Type)
		}
	}
}

// steering: check max_rounds, → streaming or completed.
func steeringHandler[Msg, Call, Result any](cfg StatesConfig[Msg, Call, Result]) StateHandler {
	return func(ctx context.Context, t *Turn, e Event) (State, error) {
		if t.Round < cfg.MaxRounds {
			return StateStreaming, nil
		}
		return StateCompleted, nil
	}
}

// Avoid unused import warning when json is referenced only in this file.
var _ = json.RawMessage(nil)