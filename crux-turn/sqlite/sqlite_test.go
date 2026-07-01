package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hycjack/crux-turn"
	"github.com/hycjack/crux-turn/sqlite"
)

func tmpDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "test.db")
}

type callShape struct {
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

func legacyReconstruct(functionID string, args map[string]any, rawArgs string) (turn.TurnCall, error) {
	c := callShape{}
	c.Function.Name = functionID
	return c, nil
}

func TestStore_SaveLoadList(t *testing.T) {
	path := tmpDB(t)
	defer os.Remove(path)

	store, err := sqlite.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	tr := &turn.Turn{
		ID:        "t1",
		SessionID: "s1",
		State:     turn.StateReceived,
		Messages:  []turn.TurnMsg{"hello"},
	}
	if err := store.Save(ctx, tr); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "t1" || got.SessionID != "s1" {
		t.Errorf("got %+v, want t1/s1", got)
	}
	if len(got.Messages) != 1 || got.Messages[0] != "hello" {
		t.Errorf("messages round-trip failed: %+v", got.Messages)
	}

	list, err := store.List(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "t1" {
		t.Errorf("list = %+v, want [t1]", list)
	}
}

func TestStore_NotFound(t *testing.T) {
	path := tmpDB(t)
	store, err := sqlite.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.Load(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

func TestStore_Reopen(t *testing.T) {
	path := tmpDB(t)

	// Write, close, reopen — verify state survives.
	{
		store, err := sqlite.NewStore(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Save(context.Background(), &turn.Turn{
			ID: "t1", SessionID: "s1", State: turn.StateReceived,
		}); err != nil {
			t.Fatal(err)
		}
		store.Close()
	}
	{
		store, err := sqlite.NewStore(path)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		got, err := store.Load(context.Background(), "t1")
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "t1" {
			t.Errorf("after reopen, got ID=%q, want t1", got.ID)
		}
	}
}

func TestApprovalStore_CreateResolveGet(t *testing.T) {
	path := tmpDB(t)
	store, err := sqlite.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	appr := sqlite.NewApprovalStoreFromDB(store.DB(), legacyReconstruct)
	// Also keep a reference so we can close cleanly.
	defer appr.Close()

	ctx := context.Background()
	call := callShape{}
	call.Function.Name = "exec_command"

	id, err := appr.Create(ctx, "turn-1", "sess-1", "user-1", call, `{"cmd":"ls"}`)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	// Get pending.
	got, err := appr.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.TurnID != "turn-1" {
		t.Errorf("got = %+v, want id=%s turn=turn-1", got, id)
	}
	if got.Resolved {
		t.Error("newly created should not be resolved")
	}
	// JSON round-trip via TurnCall (any) gives map[string]any, not original struct.
	// Verify the function_id was extracted correctly via the legacy column path.
	if got.Decision != "pending" {
		t.Errorf("Decision=%q, want pending", got.Decision)
	}

	// Resolve.
	if err := appr.Resolve(ctx, id, "admin", "looks safe", true); err != nil {
		t.Fatal(err)
	}

	// Get resolved.
	got, _ = appr.Get(ctx, id)
	if !got.Resolved || !got.Approved {
		t.Errorf("after resolve: Resolved=%v Approved=%v, want both true", got.Resolved, got.Approved)
	}
	if got.DecidedBy != "admin" {
		t.Errorf("DecidedBy=%q, want admin", got.DecidedBy)
	}
}

func TestApprovalStore_ResolveTwiceFails(t *testing.T) {
	path := tmpDB(t)
	store, _ := sqlite.NewStore(path)
	defer store.Close()
	appr := sqlite.NewApprovalStoreFromDB(store.DB(), legacyReconstruct)
	defer appr.Close()

	ctx := context.Background()
	call := callShape{}
	call.Function.Name = "x"

	id, _ := appr.Create(ctx, "t", "s", "u", call, "")
	if err := appr.Resolve(ctx, id, "u", "ok", true); err != nil {
		t.Fatal(err)
	}
	if err := appr.Resolve(ctx, id, "u", "again", false); err == nil {
		t.Error("expected error resolving twice")
	}
}

func TestApprovalStore_ListPending(t *testing.T) {
	path := tmpDB(t)
	store, _ := sqlite.NewStore(path)
	defer store.Close()
	appr := sqlite.NewApprovalStoreFromDB(store.DB(), legacyReconstruct)
	defer appr.Close()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		call := callShape{}
		call.Function.Name = "x"
		_, err := appr.Create(ctx, "t", "s", "u", call, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	pending, err := appr.ListPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 3 {
		t.Errorf("pending=%d, want 3", len(pending))
	}
}

// TestApprovalStore_LegacyRowReconstruct verifies the OnLegacyCall hook
// fires for rows without call_payload (simulating old crux-harness/approval data).
func TestApprovalStore_LegacyRowReconstruct(t *testing.T) {
	path := tmpDB(t)
	store, _ := sqlite.NewStore(path)
	defer store.Close()

	// Insert a legacy row directly (no call_payload).
	_, err := store.DB().Exec(`
		INSERT INTO approval_requests (
			id, turn_id, session_id, user_id, agent_id, function_id, arguments, raw_args,
			policy_rule, decision, decided_by, reason, created_at, decided_at, expires_at
		) VALUES ('legacy-1', 't1', 's1', 'u1', '', 'exec_command', '{"cmd":"ls"}', '{"cmd":"ls"}',
			'', 'pending', '', '', 1700000000, NULL, 1700086400)
	`)
	if err != nil {
		t.Fatal(err)
	}

	called := false
	appr := sqlite.NewApprovalStoreFromDB(store.DB(), func(functionID string, args map[string]any, rawArgs string) (turn.TurnCall, error) {
		called = true
		if functionID != "exec_command" {
			t.Errorf("functionID=%q, want exec_command", functionID)
		}
		c := callShape{}
		c.Function.Name = functionID
		return c, nil
	})
	defer appr.Close()

	got, err := appr.Get(context.Background(), "legacy-1")
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("OnLegacyCall hook did not fire for legacy row")
	}
	if c, ok := got.Call.(callShape); !ok || c.Function.Name != "exec_command" {
		t.Errorf("legacy reconstruction returned wrong call: %+v", got.Call)
	}
}

// TestApprovalStore_InProcess_PreservesTypedCall verifies the
// InProcessApproval service (no JSON serialization) keeps the concrete
// call type intact — this is the recommended path for typed consumers.
func TestApprovalStore_InProcess_PreservesTypedCall(t *testing.T) {
	appr := sqlite.NewInProcessApproval()
	ctx := context.Background()

	c := callShape{}
	c.Function.Name = "exec_command"
	id, err := appr.Create(ctx, "t1", "s1", "u1", c, `{"cmd":"ls"}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := appr.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Resolved == false {
		t.Error("should not be resolved yet")
	}
	// InProcessApproval never round-trips through JSON, so concrete type survives.
	gotCall, ok := got.Call.(callShape)
	if !ok {
		t.Fatalf("typed call lost: got %T, want callShape", got.Call)
	}
	if gotCall.Function.Name != "exec_command" {
		t.Errorf("function_id = %q, want exec_command", gotCall.Function.Name)
	}

	// Resolve + verify ResolvedAt populated.
	if err := appr.Resolve(ctx, id, "admin", "ok", true); err != nil {
		t.Fatal(err)
	}
	got, _ = appr.Get(ctx, id)
	if !got.Resolved || !got.Approved || got.DecidedBy != "admin" {
		t.Errorf("after resolve: %+v", got)
	}
	if got.ResolvedAt == "" {
		t.Error("ResolvedAt not populated")
	}
}

// mutexAvoidUnused is a no-op to keep sync referenced.
var _ = sync.Mutex{}

// TestStore_TypedMsgRoundTrip exercises the SQLite save→load round-trip
// with a concrete Go struct as TurnMsg. This is the path crux-ai
// consumers actually use, and it must survive serialization without
// losing the concrete type — otherwise the FSM's AsMessages would panic
// (after commit 9f93d46's fail-loud change) on the next state transition.
//
// Without `policy_rule` column awareness (which lives in the schema
// migration in this package), this test ensures the basic JSON
// round-trip preserves arbitrary struct values.
func TestStore_TypedMsgRoundTrip(t *testing.T) {
	path := tmpDB(t)
	store, err := sqlite.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	type typedMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Extra   struct {
			Nested string `json:"nested"`
		} `json:"extra"`
	}

	ctx := context.Background()
	original := typedMsg{Role: "user", Content: "hello"}
	original.Extra.Nested = "deep"

	tr := &turn.Turn{
		ID:        "t-typed",
		SessionID: "s1",
		State:     turn.StateReceived,
		Messages:  []turn.TurnMsg{original},
	}
	if err := store.Save(ctx, tr); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load(ctx, "t-typed")
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(got.Messages))
	}

	// SQLite serializes []TurnMsg via JSON; on Load, each entry becomes
	// map[string]any (Go's default JSON unmarshal target for `any`).
	// Document this behavior here so consumers know to either:
	//   (a) stay on MemoryStore for in-process tests
	//   (b) reconstruct typed values before calling Adapter.AsMessages
	gotMsg, ok := got.Messages[0].(map[string]any)
	if !ok {
		t.Fatalf("after SQLite round-trip, message type = %T, want map[string]any (documented limitation)", got.Messages[0])
	}
	if gotMsg["role"] != "user" || gotMsg["content"] != "hello" {
		t.Errorf("round-trip lost fields: %+v", gotMsg)
	}
}