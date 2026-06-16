package context

import (
	"context"
	"testing"

	"github.com/hycjack/crux-ai/core"
	"crux-agent-runtime/session"
)

func TestDefaultTokenCounter(t *testing.T) {
	msgs := []core.Message{
		core.UserMessage{Role: core.MessageRoleUser, Content: "Hello, how are you?"},
		core.AssistantMessage{
			Role: core.MessageRoleAssistant,
			Content: []core.ContentBlock{
				core.TextContent{Type: "text", Text: "I'm doing well, thank you!"},
			},
		},
	}

	tokens := DefaultTokenCounter("You are a helpful assistant.", msgs, nil)
	if tokens <= 0 {
		t.Errorf("expected positive token count, got %d", tokens)
	}
}

func TestSlideWindow(t *testing.T) {
	compactor := NewSlideWindow(5)

	msgs := make([]core.Message, 10)
	for i := 0; i < 10; i++ {
		msgs[i] = core.UserMessage{
			Role:    core.MessageRoleUser,
			Content: "message",
		}
	}

	result, changed, err := compactor.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if !changed {
		t.Error("expected compaction to change messages")
	}
	if len(result) != 5 {
		t.Errorf("expected 5 messages, got %d", len(result))
	}
}

func TestSlideWindow_NoCompactionNeeded(t *testing.T) {
	compactor := NewSlideWindow(10)

	msgs := []core.Message{
		core.UserMessage{Role: core.MessageRoleUser, Content: "hello"},
	}

	result, changed, err := compactor.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if changed {
		t.Error("expected no compaction")
	}
	if len(result) != 1 {
		t.Errorf("expected 1 message, got %d", len(result))
	}
}

func TestLLMSummarize(t *testing.T) {
	compactor := NewLLMSummarize()
	compactor.MinTrigger = 5
	compactor.KeepLast = 2

	// Use a simple summarizer.
	compactor.Summarize = func(ctx context.Context, dropped []core.Message) (string, error) {
		return "Summary of conversation", nil
	}

	msgs := make([]core.Message, 10)
	for i := 0; i < 10; i++ {
		msgs[i] = core.UserMessage{
			Role:    core.MessageRoleUser,
			Content: "message",
		}
	}

	result, changed, err := compactor.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if !changed {
		t.Error("expected compaction to change messages")
	}
	// head (0) + summary (1) + tail (2) = 3
	if len(result) != 3 {
		t.Errorf("expected 3 messages, got %d", len(result))
	}
	if compactor.Calls.Load() != 1 {
		t.Errorf("expected 1 summarize call, got %d", compactor.Calls.Load())
	}
}

func TestLLMSummarize_BelowMinTrigger(t *testing.T) {
	compactor := NewLLMSummarize()
	compactor.MinTrigger = 20

	msgs := []core.Message{
		core.UserMessage{Role: core.MessageRoleUser, Content: "hello"},
	}

	_, changed, err := compactor.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if changed {
		t.Error("expected no compaction below MinTrigger")
	}
}

func TestChainedCompactor(t *testing.T) {
	// First compactor: does nothing
	c1 := NewSlideWindow(100)
	// Second compactor: compacts
	c2 := NewSlideWindow(5)

	chained := &ChainedCompactor{Compactors: []Compactor{c1, c2}}

	msgs := make([]core.Message, 10)
	for i := 0; i < 10; i++ {
		msgs[i] = core.UserMessage{
			Role:    core.MessageRoleUser,
			Content: "message",
		}
	}

	result, changed, err := chained.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if !changed {
		t.Error("expected compaction to change messages")
	}
	if len(result) != 5 {
		t.Errorf("expected 5 messages, got %d", len(result))
	}
}

func TestContextWindowCompactor(t *testing.T) {
	config := ContextWindowConfig{
		MaxTokens:     100,
		ReserveTokens: 20,
		MinMessages:   2,
	}

	inner := NewSlideWindow(5)
	compactor := NewContextWindowCompactor(config, inner)

	// Create messages that exceed the window.
	msgs := make([]core.Message, 20)
	for i := 0; i < 20; i++ {
		msgs[i] = core.UserMessage{
			Role:    core.MessageRoleUser,
			Content: "This is a test message with enough content to consume tokens.",
		}
	}

	result, changed, err := compactor.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if !changed {
		t.Error("expected compaction to change messages")
	}
	if len(result) >= len(msgs) {
		t.Errorf("expected fewer messages, got %d vs %d", len(result), len(msgs))
	}
}

func TestManager(t *testing.T) {
	config := DefaultContextWindowConfig()
	config.MaxTokens = 1000
	config.ReserveTokens = 200

	mgr := NewManager(config)

	// Add messages.
	mgr.AddMessage(core.UserMessage{
		Role:    core.MessageRoleUser,
		Content: "Hello",
	})
	mgr.AddMessage(core.AssistantMessage{
		Role: core.MessageRoleAssistant,
		Content: []core.ContentBlock{
			core.TextContent{Type: "text", Text: "Hi there!"},
		},
	})

	msgs := mgr.GetMessages()
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}

	stats := mgr.GetStats()
	if stats.MessageCount != 2 {
		t.Errorf("expected 2 messages in stats, got %d", stats.MessageCount)
	}
}

func TestManager_LoadFromSession(t *testing.T) {
	storage := session.NewMemoryStorage()
	sess, _ := session.NewSession(storage)

	sess.Append(session.NewSystemPromptEntry("You are helpful."))
	sess.Append(session.NewUserMessageEntry("Hello"))
	sess.Append(session.NewAssistantMessageEntry(core.AssistantMessage{
		Role:    core.MessageRoleAssistant,
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Hi!"}},
	}))

	config := DefaultContextWindowConfig()
	mgr := NewManager(config)
	mgr.LoadFromSession(sess)

	ctx := mgr.GetContext()
	if ctx.SystemPrompt != "You are helpful." {
		t.Errorf("expected system prompt, got %q", ctx.SystemPrompt)
	}
	if len(ctx.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(ctx.Messages))
	}
}

func TestManager_CompactIfNeeded(t *testing.T) {
	config := ContextWindowConfig{
		MaxTokens:     100,
		ReserveTokens: 20,
		MinMessages:   2,
	}

	mgr := NewManager(config)
	mgr.SetCompactor(NewSlideWindow(10))

	// Add enough messages to trigger compaction.
	for i := 0; i < 20; i++ {
		mgr.AddMessage(core.UserMessage{
			Role:    core.MessageRoleUser,
			Content: "This is a test message with enough content to consume tokens in the context window.",
		})
	}

	stats := mgr.GetStats()
	if stats.Compactions == 0 {
		t.Error("expected at least one compaction")
	}
}

func TestManager_IsNearLimit(t *testing.T) {
	config := ContextWindowConfig{
		MaxTokens:     100,
		ReserveTokens: 20,
	}

	mgr := NewManager(config)

	// Should not be near limit initially.
	if mgr.IsNearLimit(0.8) {
		t.Error("should not be near limit initially")
	}
}

func TestManager_Reset(t *testing.T) {
	config := DefaultContextWindowConfig()
	mgr := NewManager(config)

	mgr.AddMessage(core.UserMessage{
		Role:    core.MessageRoleUser,
		Content: "Hello",
	})

	mgr.Reset()

	msgs := mgr.GetMessages()
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after reset, got %d", len(msgs))
	}
}

func TestManager_GetStats(t *testing.T) {
	config := ContextWindowConfig{
		MaxTokens:     10000,
		ReserveTokens: 2000,
	}

	mgr := NewManager(config)
	mgr.AddMessage(core.UserMessage{
		Role:    core.MessageRoleUser,
		Content: "Hello",
	})

	stats := mgr.GetStats()
	if stats.MaxTokens != 10000 {
		t.Errorf("expected MaxTokens=10000, got %d", stats.MaxTokens)
	}
	if stats.AvailableTokens != 8000 {
		t.Errorf("expected AvailableTokens=8000, got %d", stats.AvailableTokens)
	}
	if stats.UsagePercent <= 0 {
		t.Errorf("expected positive usage, got %f", stats.UsagePercent)
	}
}

func TestNeedsCompaction(t *testing.T) {
	config := ContextWindowConfig{
		MaxTokens:     100,
		ReserveTokens: 20,
	}

	msgs := []core.Message{
		core.UserMessage{Role: core.MessageRoleUser, Content: "short"},
	}

	// Short message shouldn't need compaction.
	if NeedsCompaction(nil, "", msgs, nil, config) {
		t.Error("short message should not need compaction")
	}

	// Long message should need compaction.
	longMsgs := make([]core.Message, 100)
	for i := 0; i < 100; i++ {
		longMsgs[i] = core.UserMessage{
			Role:    core.MessageRoleUser,
			Content: "This is a long message that will consume many tokens in the context window.",
		}
	}
	if !NeedsCompaction(nil, "", longMsgs, nil, config) {
		t.Error("long messages should need compaction")
	}
}

func TestEstimateKeepMessages(t *testing.T) {
	msgs := make([]core.Message, 20)
	for i := 0; i < 20; i++ {
		msgs[i] = core.UserMessage{
			Role:    core.MessageRoleUser,
			Content: "test message",
		}
	}

	keep := estimateKeepMessages(nil, "", msgs, nil, 100, 2)
	if keep < 2 {
		t.Errorf("expected at least 2 messages, got %d", keep)
	}
}
