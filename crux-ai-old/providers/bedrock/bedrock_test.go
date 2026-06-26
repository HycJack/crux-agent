package bedrock

import (
	"testing"

	piai "github.com/hycjack/crux-ai"
)

func TestMapBedrockStopReason(t *testing.T) {
	tests := []struct {
		input string
		want  piai.StopReason
	}{
		{"end_turn", piai.StopStop},
		{"tool_use", piai.StopToolUse},
		{"max_tokens", piai.StopLength},
		{"stop_sequence", piai.StopStop},
		{"unknown", piai.StopStop},
	}

	for _, tt := range tests {
		got := mapBedrockStopReason(tt.input)
		if got != tt.want {
			t.Errorf("mapBedrockStopReason(%s) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestMimeToFormat(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"image/png", "png"},
		{"image/jpeg", "jpeg"},
		{"text/plain", "plain"},
	}

	for _, tt := range tests {
		got := mimeToFormat(tt.input)
		if got != tt.want {
			t.Errorf("mimeToFormat(%s) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestMapStatus(t *testing.T) {
	if mapStatus(true) != "error" {
		t.Error("expected 'error' for true")
	}
	if mapStatus(false) != "success" {
		t.Error("expected 'success' for false")
	}
}
