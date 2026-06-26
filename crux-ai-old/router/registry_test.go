package router

import (
	"errors"
	"testing"

	"github.com/hycjack/crux-ai/core"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry() returned nil")
	}
	if r.Count() != 0 {
		t.Errorf("new registry should be empty, got count=%d", r.Count())
	}
}

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	// We don't have a real APIProvider to use here, so test the
	// nil-safety and error path of Lookup.
	if r.Count() != 0 {
		t.Errorf("expected empty registry, got count=%d", r.Count())
	}

	_, err := r.Lookup(core.ProviderOpenAI)
	if err == nil {
		t.Error("expected error for missing provider")
	}
	if !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("err should wrap ErrProviderNotFound, got %v", err)
	}
}

func TestRegistry_RegisterNil(t *testing.T) {
	r := NewRegistry()
	r.Register(nil) // should be a no-op
	if r.Count() != 0 {
		t.Errorf("Register(nil) should be a no-op, got count=%d", r.Count())
	}
}

func TestRegistry_ForModel_EmptyProvider(t *testing.T) {
	r := NewRegistry()
	_, err := r.ForModel(core.Model{ID: "test", Provider: ""})
	if err == nil {
		t.Fatal("expected error for empty Provider")
	}
}

func TestRegistry_ForModel_NotRegistered(t *testing.T) {
	r := NewRegistry()
	_, err := r.ForModel(core.Model{ID: "test", Provider: core.ProviderOpenAI})
	if !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("expected ErrProviderNotFound, got %v", err)
	}
}

func TestRegistry_Has(t *testing.T) {
	r := NewRegistry()
	if r.Has(core.ProviderOpenAI) {
		t.Error("Has(openai) should be false on empty registry")
	}
}

func TestRegistry_Names_Empty(t *testing.T) {
	r := NewRegistry()
	names := r.Names()
	if len(names) != 0 {
		t.Errorf("empty registry should have empty Names, got %v", names)
	}
}

func TestRegistry_Clear(t *testing.T) {
	r := NewRegistry()
	r.Clear() // should be safe on empty registry
	if r.Count() != 0 {
		t.Errorf("Clear on empty registry should keep count at 0, got %d", r.Count())
	}
}

func TestErrProviderNotFound(t *testing.T) {
	// Verify the sentinel error is properly defined.
	if ErrProviderNotFound == nil {
		t.Error("ErrProviderNotFound should not be nil")
	}
	if ErrProviderNotFound.Error() == "" {
		t.Error("ErrProviderNotFound should have a non-empty message")
	}
}
