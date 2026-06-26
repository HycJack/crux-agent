package router

import (
	"testing"

	"github.com/hycjack/crux-ai/core"
)

func TestIsValidRole(t *testing.T) {
	for _, r := range AllRoles {
		if !IsValidRole(r) {
			t.Errorf("IsValidRole(%q) = false, want true", r)
		}
	}
	if IsValidRole("bogus") {
		t.Error("IsValidRole(\"bogus\") should be false")
	}
	if IsValidRole("") {
		t.Error("IsValidRole(\"\") should be false")
	}
}

func TestConfig_Materialize_SubstitutesDefault(t *testing.T) {
	cfg := Config{
		Default: ModelSpec{ID: "gpt-4o", Provider: core.ProviderOpenAI, API: core.APIOpenAICompletions},
		Smol:    ModelSpec{ID: "gpt-4o-mini", Provider: core.ProviderOpenAI, API: core.APIOpenAICompletions},
	}
	m, err := cfg.Materialize()
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if m[RoleSmol].ID != "gpt-4o-mini" {
		t.Errorf("Smol should keep its own model, got %s", m[RoleSmol].ID)
	}
	if m[RoleSlow].ID != "gpt-4o" {
		t.Errorf("Slow should fall back to Default, got %s", m[RoleSlow].ID)
	}
	if m[RolePlan].ID != "gpt-4o" {
		t.Errorf("Plan should fall back to Default, got %s", m[RolePlan].ID)
	}
	if m[RoleCommit].ID != "gpt-4o" {
		t.Errorf("Commit should fall back to Default, got %s", m[RoleCommit].ID)
	}
}

func TestConfig_Materialize_NoDefault(t *testing.T) {
	cfg := Config{}
	_, err := cfg.Materialize()
	if err == nil {
		t.Error("Materialize should fail with empty Default")
	}
}

func TestNew_EmptyDefault(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Error("expected error for empty Default")
	}
}

func TestRoleRouter_Resolve_UnknownRole(t *testing.T) {
	r, _ := New(Config{Default: ModelSpec{ID: "gpt-4o", Provider: core.ProviderOpenAI, API: core.APIOpenAICompletions}})
	_, err := r.Resolve("nonexistent")
	if err == nil {
		t.Error("expected error for unknown role")
	}
}

func TestRoleRouter_SetConfig_Atomic(t *testing.T) {
	r, _ := New(Config{Default: ModelSpec{ID: "gpt-4o", Provider: core.ProviderOpenAI, API: core.APIOpenAICompletions}})

	newCfg := Config{
		Default: ModelSpec{ID: "gpt-4.1", Provider: core.ProviderOpenAI, API: core.APIOpenAICompletions},
		Smol:    ModelSpec{ID: "gpt-4.1-mini", Provider: core.ProviderOpenAI, API: core.APIOpenAICompletions},
	}
	if err := r.SetConfig(newCfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if r.Config().Default.ID != "gpt-4.1" {
		t.Errorf("Config not updated")
	}
}

func TestModelSpec_IsZero(t *testing.T) {
	var m ModelSpec
	if !m.IsZero() {
		t.Error("zero ModelSpec should report IsZero=true")
	}
	m = ModelSpec{ID: "test"}
	if m.IsZero() {
		t.Error("non-zero ModelSpec should report IsZero=false")
	}
}

func TestMustNew_PanicOnError(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustNew should panic on error")
		}
	}()
	MustNew(Config{})
}
