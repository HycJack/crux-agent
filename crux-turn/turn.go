// Package turn implements the Turn FSM — a finite state machine that
// drives a single agent turn (user input → complete response) through
// 9 explicit states, with persistence and reactive triggers.
//
// This package is type-agnostic: the FSM operates on consumer-supplied
// message types via the MessageAdapter interface (see adapter.go).
//
// State machine:
//
//	received → provisioning → streaming → dispatching ──→ completed
//	                                      ↓          ↑
//	                                 awaiting    executing
//	                                 approval        ↓
//	                                      ↑      steering
//	                                   (resolve)    / \
//	                                      └───────+   +→ completed
package turn

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// State is one of the 9 turn states.
type State string

const (
	StateReceived         State = "received"
	StateProvisioning     State = "provisioning"
	StateStreaming        State = "streaming"
	StateDispatching      State = "dispatching"
	StateAwaitingApproval State = "awaiting_approval"
	StateExecuting        State = "executing"
	StateSteering         State = "steering"
	StateAgentRunning     State = "agent_running"
	StateCompleted        State = "completed"
	StateFailed           State = "failed"
)

// IsTerminal reports whether the state is a terminal state.
func (s State) IsTerminal() bool {
	return s == StateCompleted || s == StateFailed
}

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	switch s {
	case StateReceived, StateProvisioning, StateStreaming, StateDispatching,
		StateAwaitingApproval, StateExecuting, StateSteering,
		StateAgentRunning,
		StateCompleted, StateFailed:
		return true
	}
	return false
}

// Event triggers a state transition.
type Event struct {
	Type      string // e.g. "stream_start", "tool_needs_approval", "approval_resolved"
	Payload   any    // event-specific data
	Timestamp time.Time
}

// Common event types used across handlers.
const (
	EventStart            = "start"
	EventProvisioned      = "provisioned"
	EventStreamStart      = "stream_start"
	EventStreamDone       = "stream_done"
	EventToolDispatched   = "tool_dispatched"
	EventToolExecuted     = "tool_executed"
	EventApprovalResolved = "approval_resolved"
	EventApprovalDenied   = "approval_denied"
	EventSteerContinue    = "steer_continue"
	EventSteerStop        = "steer_stop"
	EventAbort            = "abort"
)

// Turn is the persistent state of a single agent turn.
// TurnMsg is the consumer's message type; it's marshaled to JSON for storage.
type Turn struct {
	ID        string            `json:"id"`
	SessionID string            `json:"session_id"`
	UserID    string            `json:"user_id,omitempty"`
	AgentID   string            `json:"agent_id,omitempty"`
	State     State             `json:"state"`
	Round     int               `json:"round"`
	// Messages is the full conversation history for this turn.
	// Stored as []TurnMsg (any), serialized as JSON array.
	Messages  []TurnMsg         `json:"messages"`
	Pending   []TurnCall        `json:"pending,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Error     string            `json:"error,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Store persists turn state.
//
// Implementations MUST deep-copy returned *Turn values to prevent external
// mutation of stored state. The in-memory MemoryStore in store.go and the
// SQLite store in sqlite/ both follow this contract.
type Store interface {
	Save(ctx context.Context, turn *Turn) error
	Load(ctx context.Context, id string) (*Turn, error)
	List(ctx context.Context, sessionID string) ([]*Turn, error)
}

// Trigger signals external events back to the FSM.
// ChannelTrigger for in-process; DBTrigger / RedisTrigger can implement the same interface.
type Trigger interface {
	Wait(ctx context.Context) (Event, error)
	Fire(event Event) error
}

// StateHandler is the work for a single state. It receives the turn
// and the triggering event, performs work, and returns the next state.
type StateHandler func(ctx context.Context, turn *Turn, event Event) (State, error)

// Machine is the FSM executor. It is type-parameterized over the
// consumer's message type (Msg), tool call type (Call), and tool
// result type (Result).
type Machine struct {
	store   Store
	trigger Trigger
	logger  *slog.Logger

	mu       sync.RWMutex
	handlers map[State]StateHandler

	// turnTriggers is per-turn channels for in-process triggers (keyed by turn ID).
	turnTriggers map[string]chan Event
	tmu          sync.Mutex
}

// New creates a new FSM with the given store and trigger.
//
// Type-parameterization lets the FSM operate on consumer-supplied
// message/call/result types via the Adapter.
func New[Msg, Call, Result any](
	store Store,
	adapter Adapter[Msg, Call, Result],
	opts ...Option[Msg, Call, Result],
) *Machine {
	cfg := applyOptions(opts)
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}
	if cfg.trigger == nil {
		cfg.trigger = NewChannelTrigger()
	}
	m := &Machine{
		store:        store,
		trigger:      cfg.trigger,
		logger:       cfg.logger,
		handlers:     make(map[State]StateHandler),
		turnTriggers: make(map[string]chan Event),
	}
	RegisterDefaultStates(m, StatesConfig[Msg, Call, Result]{
		StreamFn:    cfg.streamFn,
		Tools:       cfg.tools,
		ToolFn:      cfg.toolFn,
		Dispatcher:  cfg.dispatcher,
		MaxRounds:   cfg.maxRounds,
		Approval:    cfg.approval,
		Adapter:     adapter,
		OnStreaming: cfg.onStreaming,
		OnToolCall:  cfg.onToolCall,
		Logger:      cfg.logger,
	})
	return m
}

// Register binds a handler to a state.
func (m *Machine) Register(state State, handler StateHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[state] = handler
}

// handlerFor returns the registered handler (or nil if none).
func (m *Machine) handlerFor(state State) StateHandler {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.handlers[state]
}

// Send injects an external event into a specific turn.
// For in-process triggers, this uses the per-turn channel.
// For DB/Redis triggers, this writes to the external store.
func (m *Machine) Send(ctx context.Context, turnID string, event Event) error {
	m.tmu.Lock()
	ch, ok := m.turnTriggers[turnID]
	m.tmu.Unlock()
	if !ok {
		// Fallback: use the global trigger
		return m.trigger.Fire(event)
	}
	select {
	case ch <- event:
		return nil
	default:
		return fmt.Errorf("trigger channel full for turn %s", turnID)
	}
}

// Start initializes a new turn and runs it through the FSM until it
// reaches a terminal state or blocks on an external trigger.
func (m *Machine) Start(ctx context.Context, turn *Turn) error {
	if turn.ID == "" {
		return fmt.Errorf("turn.ID is required")
	}
	if turn.State == "" {
		turn.State = StateReceived
	}
	if !turn.State.Valid() {
		return fmt.Errorf("invalid initial state %q", turn.State)
	}
	now := time.Now()
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = now
	}
	turn.UpdatedAt = now

	// Persist initial state
	if err := m.store.Save(ctx, turn); err != nil {
		return fmt.Errorf("save initial turn: %w", err)
	}

	// Send the start event to drive the FSM
	return m.runUntilBlock(ctx, turn, Event{Type: EventStart, Timestamp: now})
}

// Resume loads a turn from store and continues execution.
func (m *Machine) Resume(ctx context.Context, turnID string) error {
	turn, err := m.store.Load(ctx, turnID)
	if err != nil {
		return fmt.Errorf("load turn: %w", err)
	}
	if turn.State.IsTerminal() {
		return fmt.Errorf("turn %s is already in terminal state %s", turnID, turn.State)
	}
	eventType := "resume"
	if turn.State == StateAwaitingApproval {
		eventType = EventApprovalResolved
	}
	return m.runUntilBlock(ctx, turn, Event{Type: eventType, Timestamp: time.Now()})
}

// PendingTriggerCount returns the number of per-turn trigger channels
// currently held by the machine. Exported for tests; production code
// should not need this (channels are cleaned up automatically by
// runUntilBlock on every exit path).
func (m *Machine) PendingTriggerCount() int {
	m.tmu.Lock()
	defer m.tmu.Unlock()
	return len(m.turnTriggers)
}

// runUntilBlock drives the FSM, calling handlers in sequence, until
// the FSM either reaches a terminal state or blocks waiting for an
// external trigger.
//
// The per-turn trigger channel (created lazily by ensureTriggerChannel
// inside waitForTrigger) must be cleaned up on every exit path —
// including error paths — or the machine accumulates dead channels.
// We achieve this by:
//   1. Tracking whether waitForTrigger was entered (channel exists)
//   2. deferring cleanupTrigger to fire on any return
//   3. Only deleting when channelExists is true
//
// Without this, ctx cancellation during await, Save failure after a
// successful wait, or panic-recovery paths would all leak a channel
// per turn for the machine's lifetime.
func (m *Machine) runUntilBlock(ctx context.Context, turn *Turn, event Event) error {
	channelExists := false
	defer func() {
		if channelExists {
			m.cleanupTrigger(turn.ID)
		}
	}()

	current := turn.State
	for {
		if current.IsTerminal() {
			m.logger.Debug("turn reached terminal state", "turn", turn.ID, "state", current)
			return nil
		}

		handler := m.handlerFor(current)
		if handler == nil {
			return fmt.Errorf("no handler for state %s", current)
		}

		m.logger.Debug("turn entering state", "turn", turn.ID, "state", current, "event", event.Type)

		// safeInvoke wraps handler call with panic recovery so a bug in
		// user-supplied callbacks (StreamFn / Dispatcher / ToolFn /
		// OnStreaming / OnToolCall / Approval.Create) cannot crash the
		// daemon. A panic is converted to a normal handler error path,
		// which marks the turn StateFailed and persists the error.
		//
		// Why this matters: before this, a single bad tool callback
		// would propagate the panic up through runUntilBlock, skipping
		// the deferred cleanupTrigger and the Save of the failed state.
		// The result was a leaked channel AND a turn left in a
		// non-terminal state that no future Resume could recover.
		next, err := safeInvoke(ctx, m.logger, current, handler, ctx, turn, event)
		if err != nil {
			turn.State = StateFailed
			turn.Error = err.Error()
			turn.UpdatedAt = time.Now()
			_ = m.store.Save(ctx, turn)
			return fmt.Errorf("handler error in state %s: %w", current, err)
		}

		if !next.Valid() {
			return fmt.Errorf("handler for state %s returned invalid state %q", current, next)
		}

		turn.State = next
		turn.UpdatedAt = time.Now()
		if err := m.store.Save(ctx, turn); err != nil {
			return fmt.Errorf("save turn after transition: %w", err)
		}

		// If we just entered awaiting_approval, wait for external event
		if next == StateAwaitingApproval {
			m.logger.Info("turn blocked, awaiting external trigger", "turn", turn.ID)
			channelExists = true
			ev, err := m.waitForTrigger(ctx, turn.ID)
			if err != nil {
				return fmt.Errorf("wait for trigger: %w", err)
			}
			event = ev
			current = next
			continue
		}

		// If we just entered completed/failed, terminal — done
		if next.IsTerminal() {
			return nil
		}

		// Self-trigger: continue with the same event for streaming/dispatching loops
		current = next
		// Reset event to a generic "continue" so the next handler doesn't see stale type
		event = Event{Type: "continue", Timestamp: time.Now()}
	}
}

// waitForTrigger blocks until an external event arrives for this turn.
func (m *Machine) waitForTrigger(ctx context.Context, turnID string) (Event, error) {
	ch := m.ensureTriggerChannel(turnID)
	select {
	case ev := <-ch:
		return ev, nil
	case <-ctx.Done():
		return Event{}, ctx.Err()
	}
}

func (m *Machine) ensureTriggerChannel(turnID string) chan Event {
	m.tmu.Lock()
	defer m.tmu.Unlock()
	ch, ok := m.turnTriggers[turnID]
	if !ok {
		ch = make(chan Event, 1)
		m.turnTriggers[turnID] = ch
	}
	return ch
}

func (m *Machine) cleanupTrigger(turnID string) {
	m.tmu.Lock()
	defer m.tmu.Unlock()
	if ch, ok := m.turnTriggers[turnID]; ok {
		close(ch)
		delete(m.turnTriggers, turnID)
	}
}

// safeInvoke calls a StateHandler with panic recovery.
//
// A panic inside a consumer-supplied callback (StreamFn, Dispatcher,
// ToolFn, OnStreaming, OnToolCall, Approval.Create, or even the
// framework's own default handlers if they panic on bad input) is
// converted into a regular error, preserving the daemon's
// crash-resistance contract.
//
// The recovered error includes the state name, the panic value, and
// the stack trace so the failure is debuggable from logs alone.
//
// The logger is the Machine's structured logger (may be nil → uses
// slog.Default() via the caller). state is included in the error
// message and the log record for correlation.
func safeInvoke(ctx context.Context, logger *slog.Logger, state State, handler StateHandler, hctx context.Context, turn *Turn, event Event) (result State, err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			if logger != nil {
				logger.Error("handler panicked",
					"turn", turn.ID,
					"state", state,
					"panic", fmt.Sprintf("%v", r),
					"stack", string(stack),
				)
			}
			result = StateFailed
			err = fmt.Errorf("handler panicked in state %s: %v", state, r)
		}
	}()
	return handler(hctx, turn, event)
}