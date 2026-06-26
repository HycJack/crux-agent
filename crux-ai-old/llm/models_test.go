package llm

import (
	"testing"

	"github.com/hycjack/crux-ai/core"
)

func setupTestModels() {
	LoadModels(map[core.KnownProvider]map[string]core.Model{
		core.ProviderAnthropic: {
			"claude-3-opus": {ID: "claude-3-opus", Provider: core.ProviderAnthropic, API: core.APIAnthropicMessages},
		},
		core.ProviderOpenAI: {
			"gpt-4": {ID: "gpt-4", Provider: core.ProviderOpenAI, API: core.APIOpenAICompletions},
		},
	})
}

func TestAI_GetModel(t *testing.T) {
	setupTestModels()
	m, err := GetModel(core.ProviderAnthropic, "claude-3-opus")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ID != "claude-3-opus" {
		t.Errorf("expected claude-3-opus, got %s", m.ID)
	}
}

func TestAI_GetModelNotFound(t *testing.T) {
	setupTestModels()
	_, err := GetModel(core.ProviderAnthropic, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent model")
	}
}

func TestAI_GetModelUnknownProvider(t *testing.T) {
	setupTestModels()
	_, err := GetModel(core.ProviderFireworks, "model")
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestAI_GetProviders(t *testing.T) {
	setupTestModels()
	providers := GetProviders()
	if len(providers) < 2 {
		t.Errorf("expected at least 2 providers, got %d", len(providers))
	}
}

func TestAI_GetModels(t *testing.T) {
	setupTestModels()
	models := GetModels(core.ProviderOpenAI)
	if len(models) == 0 {
		t.Error("expected at least 1 model")
	}
}

func TestAI_GenerateImagesNoProvider(t *testing.T) {
	core.ClearImagesProviders()
	defer core.ClearImagesProviders()

	_, err := GenerateImages(nil, core.ImagesModel{
		API: "nonexistent-images",
	}, nil)
	if err == nil {
		t.Error("expected error for unregistered provider")
	}
}

func TestAI_GetImageModel(t *testing.T) {
	LoadImageModels(map[core.KnownProvider]map[string]core.ImagesModel{
		core.ProviderOpenRouter: {
			"dall-e": {ID: "dall-e", Provider: core.ProviderOpenRouter, API: "openrouter-images"},
		},
	})
	defer LoadImageModels(nil)

	m, err := GetImageModel(core.ProviderOpenRouter, "dall-e")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ID != "dall-e" {
		t.Errorf("expected dall-e, got %s", m.ID)
	}
}

func TestAI_GetImageModelNotFound(t *testing.T) {
	LoadImageModels(nil)
	_, err := GetImageModel(core.ProviderOpenRouter, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent image model")
	}
}

func TestAI_GetImageProviders(t *testing.T) {
	LoadImageModels(map[core.KnownProvider]map[string]core.ImagesModel{
		core.ProviderOpenRouter: {"x": {ID: "x"}},
	})
	defer LoadImageModels(nil)

	providers := GetImageProviders()
	if len(providers) == 0 {
		t.Error("expected at least 1 image provider")
	}
}

func TestAI_GetImageModels(t *testing.T) {
	LoadImageModels(map[core.KnownProvider]map[string]core.ImagesModel{
		core.ProviderOpenRouter: {
			"dall-e": {ID: "dall-e"},
		},
	})
	defer LoadImageModels(nil)

	models := GetImageModels(core.ProviderOpenRouter)
	if len(models) == 0 {
		t.Error("expected at least 1 image model")
	}
}
