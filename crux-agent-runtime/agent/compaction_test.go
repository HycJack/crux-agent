package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	ctxpkg "crux-agent-runtime/context"

	core "github.com/hycjack/crux-ai/core"
)

// recordingCompactor is a test compactor that captures the messages it
// was called with and returns a fixed replacement.
type recordingCompactor struct {
	calls     int
	lastMsgs  []core.Message
	replaced  []core.Message
	returnErr error
}

func (r *recordingCompactor) Name() string { return "recording" }

func (r *recordingCompactor) Compact(_ context.Context, msgs []core.Message) ([]core.Message, bool, error) {
	r.calls++
	r.lastMsgs = msgs
	if r.returnErr != nil {
		return msgs, false, r.returnErr
	}
	if r.replaced == nil {
		// Default: return half the messages.
		half := len(msgs) / 2
		if half == 0 {
			return msgs, false, nil
		}
		return msgs[len(msgs)-half:], true, nil
	}
	return r.replaced, true, nil
}

// constantCounter returns a fixed token count.
func constantCounter(n int) ctxpkg.TokenCounter {
	return func(_ string, _ []core.Message, _ []core.Tool) int { return n }
}

// lenBasedCounter counts ~10 tokens per message plus a base — useful for
// tests that need to verify token counts change after compaction.
func lenBasedCounter(base int) ctxpkg.TokenCounter {
	return func(_ string, msgs []core.Message, _ []core.Tool) int {
		return base + len(msgs)*10
	}
}

// makeUserMsg constructs a UserMessage for testing.
func makeUserMsg(content string) core.Message {
	return core.UserMessage{
		Role:    core.MessageRoleUser,
		Content: content,
	}
}

func TestMaybeCompactPreCall_NoCompactor(t *testing.T) {
	msgs := []core.Message{makeUserMsg("hello")}
	got := maybeCompactPreCall(context.Background(), AgentLoopConfig{}, msgs)
	if len(got) != len(msgs) {
		t.Errorf("expected no compaction, got %d msgs", len(got))
	}
}

func TestMaybeCompactPreCall_UnderBudget(t *testing.T) {
	comp := &recordingCompactor{}
	cfg := AgentLoopConfig{
		SystemPrompt: "test",
		Compaction: CompactionConfig{
			Compactor:     comp,
			MaxTokens:     100,
			ReserveTokens: 0,
			TokenCounter:  constantCounter(50), // under budget
		},
	}
	msgs := []core.Message{makeUserMsg("hi")}
	got := maybeCompactPreCall(context.Background(), cfg, msgs)
	if len(got) != 1 {
		t.Errorf("expected 1 msg, got %d", len(got))
	}
	if comp.calls != 0 {
		t.Errorf("compactor should not be called under budget, got %d calls", comp.calls)
	}
}

func TestMaybeCompactPreCall_OverBudget(t *testing.T) {
	comp := &recordingCompactor{
		replaced: []core.Message{makeUserMsg("summary")},
	}
	callbackCalled := false
	cfg := AgentLoopConfig{
		SystemPrompt: "test",
		Compaction: CompactionConfig{
			Compactor:     comp,
			MaxTokens:     100,
			ReserveTokens: 0,
			TokenCounter:  lenBasedCounter(200), // 5 msgs = 250 tokens, 1 msg = 210 tokens
			OnCompact: func(prev, next, prevMsgs, nextMsgs int) {
				callbackCalled = true
				if prev != 250 {
					t.Errorf("OnCompact prev=%d, want 250", prev)
				}
				if next != 210 {
					t.Errorf("OnCompact next=%d, want 210", next)
				}
				if prevMsgs != 5 || nextMsgs != 1 {
					t.Errorf("OnCompact got prevMsgs=%d nextMsgs=%d", prevMsgs, nextMsgs)
				}
			},
		},
	}
	msgs := []core.Message{
		makeUserMsg("a"), makeUserMsg("b"), makeUserMsg("c"),
		makeUserMsg("d"), makeUserMsg("e"),
	}
	got := maybeCompactPreCall(context.Background(), cfg, msgs)
	if len(got) != 1 || got[0].(core.UserMessage).Content != "summary" {
		t.Errorf("expected compacted slice with summary, got %+v", got)
	}
	if comp.calls != 1 {
		t.Errorf("expected 1 compactor call, got %d", comp.calls)
	}
	if !callbackCalled {
		t.Error("OnCompact callback was not invoked")
	}
}

func TestMaybeCompactPreCall_CompactorNoOp(t *testing.T) {
	// Compactor returns changed=false (no work done). Messages should pass through.
	comp := &recordingCompactor{
		replaced: []core.Message{makeUserMsg("anything")},
	}
	// Patch returnErr to trigger changed=false path:
	comp.replaced = nil // recordingCompactor returns changed=false if replaced is nil and half==0
	msgs := []core.Message{makeUserMsg("only")}
	cfg := AgentLoopConfig{
		SystemPrompt: "test",
		Compaction: CompactionConfig{
			Compactor:     comp,
			MaxTokens:     100,
			ReserveTokens: 0,
			TokenCounter:  constantCounter(200),
		},
	}
	got := maybeCompactPreCall(context.Background(), cfg, msgs)
	if len(got) != len(msgs) {
		t.Errorf("compactor no-op should not change messages, got %d", len(got))
	}
}

func TestToCoreTools(t *testing.T) {
	tools := []AgentTool{
		{Name: "echo", Description: "echo tool"},
		{Name: "calc", Description: "calc tool"},
	}
	got := toCoreTools(tools)
	if len(got) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(got))
	}
	if got[0].Name != "echo" || got[1].Name != "calc" {
		t.Errorf("tool names wrong: %+v", got)
	}
}

func TestToCoreTools_Empty(t *testing.T) {
	got := toCoreTools(nil)
	if got != nil {
		t.Errorf("expected nil for empty input, got %+v", got)
	}
}

func TestIsContextOverflow_ProviderError(t *testing.T) {
	// OverflowError from core package should be detected.
	err := &core.OverflowError{Provider: core.ProviderXiaomi, Message: "prompt too long"}
	if !core.IsContextOverflow(err) {
		t.Error("expected overflow detection for *OverflowError")
	}
}

func TestIsContextOverflow_StringMatch(t *testing.T) {
	if !core.IsContextOverflow(errors.New("context_length_exceeded: too long")) {
		t.Error("expected string-based detection")
	}
	if core.IsContextOverflow(errors.New("invalid api key")) {
		t.Error("non-overflow error should not match")
	}
}

// TestCompactionEndToEnd_OverflowRetry exercises the full retry path:
// the LLM "rejects" the first call with an overflow error, the compactor
// runs, and the second call succeeds.
func TestCompactionEndToEnd_OverflowRetry(t *testing.T) {
	var (
		firstCallSeen  bool
		secondCallSeen bool
		streamFnCalls  int
	)
	comp := &recordingCompactor{
		replaced: []core.Message{
			core.UserMessage{Role: core.MessageRoleUser, Content: "summary of old"},
			makeUserMsg("recent"),
		},
	}

	// Mock StreamFn that fails with overflow on first call, succeeds on second.
	streamFn := func(ctx context.Context, model core.Model, c core.Context, opts core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
		streamFnCalls++
		s := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()
		go func() {
			if streamFnCalls == 1 {
				firstCallSeen = true
				s.Error(&core.OverflowError{Provider: core.ProviderXiaomi, Message: "context too long"})
				return
			}
			secondCallSeen = true
			s.Push(core.EventTextDelta{Delta: "hello"})
			s.End(core.AssistantMessage{
				Role:    core.MessageRoleAssistant,
				Content: []core.ContentBlock{core.TextContent{Text: "hello"}},
			})
		}()
		return s, nil
	}

	// Use a high MaxTokens so pre-call compaction doesn't fire — we want to
	// test ONLY the overflow-retry path.
	cfg := AgentLoopConfig{
		Model:        core.Model{Provider: core.ProviderXiaomi, ID: "test"},
		SystemPrompt: "sys",
		Compaction: CompactionConfig{
			Compactor:       comp,
			MaxTokens:       1000000,
			ReserveTokens:   0,
			TokenCounter:    constantCounter(10),
			OverflowRetries: 2,
		},
		StreamFn: streamFn,
	}
	msgs := []core.Message{
		makeUserMsg("a"), makeUserMsg("b"), makeUserMsg("c"),
	}

	stream := AgentLoop(context.Background(), msgs, cfg)

	// Drain events into the stream.
	_, _ = stream.Result()

	if !firstCallSeen {
		t.Error("expected first LLM call to be made")
	}
	if !secondCallSeen {
		t.Error("expected retry after overflow")
	}
	if streamFnCalls != 2 {
		t.Errorf("expected 2 stream calls, got %d", streamFnCalls)
	}
	if comp.calls != 1 {
		t.Errorf("expected 1 compaction, got %d", comp.calls)
	}
}

func TestCompactionEndToEnd_NoOverflowPassesThrough(t *testing.T) {
	var streamFnCalls int
	streamFn := func(ctx context.Context, model core.Model, c core.Context, opts core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
		streamFnCalls++
		s := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()
		go func() {
			s.Push(core.EventTextDelta{Delta: "ok"})
			s.End(core.AssistantMessage{
				Role:    core.MessageRoleAssistant,
				Content: []core.ContentBlock{core.TextContent{Text: "ok"}},
			})
		}()
		return s, nil
	}
	comp := &recordingCompactor{}
	cfg := AgentLoopConfig{
		Model:        core.Model{Provider: core.ProviderXiaomi, ID: "test"},
		SystemPrompt: "sys",
		Compaction: CompactionConfig{
			Compactor:       comp,
			MaxTokens:       1000000,
			ReserveTokens:   0,
			TokenCounter:    constantCounter(10),
			OverflowRetries: 2,
		},
		StreamFn: streamFn,
	}
	stream := AgentLoop(context.Background(), []core.Message{makeUserMsg("hi")}, cfg)
	_, _ = stream.Result()
	if streamFnCalls != 1 {
		t.Errorf("expected 1 call, got %d", streamFnCalls)
	}
	if comp.calls != 0 {
		t.Errorf("compactor should not run when call succeeds, got %d", comp.calls)
	}
}

func TestCompactionEndToEnd_PreCallTriggers(t *testing.T) {
	// Pre-call compaction should fire when token count exceeds budget.
	// After compaction, the LLM call succeeds.
	comp := &recordingCompactor{
		replaced: []core.Message{makeUserMsg("summary")},
	}
	streamFn := func(ctx context.Context, model core.Model, c core.Context, opts core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
		s := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()
		go func() {
			s.End(core.AssistantMessage{
				Role:    core.MessageRoleAssistant,
				Content: []core.ContentBlock{core.TextContent{Text: "ok"}},
			})
		}()
		return s, nil
	}

	cfg := AgentLoopConfig{
		Model:        core.Model{Provider: core.ProviderXiaomi, ID: "test"},
		SystemPrompt: "sys",
		Compaction: CompactionConfig{
			Compactor:     comp,
			MaxTokens:     50,
			ReserveTokens: 0,
			TokenCounter:  constantCounter(100), // over budget
		},
		StreamFn: streamFn,
	}
	msgs := []core.Message{
		makeUserMsg("a"), makeUserMsg("b"), makeUserMsg("c"),
	}
	stream := AgentLoop(context.Background(), msgs, cfg)
	_, _ = stream.Result()
	if comp.calls != 1 {
		t.Errorf("expected 1 pre-call compaction, got %d", comp.calls)
	}
}

// verifyRecordingCompactorShortCircuit makes sure recordingCompactor
// returns changed=false when the slice is empty.
func TestRecordingCompactor_NoOpOnEmpty(t *testing.T) {
	comp := &recordingCompactor{}
	out, changed, err := comp.Compact(context.Background(), nil)
	if err != nil || changed {
		t.Errorf("expected no-op on empty input, got changed=%v err=%v", changed, err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty out, got %v", out)
	}
}

func TestRecordingCompactor_PropagatesError(t *testing.T) {
	want := fmt.Errorf("compactor failed")
	comp := &recordingCompactor{returnErr: want}
	_, _, err := comp.Compact(context.Background(), []core.Message{makeUserMsg("x")})
	if err != want {
		t.Errorf("expected %v, got %v", want, err)
	}
}
