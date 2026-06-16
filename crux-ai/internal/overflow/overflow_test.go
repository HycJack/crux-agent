package overflow

import (
	"testing"
)

func TestIsOverflowError(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		window   int
		usage    int
		expected bool
	}{
		{
			name:     "context length exceeded",
			errMsg:   "This request would exceed the context length limit",
			expected: true,
		},
		{
			name:     "maximum context window",
			errMsg:   "Maximum context window of 200000 tokens exceeded",
			expected: true,
		},
		{
			name:     "prompt too long",
			errMsg:   "prompt is too long: 150000 tokens",
			expected: true,
		},
		{
			name:     "context_length_exceeded",
			errMsg:   "context_length_exceeded",
			expected: true,
		},
		{
			name:     "too many tokens",
			errMsg:   "Request contains too many tokens",
			expected: true,
		},
		{
			name:     "non-overflow error",
			errMsg:   "rate limit exceeded",
			expected: false,
		},
		{
			name:     "empty error",
			errMsg:   "",
			expected: false,
		},
		{
			name:     "silent overflow",
			errMsg:   "",
			window:   100000,
			usage:    150000,
			expected: true,
		},
		{
			name:     "within window",
			errMsg:   "",
			window:   100000,
			usage:    50000,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsOverflow(tt.errMsg, tt.window, tt.usage)
			if result != tt.expected {
				t.Errorf("IsOverflow(%q, %d, %d) = %v, want %v",
					tt.errMsg, tt.window, tt.usage, result, tt.expected)
			}
		})
	}
}

func TestGetPatterns(t *testing.T) {
	patterns := GetPatterns()
	if len(patterns) == 0 {
		t.Error("expected non-empty patterns")
	}
}
