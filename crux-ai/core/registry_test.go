package core

import (
	"context"
	"testing"
)

// mockAPIProvider 用于测试的 mock provider
type mockAPIProvider struct {
	name string
}

func (m *mockAPIProvider) Stream(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) (*AssistantMessageEventStream, error) {
	s := NewEventStream[AssistantMessageEvent, AssistantMessage]()
	go s.End(AssistantMessage{Role: "assistant", Model: model.ID})
	return s, nil
}

func (m *mockAPIProvider) StreamSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) (*AssistantMessageEventStream, error) {
	s := NewEventStream[AssistantMessageEvent, AssistantMessage]()
	go s.End(AssistantMessage{Role: "assistant", Model: model.ID})
	return s, nil
}

func (m *mockAPIProvider) GetName() string { return m.name }

// mockImagesProvider 用于测试的 mock image provider
type mockImagesProvider struct {
	name string
}

func (m *mockImagesProvider) GenerateImages(model ImagesModel, llmCtx Context, opts ImageOptions) (*AssistantImages, error) {
	return &AssistantImages{API: model.API, Provider: model.Provider, Model: model.ID}, nil
}

func (m *mockImagesProvider) GetName() string { return m.name }

// setupTestRegistry 设置测试用的注册表
func setupTestRegistry() func() {
	ClearProviders()
	ClearImagesProviders()
	return func() {
		ClearProviders()
		ClearImagesProviders()
	}
}

// TestRegisterProvider 测试基本的 provider 注册
func TestRegisterProvider(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	provider := &mockAPIProvider{name: "test"}
	RegisterProvider("test-api-1", provider)

	p, err := GetProvider("test-api-1")
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}
	if p != provider {
		t.Error("Expected same provider instance")
	}
}

// TestRegisterProviderWithSource 测试带 source ID 的注册
func TestRegisterProviderWithSource(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	provider := &mockAPIProvider{name: "test"}
	RegisterProvider("test-api-2", provider, "test-source")

	apis := GetRegisteredProviders()
	found := false
	for _, api := range apis {
		if api == "test-api-2" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected 'test-api-2' to be registered")
	}
}

// TestGetProviderNotFound 测试获取未注册的 provider
func TestGetProviderNotFound(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	_, err := GetProvider("nonexistent-api")
	if err == nil {
		t.Fatal("Expected error for non-existent provider")
	}
}

// TestUnregisterProviders 测试注销 providers
func TestUnregisterProviders(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	RegisterProvider("api-1", &mockAPIProvider{}, "source-1")
	RegisterProvider("api-2", &mockAPIProvider{}, "source-1")
	RegisterProvider("api-3", &mockAPIProvider{}, "source-2")

	UnregisterProviders("source-1")

	_, err := GetProvider("api-1")
	if err == nil {
		t.Error("api-1 should be unregistered")
	}
	_, err = GetProvider("api-2")
	if err == nil {
		t.Error("api-2 should be unregistered")
	}

	_, err = GetProvider("api-3")
	if err != nil {
		t.Errorf("api-3 should still be registered: %v", err)
	}
}

// TestUnregisterProvidersNonExistentSource 测试注销不存在的 source
func TestUnregisterProvidersNonExistentSource(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Should not panic on non-existent source: %v", r)
		}
	}()

	UnregisterProviders("non-existent-source")
}

// TestClearProviders 测试清空所有 providers
func TestClearProviders(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	RegisterProvider("api-1", &mockAPIProvider{})
	RegisterProvider("api-2", &mockAPIProvider{})

	ClearProviders()

	apis := GetRegisteredProviders()
	if len(apis) != 0 {
		t.Errorf("Expected 0 providers after clear, got %d", len(apis))
	}
}

// TestGetRegisteredProvidersEmpty 测试空注册表
func TestGetRegisteredProvidersEmpty(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	apis := GetRegisteredProviders()
	if len(apis) != 0 {
		t.Errorf("Expected 0 providers, got %d", len(apis))
	}
}

// TestRegisterImagesProvider 测试注册 images provider
func TestRegisterImagesProvider(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	provider := &mockImagesProvider{}
	RegisterImagesProvider("test-images-api", provider)

	p, err := GetImagesProvider("test-images-api")
	if err != nil {
		t.Fatalf("GetImagesProvider failed: %v", err)
	}
	if p != provider {
		t.Error("Expected same provider instance")
	}
}

// TestGetImagesProviderNotFound 测试获取未注册的 images provider
func TestGetImagesProviderNotFound(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	_, err := GetImagesProvider("nonexistent-images")
	if err == nil {
		t.Fatal("Expected error for non-existent images provider")
	}
}

// TestUnregisterImagesProviders 测试注销 images providers
func TestUnregisterImagesProviders(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	RegisterImagesProvider("img-1", &mockImagesProvider{}, "source-1")
	RegisterImagesProvider("img-2", &mockImagesProvider{}, "source-1")
	RegisterImagesProvider("img-3", &mockImagesProvider{}, "source-2")

	UnregisterImagesProviders("source-1")

	_, err := GetImagesProvider("img-1")
	if err == nil {
		t.Error("img-1 should be unregistered")
	}
	_, err = GetImagesProvider("img-2")
	if err == nil {
		t.Error("img-2 should be unregistered")
	}

	_, err = GetImagesProvider("img-3")
	if err != nil {
		t.Errorf("img-3 should still be registered: %v", err)
	}
}

// TestClearImagesProviders 测试清空 images providers
func TestClearImagesProviders(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	RegisterImagesProvider("img-1", &mockImagesProvider{})
	RegisterImagesProvider("img-2", &mockImagesProvider{})

	ClearImagesProviders()

	apis := GetRegisteredImagesProviders()
	if len(apis) != 0 {
		t.Errorf("Expected 0 image providers after clear, got %d", len(apis))
	}
}

// TestGetRegisteredImagesProvidersEmpty 测试空 images 注册表
func TestGetRegisteredImagesProvidersEmpty(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	apis := GetRegisteredImagesProviders()
	if len(apis) != 0 {
		t.Errorf("Expected 0 image providers, got %d", len(apis))
	}
}

// TestGetRegisteredImagesProviders 测试列出已注册的 images providers
func TestGetRegisteredImagesProviders(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	RegisterImagesProvider("img-a", &mockImagesProvider{})
	RegisterImagesProvider("img-b", &mockImagesProvider{})

	apis := GetRegisteredImagesProviders()
	if len(apis) != 2 {
		t.Errorf("Expected 2 image providers, got %d", len(apis))
	}
}

// TestProviderOverwrite 测试覆盖已注册的 provider
func TestProviderOverwrite(t *testing.T) {
	cleanup := setupTestRegistry()
	defer cleanup()

	p1 := &mockAPIProvider{name: "first"}
	p2 := &mockAPIProvider{name: "second"}

	RegisterProvider("api", p1)
	RegisterProvider("api", p2)

	p, err := GetProvider("api")
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}
	if p == p1 {
		t.Error("Expected new provider to overwrite old one")
	}
	if p != p2 {
		t.Error("Expected the second provider")
	}
}
