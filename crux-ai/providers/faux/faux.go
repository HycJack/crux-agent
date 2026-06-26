// Package faux provides a mock provider for testing.
package faux

import (
	"context"
	"fmt"
	"strings"
	"time"

	core "crux-ai/core"
)

// Provider is a mock provider that generates deterministic responses.
type Provider struct {
	Delay time.Duration
}

func New() *Provider { return &Provider{} }

func (p *Provider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.AssistantMessageEventStream, error) {
	return p.stream(ctx, model, llmCtx, opts)
}

func (p *Provider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.AssistantMessageEventStream, error) {
	return p.stream(ctx, model, llmCtx, opts.StreamOptions)
}

func (p *Provider) stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.AssistantMessageEventStream, error) {
	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				stream.Error(fmt.Errorf("faux: panic: %v", r))
			}
		}()

		stream.Push(core.EventStart{Type: "start", API: model.API, Provider: model.Provider, Model: model.ID, Timestamp: time.Now()})

		response := generateResponse(llmCtx.Messages)
		stream.Push(core.EventTextStart{Type: "text_start"})

		words := strings.Split(response, " ")
		for _, word := range words {
			if ctx.Err() != nil {
				stream.Error(ctx.Err())
				return
			}
			if p.Delay > 0 {
				time.Sleep(p.Delay)
			}
			stream.Push(core.EventTextDelta{Type: "text_delta", Delta: word + " "})
		}

		stream.Push(core.EventTextEnd{Type: "text_end"})

		msg := core.AssistantMessage{
			Role: "assistant", API: model.API, Provider: model.Provider, Model: model.ID,
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: response}},
			StopReason: core.StopStop, Timestamp: time.Now(),
		}
		stream.Push(core.EventDone{Type: "done", Message: msg})
		stream.End(msg)
	}()

	return stream, nil
}

func generateResponse(msgs []core.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if um, ok := msgs[i].(core.UserMessage); ok {
			if s, ok := um.Content.(string); ok {
				return "Faux response to: " + s
			}
		}
	}
	return "Faux response (no user message found)"
}
