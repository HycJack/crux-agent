// Package faux provides a mock provider for testing.
//
// The standard faux.Provider emits AssistantMessage events directly. This file
// adds an alternative: FauxBridgeProvider that emits ProviderEvent streams
// canonicalized via core.CanonicalizeProviderStream.
//
// This demonstrates the two-layer event system: adapter parsers write simple
// atomic deltas (ProviderEvent) and the bridge produces the full
// Start/Delta/End protocol automatically.
package faux

import (
	"context"
	"fmt"
	"strings"
	"time"

	core "github.com/hycjack/crux-ai/core"
)

// BridgeProvider is a variant of Provider that emits ProviderEvent streams
// canonicalized via core.CanonicalizeProviderStream.
//
// Use this to test adapter code that writes ProviderEvent deltas; the bridge
// layer is exercised end-to-end.
type BridgeProvider struct {
	Delay time.Duration
	API   core.KnownAPI
	Prov  core.KnownProvider
	Model string
}

// NewBridge creates a BridgeProvider with the given identification metadata.
func NewBridge(api core.KnownAPI, prov core.KnownProvider, model string) *BridgeProvider {
	return &BridgeProvider{
		API:   api,
		Prov:  prov,
		Model: model,
	}
}

// Stream implements core.APIProvider by producing ProviderEvent deltas and
// bridging them to AssistantMessageEvent.
func (p *BridgeProvider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.AssistantMessageEventStream, error) {
	ps := core.NewProviderEventStream()
	go p.produce(ctx, ps, llmCtx)
	return core.CanonicalizeProviderStream(ps, p.API, p.Prov, p.Model), nil
}

// StreamSimple implements core.APIProvider.
func (p *BridgeProvider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.AssistantMessageEventStream, error) {
	return p.Stream(ctx, model, llmCtx, opts.StreamOptions)
}

func (p *BridgeProvider) produce(ctx context.Context, ps *core.ProviderEventStream, llmCtx core.Context) {
	defer func() {
		if r := recover(); r != nil {
			ps.Error(fmt.Errorf("faux-bridge: panic: %v", r))
		}
	}()

	response := generateResponse(llmCtx.Messages)
	words := strings.Split(response, " ")

	ps.Push(core.ProviderResponseStart{
		Type: "response_start", Model: p.Model, Timestamp: time.Now(),
	})

	for _, word := range words {
		if ctx.Err() != nil {
			ps.Error(ctx.Err())
			return
		}
		if p.Delay > 0 {
			time.Sleep(p.Delay)
		}
		if word != "" {
			ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: word + " "})
		}
	}

	final := core.AssistantMessage{
		Role:       "assistant",
		API:        p.API,
		Provider:   p.Prov,
		Model:      p.Model,
		Content:    []core.ContentBlock{core.TextContent{Type: "text", Text: response}},
		StopReason: core.StopStop,
		Timestamp:  time.Now(),
	}
	ps.Push(core.ProviderResponseEnd{
		Type: "response_end", Message: final, FinishReason: "stop",
	})
	ps.End(core.ProviderEventStreamResult{})
}
