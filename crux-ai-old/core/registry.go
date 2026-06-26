/*
 * 功能说明：API 提供者注册表
 * 
 * 解决的问题：
 * 1. 需要管理多个 AI 提供者的注册和查找
 * 2. 需要线程安全的提供者注册表
 * 3. 需要支持按来源 ID 批量注销提供者
 * 4. 需要同时支持文本生成和图像生成两种 API
 * 
 * 解决方案：
 * 1. 使用 map 存储提供者，配合读写锁保证线程安全
 * 2. 提供 Register/Unregister/Get/List 等管理函数
 * 3. 使用 sourceID 追踪注册来源，支持批量注销
 * 4. 分别维护文本和图像两个注册表
 * 
 * 应用场景：
 * - providers/ 层在初始化时注册自己
 * - ai/ 层通过 GetProvider 获取提供者实例
 * - 支持动态加载和卸载提供者
 */
package core

import (
	"context"
	"fmt"
	"sync"
)

// APIProvider is the interface for text generation API providers.
// || 文本生成 API 提供者接口
type APIProvider interface {
	// Stream performs a streaming completion request.
	// || 执行流式补全请求
	Stream(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) (*AssistantMessageEventStream, error)
	// StreamSimple performs a streaming completion with unified reasoning controls.
	// || 执行带统一推理控制的流式补全
	StreamSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) (*AssistantMessageEventStream, error)
}

// apiProviders stores registered API providers by API type.
// || 按 API 类型存储注册的 API 提供者
var (
	apiProviders   = make(map[KnownAPI]APIProvider)     // API 类型 -> 提供者
	apiProviderSrc = make(map[string][]KnownAPI)       // 来源 ID -> API 类型列表
	apiProvidersMu sync.RWMutex                        // 保护并发访问
)

// RegisterProvider registers an API provider for the given API type.
// || 为指定的 API 类型注册提供者
// 参数：
//   api - API 类型
//   provider - 提供者实例
//   sourceID - 可选的来源 ID（用于批量注销）
func RegisterProvider(api KnownAPI, provider APIProvider, sourceID ...string) {
	apiProvidersMu.Lock()
	defer apiProvidersMu.Unlock()
	apiProviders[api] = provider
	if len(sourceID) > 0 {
		apiProviderSrc[sourceID[0]] = append(apiProviderSrc[sourceID[0]], api)
	}
}

// GetProvider returns the API provider for the given API type.
// || 返回指定 API 类型的提供者
// 参数：
//   api - API 类型
// 返回：
//   提供者实例和错误
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
// || 返回所有已注册的 API 类型
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
// || 移除指定来源 ID 注册的所有提供者
// 参数：
//   sourceID - 来源 ID
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
// || 移除所有已注册的提供者（用于测试）
func ClearProviders() {
	apiProvidersMu.Lock()
	defer apiProvidersMu.Unlock()
	apiProviders = make(map[KnownAPI]APIProvider)
	apiProviderSrc = make(map[string][]KnownAPI)
}

// --- Images API Provider Registry ---
// || --- 图像 API 提供者注册表 ---

// ImagesAPIProvider is the interface for image generation API providers.
// || 图像生成 API 提供者接口
type ImagesAPIProvider interface {
	// GenerateImages generates images from a prompt.
	// || 根据提示生成图像
	GenerateImages(model ImagesModel, llmCtx Context, opts ImageOptions) (*AssistantImages, error)
}

// imagesProviders stores registered image API providers by API type.
// || 按 API 类型存储注册的图像 API 提供者
var (
	imagesProviders   = make(map[KnownAPI]ImagesAPIProvider) // API 类型 -> 图像提供者
	imagesProviderSrc = make(map[string][]KnownAPI)         // 来源 ID -> API 类型列表
	imagesProvidersMu sync.RWMutex                          // 保护并发访问
)

// RegisterImagesProvider registers an image API provider.
// || 注册图像 API 提供者
// 参数：
//   api - API 类型
//   provider - 图像提供者实例
//   sourceID - 可选的来源 ID
func RegisterImagesProvider(api KnownAPI, provider ImagesAPIProvider, sourceID ...string) {
	imagesProvidersMu.Lock()
	defer imagesProvidersMu.Unlock()
	imagesProviders[api] = provider
	if len(sourceID) > 0 {
		imagesProviderSrc[sourceID[0]] = append(imagesProviderSrc[sourceID[0]], api)
	}
}

// GetImagesProvider returns the image API provider for the given API type.
// || 返回指定 API 类型的图像提供者
// 参数：
//   api - API 类型
// 返回：
//   图像提供者实例和错误
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
// || 返回所有已注册的图像 API 类型
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
// || 移除指定来源 ID 注册的所有图像提供者
// 参数：
//   sourceID - 来源 ID
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
// || 移除所有已注册的图像提供者（用于测试）
func ClearImagesProviders() {
	imagesProvidersMu.Lock()
	defer imagesProvidersMu.Unlock()
	imagesProviders = make(map[KnownAPI]ImagesAPIProvider)
	imagesProviderSrc = make(map[string][]KnownAPI)
}
