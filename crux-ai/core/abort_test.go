package core

import (
	"context"
	"testing"
	"time"
)

func TestCombineAbortSignals_NoParents(t *testing.T) {
	ca := CombineAbortSignals()
	if ca.Ctx.Err() != nil {
		t.Errorf("should not be cancelled yet")
	}
	ca.Cancel()
	if ca.Ctx.Err() == nil {
		t.Errorf("Cancel should cancel the context")
	}
}

func TestCombineAbortSignals_SingleParent(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	ca := CombineAbortSignals(parent)

	if ca.Ctx.Err() != nil {
		t.Errorf("not yet cancelled")
	}
	cancel()

	// Watchdog: parent cancel must propagate within 100ms.
	select {
	case <-ca.Ctx.Done():
		// ok
	case <-time.After(100 * time.Millisecond):
		t.Errorf("parent cancel did not propagate")
	}
}

func TestCombineAbortSignals_MultipleParents(t *testing.T) {
	p1, c1 := context.WithCancel(context.Background())
	p2, c2 := context.WithCancel(context.Background())
	p3, c3 := context.WithCancel(context.Background())
	defer c1()
	defer c3()
	ca := CombineAbortSignals(p1, p2, p3)

	// Cancelling any parent must propagate.
	c2()

	select {
	case <-ca.Ctx.Done():
		// ok
	case <-time.After(100 * time.Millisecond):
		t.Errorf("cancel of p2 did not propagate")
	}
}

func TestCombineAbortSignals_AlreadyCancelled(t *testing.T) {
	p1, c1 := context.WithCancel(context.Background())
	c1()
	_ = p1
	ca := CombineAbortSignals(p1, context.Background())

	// Already-cancelled parent should make the combined context done
	// immediately.
	select {
	case <-ca.Ctx.Done():
		// ok
	case <-time.After(100 * time.Millisecond):
		t.Errorf("already-cancelled parent did not propagate")
	}
}

func TestCombineAbortSignals_NilParents(t *testing.T) {
	ca := CombineAbortSignals(nil, nil)
	if ca.Ctx.Err() != nil {
		t.Errorf("nil parents should be no-op")
	}
}

func TestCombineAbortSignals_IgnoresStrayNils(t *testing.T) {
	p, c := context.WithCancel(context.Background())
	defer c()
	ca := CombineAbortSignals(nil, p, nil)
	c()
	select {
	case <-ca.Ctx.Done():
		// ok
	case <-time.After(100 * time.Millisecond):
		t.Errorf("cancel should propagate despite nil siblings")
	}
}
