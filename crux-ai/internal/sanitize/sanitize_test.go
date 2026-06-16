package sanitize

import (
	"testing"
)

func TestSurrogatesValidUTF8(t *testing.T) {
	input := "Hello, World! 你好世界"
	output := Surrogates(input)
	if output != input {
		t.Errorf("expected unchanged string, got %s", output)
	}
}

func TestSurrogatesRemovesInvalid(t *testing.T) {
	// Create a string with invalid UTF-8 bytes
	input := "Hello" + string([]byte{0xff, 0xfe}) + "World"
	output := Surrogates(input)
	if output == input {
		t.Error("expected invalid bytes to be removed")
	}
	// Should still contain valid parts
	if len(output) == 0 {
		t.Error("expected non-empty output")
	}
}

func TestSurrogatesEmpty(t *testing.T) {
	output := Surrogates("")
	if output != "" {
		t.Errorf("expected empty string, got %s", output)
	}
}

func TestSurrogatesEmoji(t *testing.T) {
	input := "Hello 🌍 World"
	output := Surrogates(input)
	if output != input {
		t.Errorf("expected emoji preserved, got %s", output)
	}
}
