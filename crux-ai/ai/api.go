// Package ai provides the AI calling layer: public streaming/completion API,
// model management, and environment variable resolution.
package ai

import (
	"context"

	"github.com/hycjack/crux-ai/core"
)

// Stream starts a streaming completion request.
//
// Deprecated: Stream only passes Messages; SystemPrompt, Tools, and other
// Context fields are dropped. Use StreamWithContext to send the full context.
func Stream(ctx context.Context, model core.Model, msgs []core.Message, opts ...core.StreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	return StreamWithContext(ctx, model, core.Context{Messages: msgs}, opts...)
}

// StreamWithContext starts a streaming completion request with the full
// context, including SystemPrompt, Tools, and Messages.
func StreamWithContext(ctx context.Context, model core.Model, llmCtx core.Context, opts ...core.StreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	provider, err := core.GetProvider(model.API)
	if err != nil {
		return nil, err
	}

	var opt core.StreamOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	return provider.Stream(ctx, model, llmCtx, opt)
}

// Complete calls StreamWithContext and waits for the final result.
func Complete(ctx context.Context, model core.Model, msgs []core.Message, opts ...core.StreamOptions) (core.AssistantMessage, error) {
	return CompleteWithContext(ctx, model, core.Context{Messages: msgs}, opts...)
}

// CompleteWithContext calls StreamWithContext and waits for the final result.
func CompleteWithContext(ctx context.Context, model core.Model, llmCtx core.Context, opts ...core.StreamOptions) (core.AssistantMessage, error) {
	s, err := StreamWithContext(ctx, model, llmCtx, opts...)
	if err != nil {
		return core.AssistantMessage{}, err
	}
	return s.Result()
}

// StreamSimple starts a streaming completion with simplified reasoning options.
//
// Deprecated: StreamSimple only passes Messages; use StreamSimpleWithContext.
func StreamSimple(ctx context.Context, model core.Model, msgs []core.Message, opts ...core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	return StreamSimpleWithContext(ctx, model, core.Context{Messages: msgs}, opts...)
}

// StreamSimpleWithContext starts a streaming completion with full context.
func StreamSimpleWithContext(ctx context.Context, model core.Model, llmCtx core.Context, opts ...core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	provider, err := core.GetProvider(model.API)
	if err != nil {
		return nil, err
	}

	var opt core.SimpleStreamOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	return provider.StreamSimple(ctx, model, llmCtx, opt)
}

// CompleteSimple calls StreamSimpleWithContext and waits for the final result.
func CompleteSimple(ctx context.Context, model core.Model, msgs []core.Message, opts ...core.SimpleStreamOptions) (core.AssistantMessage, error) {
	return CompleteSimpleWithContext(ctx, model, core.Context{Messages: msgs}, opts...)
}

// CompleteSimpleWithContext calls StreamSimpleWithContext and waits.
func CompleteSimpleWithContext(ctx context.Context, model core.Model, llmCtx core.Context, opts ...core.SimpleStreamOptions) (core.AssistantMessage, error) {
	s, err := StreamSimpleWithContext(ctx, model, llmCtx, opts...)
	if err != nil {
		return core.AssistantMessage{}, err
	}
	return s.Result()
}

// GenerateImages generates images using the specified image model.
//
// Deprecated: GenerateImages only passes Messages; use GenerateImagesWithContext.
func GenerateImages(ctx context.Context, model core.ImagesModel, msgs []core.Message, opts ...core.ImageOptions) (core.AssistantImages, error) {
	return GenerateImagesWithContext(ctx, model, core.Context{Messages: msgs}, opts...)
}

// GenerateImagesWithContext generates images using the specified image model
// with full context.
func GenerateImagesWithContext(ctx context.Context, model core.ImagesModel, llmCtx core.Context, opts ...core.ImageOptions) (core.AssistantImages, error) {
	provider, err := core.GetImagesProvider(model.API)
	if err != nil {
		return core.AssistantImages{}, err
	}

	var opt core.ImageOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	result, err := provider.GenerateImages(model, llmCtx, opt)
	if err != nil {
		return core.AssistantImages{}, err
	}
	return *result, nil
}
