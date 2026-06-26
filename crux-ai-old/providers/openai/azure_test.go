package openai

import (
	"context"
	"testing"

	piai "github.com/hycjack/crux-ai"
)

func TestNewAzure(t *testing.T) {
	p := NewAzure()
	if p == nil {
		t.Error("expected non-nil provider")
	}
}

func TestAzureProviderImplementsInterface(t *testing.T) {
	var _ = NewAzure()
}

func TestAzureStreamNoAPIKey(t *testing.T) {
	p := NewAzure()
	model := piai.Model{
		ID:       "gpt-4",
		Provider: piai.ProviderAzureOpenAI,
	}

	_, err := p.Stream(context.Background(), model, piai.Context{}, piai.StreamOptions{})
	if err == nil {
		t.Error("expected error for missing API key")
	}
}

func TestResolveAzureBaseURL(t *testing.T) {
	tests := []struct {
		name string
		model piai.Model
		opts  AzureOptions
		want  string
	}{
		{
			name:  "from options",
			model: piai.Model{},
			opts:  AzureOptions{AzureBaseURL: "https://custom.openai.azure.com"},
			want:  "https://custom.openai.azure.com",
		},
		{
			name:  "from resource name",
			model: piai.Model{},
			opts:  AzureOptions{AzureResourceName: "myresource"},
			want:  "https://myresource.openai.azure.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAzureBaseURL(tt.model, tt.opts)
			if got != tt.want {
				t.Errorf("resolveAzureBaseURL() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestResolveAzureDeploymentName(t *testing.T) {
	tests := []struct {
		name  string
		model piai.Model
		opts  AzureOptions
		want  string
	}{
		{
			name:  "from options",
			model: piai.Model{ID: "gpt-4"},
			opts:  AzureOptions{AzureDeploymentName: "my-deployment"},
			want:  "my-deployment",
		},
		{
			name:  "fallback to model ID",
			model: piai.Model{ID: "gpt-4"},
			opts:  AzureOptions{},
			want:  "gpt-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAzureDeploymentName(tt.model, tt.opts)
			if got != tt.want {
				t.Errorf("resolveAzureDeploymentName() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestMergeMaps(t *testing.T) {
	m1 := map[string]string{"a": "1", "b": "2"}
	m2 := map[string]string{"b": "3", "c": "4"}

	result := mergeMaps(m1, m2)
	if result["a"] != "1" {
		t.Errorf("expected a=1, got %s", result["a"])
	}
	if result["b"] != "3" {
		t.Errorf("expected b=3 (overwritten), got %s", result["b"])
	}
	if result["c"] != "4" {
		t.Errorf("expected c=4, got %s", result["c"])
	}
}
