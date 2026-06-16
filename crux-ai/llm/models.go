package llm

import (
	"fmt"
	"sync"

	"github.com/hycjack/crux-ai/core"
)

var (
	modelsMap = make(map[core.KnownProvider]map[string]core.Model)
	modelsMu  sync.RWMutex
)

// LoadModels loads models into the registry. Called during init with generated data.
func LoadModels(models map[core.KnownProvider]map[string]core.Model) {
	modelsMu.Lock()
	defer modelsMu.Unlock()
	modelsMap = models
}

// GetModel looks up a model by provider and model ID.
func GetModel(provider core.KnownProvider, modelID string) (core.Model, error) {
	modelsMu.RLock()
	defer modelsMu.RUnlock()

	providerModels, ok := modelsMap[provider]
	if !ok {
		return core.Model{}, fmt.Errorf("unknown provider: %s", provider)
	}

	model, ok := providerModels[modelID]
	if !ok {
		return core.Model{}, fmt.Errorf("unknown model: %s/%s", provider, modelID)
	}

	return model, nil
}

// GetProviders returns all registered provider names.
func GetProviders() []core.KnownProvider {
	modelsMu.RLock()
	defer modelsMu.RUnlock()

	providers := make([]core.KnownProvider, 0, len(modelsMap))
	for p := range modelsMap {
		providers = append(providers, p)
	}
	return providers
}

// GetModels returns all models for a given provider.
func GetModels(provider core.KnownProvider) []core.Model {
	modelsMu.RLock()
	defer modelsMu.RUnlock()

	providerModels, ok := modelsMap[provider]
	if !ok {
		return nil
	}

	models := make([]core.Model, 0, len(providerModels))
	for _, m := range providerModels {
		models = append(models, m)
	}
	return models
}

// GetSupportedThinkingLevels returns the thinking levels supported by a model.
func GetSupportedThinkingLevels(model core.Model) []core.ThinkingLevel {
	if !model.Reasoning {
		return nil
	}

	if model.ThinkingLevelMap != nil {
		levels := make([]core.ThinkingLevel, 0, len(model.ThinkingLevelMap))
		for level := range model.ThinkingLevelMap {
			levels = append(levels, core.ThinkingLevel(level))
		}
		return levels
	}

	return []core.ThinkingLevel{core.ThinkingLow, core.ThinkingMedium, core.ThinkingHigh}
}

// ClampThinkingLevel finds the closest supported thinking level.
func ClampThinkingLevel(model core.Model, level core.ThinkingLevel) core.ThinkingLevel {
	supported := GetSupportedThinkingLevels(model)
	if len(supported) == 0 {
		return core.ThinkingMedium
	}

	for _, s := range supported {
		if s == level {
			return level
		}
	}

	levelOrder := []core.ThinkingLevel{core.ThinkingMinimal, core.ThinkingLow, core.ThinkingMedium, core.ThinkingHigh, core.ThinkingXHigh}
	targetIdx := -1
	for i, l := range levelOrder {
		if l == level {
			targetIdx = i
			break
		}
	}

	if targetIdx < 0 {
		targetIdx = 2
	}

	bestIdx := -1
	bestDist := len(levelOrder) + 1
	for _, s := range supported {
		for i, l := range levelOrder {
			if l == s {
				dist := abs(i - targetIdx)
				if dist < bestDist {
					bestDist = dist
					bestIdx = i
				}
			}
		}
	}

	if bestIdx >= 0 {
		return levelOrder[bestIdx]
	}

	return supported[0]
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ModelsAreEqual compares two models by id and provider.
func ModelsAreEqual(a, b core.Model) bool {
	return a.ID == b.ID && a.Provider == b.Provider
}

// --- Image Model Registry ---

var (
	imageModelsMap = make(map[core.KnownProvider]map[string]core.ImagesModel)
	imageModelsMu  sync.RWMutex
)

// LoadImageModels loads image models into the registry.
func LoadImageModels(models map[core.KnownProvider]map[string]core.ImagesModel) {
	imageModelsMu.Lock()
	defer imageModelsMu.Unlock()
	imageModelsMap = models
}

// GetImageModel looks up an image model by provider and model ID.
func GetImageModel(provider core.KnownProvider, modelID string) (core.ImagesModel, error) {
	imageModelsMu.RLock()
	defer imageModelsMu.RUnlock()

	providerModels, ok := imageModelsMap[provider]
	if !ok {
		return core.ImagesModel{}, fmt.Errorf("unknown image provider: %s", provider)
	}

	model, ok := providerModels[modelID]
	if !ok {
		return core.ImagesModel{}, fmt.Errorf("unknown image model: %s/%s", provider, modelID)
	}

	return model, nil
}

// GetImageProviders returns all registered image provider names.
func GetImageProviders() []core.KnownProvider {
	imageModelsMu.RLock()
	defer imageModelsMu.RUnlock()

	providers := make([]core.KnownProvider, 0, len(imageModelsMap))
	for p := range imageModelsMap {
		providers = append(providers, p)
	}
	return providers
}

// GetImageModels returns all image models for a given provider.
func GetImageModels(provider core.KnownProvider) []core.ImagesModel {
	imageModelsMu.RLock()
	defer imageModelsMu.RUnlock()

	providerModels, ok := imageModelsMap[provider]
	if !ok {
		return nil
	}

	models := make([]core.ImagesModel, 0, len(providerModels))
	for _, m := range providerModels {
		models = append(models, m)
	}
	return models
}
