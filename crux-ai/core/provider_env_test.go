package core

import "testing"

func TestGetProviderEnvValue_OverridePrecedence(t *testing.T) {
	t.Setenv("CRUX_TEST_VAR", "process")
	got := GetProviderEnvValue("CRUX_TEST_VAR", ProviderEnv{"CRUX_TEST_VAR": "override"})
	if got != "override" {
		t.Fatalf("override precedence: got %q, want override", got)
	}
}

func TestGetProviderEnvValue_ProcessFallback(t *testing.T) {
	t.Setenv("CRUX_TEST_VAR", "process")
	got := GetProviderEnvValue("CRUX_TEST_VAR", nil)
	if got != "process" {
		t.Fatalf("process fallback: got %q, want process", got)
	}
}

func TestGetProviderEnvValue_Missing(t *testing.T) {
	got := GetProviderEnvValue("CRUX_DEFINITELY_NOT_SET_123", nil)
	if got != "" {
		t.Fatalf("missing var: got %q, want empty", got)
	}
}

func TestGetProviderEnvValue_EmptyOverrideFallsThrough(t *testing.T) {
	t.Setenv("CRUX_TEST_VAR", "process")
	got := GetProviderEnvValue("CRUX_TEST_VAR", ProviderEnv{"CRUX_TEST_VAR": ""})
	if got != "process" {
		t.Fatalf("empty override should fall through: got %q", got)
	}
}

func TestHasProviderEnvValue(t *testing.T) {
	if HasProviderEnvValue("CRUX_NOT_SET_123", nil) {
		t.Fatalf("missing var should report false")
	}
	t.Setenv("CRUX_SET_VAR", "yes")
	if !HasProviderEnvValue("CRUX_SET_VAR", nil) {
		t.Fatalf("set var should report true")
	}
}