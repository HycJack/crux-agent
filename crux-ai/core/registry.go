package core

import (
	"context"
	"fmt"
	"sync"
)

// APIProvider is the interface for text generation API providers.
type APIProvider interface {
	Stream(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) (*AssistantMessageEventStream, error)
	StreamSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) (*AssistantMessageEventStream, error)
}

var (
	apiProviders   = make(map[KnownAPI]APIProvider)
	apiProviderSrc = make(map[string][]KnownAPI)
	apiProvidersMu sync.RWMutex
)

// RegisterProvider registers an API provider for the given API type.
func RegisterProvider(api KnownAPI, provider APIProvider, sourceID ...string) {
	apiProvidersMu.Lock()
	defer apiProvidersMu.Unlock()
	apiProviders[api] = provider
	if len(sourceID) > 0 {
		apiProviderSrc[sourceID[0]] = append(apiProviderSrc[sourceID[0]], api)
	}
}

// GetProvider returns the API provider for the given API type.
func GetProvider(api KnownAPI) (APIProvider, error) {
	apiProvidersMu.RLock()
	defer apiProvidersMu.RUnlock()
	p, ok := apiProviders[api]
	if !ok {
		return nil, fmt.Errorf("no provider registered for API: %s", api)
	}
	return p, nil
}

// GetRegisteredProviders returns all registered API types.
func GetRegisteredProviders() []KnownAPI {
	apiProvidersMu.RLock()
	defer apiProvidersMu.RUnlock()
	apis := make([]KnownAPI, 0, len(apiProviders))
	for api := range apiProviders {
		apis = append(apis, api)
	}
	return apis
}

// UnregisterProviders removes all providers registered with the given source ID.
func UnregisterProviders(sourceID string) {
	apiProvidersMu.Lock()
	defer apiProvidersMu.Unlock()
	apis, ok := apiProviderSrc[sourceID]
	if !ok {
		return
	}
	for _, api := range apis {
		delete(apiProviders, api)
	}
	delete(apiProviderSrc, sourceID)
}

// ClearProviders removes all registered providers.
func ClearProviders() {
	apiProvidersMu.Lock()
	defer apiProvidersMu.Unlock()
	apiProviders = make(map[KnownAPI]APIProvider)
	apiProviderSrc = make(map[string][]KnownAPI)
}

// --- Images API Provider Registry ---

// ImagesAPIProvider is the interface for image generation API providers.
type ImagesAPIProvider interface {
	GenerateImages(model ImagesModel, llmCtx Context, opts ImageOptions) (*AssistantImages, error)
}

var (
	imagesProviders   = make(map[KnownAPI]ImagesAPIProvider)
	imagesProviderSrc = make(map[string][]KnownAPI)
	imagesProvidersMu sync.RWMutex
)

// RegisterImagesProvider registers an image API provider.
func RegisterImagesProvider(api KnownAPI, provider ImagesAPIProvider, sourceID ...string) {
	imagesProvidersMu.Lock()
	defer imagesProvidersMu.Unlock()
	imagesProviders[api] = provider
	if len(sourceID) > 0 {
		imagesProviderSrc[sourceID[0]] = append(imagesProviderSrc[sourceID[0]], api)
	}
}

// GetImagesProvider returns the image API provider for the given API type.
func GetImagesProvider(api KnownAPI) (ImagesAPIProvider, error) {
	imagesProvidersMu.RLock()
	defer imagesProvidersMu.RUnlock()
	p, ok := imagesProviders[api]
	if !ok {
		return nil, fmt.Errorf("no images provider registered for API: %s", api)
	}
	return p, nil
}

// GetRegisteredImagesProviders returns all registered image API types.
func GetRegisteredImagesProviders() []KnownAPI {
	imagesProvidersMu.RLock()
	defer imagesProvidersMu.RUnlock()
	apis := make([]KnownAPI, 0, len(imagesProviders))
	for api := range imagesProviders {
		apis = append(apis, api)
	}
	return apis
}

// UnregisterImagesProviders removes all image providers registered with the given source ID.
func UnregisterImagesProviders(sourceID string) {
	imagesProvidersMu.Lock()
	defer imagesProvidersMu.Unlock()
	apis, ok := imagesProviderSrc[sourceID]
	if !ok {
		return
	}
	for _, api := range apis {
		delete(imagesProviders, api)
	}
	delete(imagesProviderSrc, sourceID)
}

// ClearImagesProviders removes all registered image providers.
func ClearImagesProviders() {
	imagesProvidersMu.Lock()
	defer imagesProvidersMu.Unlock()
	imagesProviders = make(map[KnownAPI]ImagesAPIProvider)
	imagesProviderSrc = make(map[string][]KnownAPI)
}
