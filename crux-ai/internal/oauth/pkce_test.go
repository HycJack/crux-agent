package oauth

import (
	"testing"
)

func TestGeneratePKCE(t *testing.T) {
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pkce.Verifier == "" {
		t.Error("expected non-empty verifier")
	}
	if pkce.Challenge == "" {
		t.Error("expected non-empty challenge")
	}
	if pkce.Verifier == pkce.Challenge {
		t.Error("verifier and challenge should be different")
	}
}

func TestGeneratePKCEUniqueness(t *testing.T) {
	pkce1, _ := GeneratePKCE()
	pkce2, _ := GeneratePKCE()

	if pkce1.Verifier == pkce2.Verifier {
		t.Error("expected unique verifiers")
	}
	if pkce1.Challenge == pkce2.Challenge {
		t.Error("expected unique challenges")
	}
}

func TestGeneratePKCEVerifierLength(t *testing.T) {
	pkce, _ := GeneratePKCE()

	// Base64 raw URL encoding of 32 bytes = 43 characters
	if len(pkce.Verifier) < 40 || len(pkce.Verifier) > 50 {
		t.Errorf("unexpected verifier length: %d", len(pkce.Verifier))
	}
}
