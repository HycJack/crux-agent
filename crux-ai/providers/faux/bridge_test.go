package faux

import (
	"context"
	"testing"

	core "github.com/hycjack/crux-ai/core"
)

func TestFauxBridgeProvider_Stream_Basic(t *testing.T) {
	ctx := context.Background()
	p := NewBridge("test-api", "test-prov", "test-model")
	model := core.Model{ID: "test-model", API: "test-api", Provider: "test-prov"}

	stream, err := p.Stream(ctx, model, core.Context{
		Messages: []core.Message{
			core.UserMessage{Role: "user", Content: "hello"},
		},
	}, core.StreamOptions{})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var events []core.AssistantMessageEvent
	_, err = stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		events = append(events, evt)
		return nil
	})
	if err != nil {
		t.Fatalf("ForEach failed: %v", err)
	}

	// Should produce: start, text_start, text_delta(xN), text_end, done
	if len(events) < 5 {
		t.Fatalf("expected >=5 events, got %d", len(events))
	}

	// Verify structure
	if _, ok := events[0].(core.EventStart); !ok {
		t.Errorf("expected EventStart, got %T", events[0])
	}
	if _, ok := events[1].(core.EventTextStart); !ok {
		t.Errorf("expected EventTextStart, got %T", events[1])
	}

	// Check last event is done
	last := events[len(events)-1]
	done, ok := last.(core.EventDone)
	if !ok {
		t.Errorf("expected EventDone, got %T", last)
		return
	}
	if done.Reason != core.StopStop {
		t.Errorf("expected StopStop, got %v", done.Reason)
	}
	if done.Message.API != "test-api" {
		t.Errorf("expected API=test-api, got %s", done.Message.API)
	}
}

func TestFauxBridgeProvider_StreamSimple(t *testing.T) {
	ctx := context.Background()
	p := NewBridge("test-api", "test-prov", "test-model")
	model := core.Model{ID: "test-model", API: "test-api", Provider: "test-prov"}

	stream, err := p.StreamSimple(ctx, model, core.Context{
		Messages: []core.Message{
			core.UserMessage{Role: "user", Content: "world"},
		},
	}, core.SimpleStreamOptions{})
	if err != nil {
		t.Fatalf("StreamSimple failed: %v", err)
	}

	_, err = stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		return nil
	})
	if err != nil {
		t.Fatalf("ForEach failed: %v", err)
	}
}

func TestFauxBridgeProvider_ResponseContent(t *testing.T) {
	ctx := context.Background()
	p := NewBridge("test-api", "test-prov", "test-model")
	model := core.Model{ID: "test-model", API: "test-api", Provider: "test-prov"}

	stream, err := p.Stream(ctx, model, core.Context{
		Messages: []core.Message{
			core.UserMessage{Role: "user", Content: "hello world"},
		},
	}, core.StreamOptions{})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var text string
	_, err = stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		if d, ok := evt.(core.EventTextDelta); ok {
			text += d.Delta
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ForEach failed: %v", err)
	}

	expected := "Faux response to: hello world "
	if text != expected {
		t.Errorf("expected %q, got %q", expected, text)
	}
}

func TestFauxBridgeProvider_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := NewBridge("test-api", "test-prov", "test-model")
	model := core.Model{ID: "test-model", API: "test-api", Provider: "test-prov"}

	stream, err := p.Stream(ctx, model, core.Context{
		Messages: []core.Message{
			core.UserMessage{Role: "user", Content: "cancel"},
		},
	}, core.StreamOptions{})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	cancel()

	_, err = stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestFauxBridgeProvider_EmptyMessages(t *testing.T) {
	ctx := context.Background()
	p := NewBridge("test-api", "test-prov", "test-model")
	model := core.Model{ID: "test-model", API: "test-api", Provider: "test-prov"}

	stream, err := p.Stream(ctx, model, core.Context{}, core.StreamOptions{})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events, err := collectFauxEvents(ctx, stream)
	if err != nil {
		t.Fatalf("ForEach failed: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected >=2 events, got %d", len(events))
	}
}

func TestFauxBridgeProvider_RegisterAndCall(t *testing.T) {
	// Test registering BridgeProvider via the core registry
	core.ClearProviders()
	defer core.ClearProviders()

	p := NewBridge("bridge-api", "bridge-prov", "bridge-model")
	core.RegisterProvider("bridge-api", p, "test-bridge")

	model := core.Model{ID: "bridge-model", API: "bridge-api", Provider: "bridge-prov"}
	ctx := context.Background()

	provider, err := core.GetProvider("bridge-api")
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}

	stream, err := provider.Stream(ctx, model, core.Context{
		Messages: []core.Message{
			core.UserMessage{Role: "user", Content: "test"},
		},
	}, core.StreamOptions{})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	_, err = stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		return nil
	})
	if err != nil {
		t.Fatalf("ForEach: %v", err)
	}
}

func collectFauxEvents(ctx context.Context, stream *core.AssistantMessageEventStream) ([]core.AssistantMessageEvent, error) {
	var events []core.AssistantMessageEvent
	_, err := stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
		events = append(events, evt)
		return nil
	})
	return events, err
}
