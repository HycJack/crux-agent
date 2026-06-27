// Package llm provides the legacy import path for the agent runtime.
//
// Deprecated: Use package ai instead. This package exists solely so that
// crux-agent-runtime can continue to import "github.com/hycjack/crux-ai/llm"
// without changes.
package llm

import (
	"context"

	"github.com/hycjack/crux-ai/ai"
	"github.com/hycjack/crux-ai/core"
)

// StreamSimpleWithContext starts a streaming completion with full context.
//
// Deprecated: Use ai.StreamSimpleWithContext.
func StreamSimpleWithContext(ctx context.Context, model core.Model, llmCtx core.Context, opts ...core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	return ai.StreamSimpleWithContext(ctx, model, llmCtx, opts...)
}

// StreamSimple starts a streaming completion with simplified reasoning options.
//
// Deprecated: Use ai.StreamSimpleWithContext. This variant drops SystemPrompt,
// Tools, and other Context fields.
func StreamSimple(ctx context.Context, model core.Model, msgs []core.Message, opts ...core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	return ai.StreamSimpleWithContext(ctx, model, core.Context{Messages: msgs}, opts...)
}

// CompleteSimple calls StreamSimpleWithContext and waits for the final result.
//
// Deprecated: Use ai.CompleteSimpleWithContext.
func CompleteSimple(ctx context.Context, model core.Model, msgs []core.Message, opts ...core.SimpleStreamOptions) (core.AssistantMessage, error) {
	return ai.CompleteSimpleWithContext(ctx, model, core.Context{Messages: msgs}, opts...)
}

// CompleteSimpleWithContext calls StreamSimpleWithContext and waits.
func CompleteSimpleWithContext(ctx context.Context, model core.Model, llmCtx core.Context, opts ...core.SimpleStreamOptions) (core.AssistantMessage, error) {
	return ai.CompleteSimpleWithContext(ctx, model, llmCtx, opts...)
}

// StreamWithContext starts a streaming completion with full context.
//
// Deprecated: Use ai.StreamWithContext.
func StreamWithContext(ctx context.Context, model core.Model, llmCtx core.Context, opts ...core.StreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	return ai.StreamWithContext(ctx, model, llmCtx, opts...)
}

// Stream starts a streaming completion.
//
// Deprecated: Use ai.StreamWithContext.
func Stream(ctx context.Context, model core.Model, msgs []core.Message, opts ...core.StreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	return ai.StreamWithContext(ctx, model, core.Context{Messages: msgs}, opts...)
}

// CompleteWithContext calls StreamWithContext and waits.
//
// Deprecated: Use ai.CompleteWithContext.
func CompleteWithContext(ctx context.Context, model core.Model, llmCtx core.Context, opts ...core.StreamOptions) (core.AssistantMessage, error) {
	return ai.CompleteWithContext(ctx, model, llmCtx, opts...)
}

// Complete calls StreamWithContext and waits.
//
// Deprecated: Use ai.CompleteWithContext.
func Complete(ctx context.Context, model core.Model, msgs []core.Message, opts ...core.StreamOptions) (core.AssistantMessage, error) {
	return ai.CompleteWithContext(ctx, model, core.Context{Messages: msgs}, opts...)
}
