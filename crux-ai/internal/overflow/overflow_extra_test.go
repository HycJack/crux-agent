package overflow

import "testing"

func TestIsOverflowExtendedPatterns(t *testing.T) {
	cases := []struct {
		msg    string
		window int
		usage  int
		want   bool
	}{
		{"z.ai model_context_window_exceeded", 0, 0, true},
		{"HTTP 413 (no body)", 0, 0, true},
		{"request_too_large error from anthropic", 0, 0, true},
		{"ollama prompt too long; exceeded max context length", 0, 0, true},
		{"input token count of 250000 exceeds the maximum of 200000", 0, 0, true},
		{"rate limit exceeded", 0, 0, false},
		{"throttling error: 429", 0, 0, false},
		{"exceeded model token limit", 0, 0, true},
		{"too large for model with 32000 maximum context length", 0, 0, true},
		{"reduce the length of the messages", 0, 0, true},
	}
	for _, c := range cases {
		if got := IsOverflow(c.msg, c.window, c.usage); got != c.want {
			t.Errorf("IsOverflow(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}
