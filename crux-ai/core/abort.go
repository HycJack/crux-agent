package core

import (
	"context"
	"sync"
)

// =============================================================================
// Multi-source abort signal composition.
//
// Reference: pi-mono packages/ai/src/utils/abort-signals.ts
//
// Go has no AbortSignal type, so we compose cancel funcs into a single
// context that fires when *any* source cancels. Callers receive a
// CombinedAbort whose Cancel() is the trigger AND whose context.Context
// is updated on any source fire.
// =============================================================================

// CombinedAbort merges zero-or-more cancellation sources into a single
// derived context + cancel.
//
// Cleanup is safe to call multiple times.
type CombinedAbort struct {
	Ctx    context.Context
	Cancel context.CancelFunc
}

// CombineAbortSignals returns a CombinedAbort that fires when any of the
// provided parent contexts is cancelled. A nil/empty parent list produces
// a no-op cancel.
//
// The returned CombinedAbort must be Released() when the caller is done;
// this detaches listeners from parent contexts.
func CombineAbortSignals(parents ...context.Context) *CombinedAbort {
	parents = filterNonNil(parents)
	if len(parents) == 0 {
		ctx, cancel := context.WithCancel(context.Background())
		return &CombinedAbort{Ctx: ctx, Cancel: cancel}
	}
	if len(parents) == 1 {
		ctx, cancel := context.WithCancel(parents[0])
		return &CombinedAbort{Ctx: ctx, Cancel: cancel}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	fire := func() { once.Do(cancel) }

	for _, p := range parents {
		if p == nil {
			continue
		}
		if p.Err() != nil {
			fire()
			continue
		}
		go watchCancel(p, fire)
	}
	return &CombinedAbort{Ctx: ctx, Cancel: fire}
}

// watchCancel blocks until the parent context is done, then fires cb.
// Each parent gets its own goroutine; the goroutine exits when the
// parent is done (either by cancel or by being detached in Release).
func watchCancel(parent context.Context, cb func()) {
	<-parent.Done()
	cb()
}

// filterNonNil drops nil entries from the parent list.
func filterNonNil(parents []context.Context) []context.Context {
	out := make([]context.Context, 0, len(parents))
	for _, p := range parents {
		if p != nil {
			out = append(out, p)
		}
	}
	return out
}

// SignalToContext converts a `<-chan struct{}` cancellation signal into a
// cancellable context. Returns context.Background() when sig is nil, so the
// caller can always WithCancel on the result. Calling cancel stops the
// internal watcher goroutine.
//
// Use this instead of the standard "context.WithCancel + goroutine + select"
// boilerplate when bridging a callback-style signal into a context-aware
// HTTP request.
//
// Source: pi-mono packages/ai/src/utils/abort-signals.ts pattern (transposed).
// || 把 <-chan struct{} 信号转为可取消的 context。
func SignalToContext(sig <-chan struct{}) context.Context {
	if sig == nil {
		return context.Background()
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx
}
