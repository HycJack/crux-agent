package core

import (
	"context"
	"testing"
)

type mockProvider struct{}

func (m *mockProvider) Stream(ctx context.Context, model Model, llmCtx Context, opts StreamOptions) (*AssistantMessageEventStream, error) {
	return NewEventStream[AssistantMessageEvent, AssistantMessage](), nil
}

func (m *mockProvider) StreamSimple(ctx context.Context, model Model, llmCtx Context, opts SimpleStreamOptions) (*AssistantMessageEventStream, error) {
	return NewEventStream[AssistantMessageEvent, AssistantMessage](), nil
}

func TestRegisterAndGetProvider(t *testing.T) {
	ClearProviders()
	defer ClearProviders()

	p := &mockProvider{}
	RegisterProvider(APIAnthropicMessages, p, "test")

	got, err := GetProvider(APIAnthropicMessages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// got is APIProvider, p is *mockProvider - compare via type assertion
	if got.(*mockProvider) != p {
		t.Error("expected same provider")
	}
}

func TestGetProviderNotFound(t *testing.T) {
	ClearProviders()
	defer ClearProviders()

	_, err := GetProvider(APIAnthropicMessages)
	if err == nil {
		t.Error("expected error for unregistered provider")
	}
}

func TestGetRegisteredProviders(t *testing.T) {
	ClearProviders()
	defer ClearProviders()

	RegisterProvider(APIAnthropicMessages, &mockProvider{}, "test")
	RegisterProvider(APIOpenAICompletions, &mockProvider{}, "test")

	providers := GetRegisteredProviders()
	if len(providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(providers))
	}
}

func TestUnregisterProviders(t *testing.T) {
	ClearProviders()
	defer ClearProviders()

	RegisterProvider(APIAnthropicMessages, &mockProvider{}, "src1")
	RegisterProvider(APIOpenAICompletions, &mockProvider{}, "src1")
	RegisterProvider(APIBedrockConverse, &mockProvider{}, "src2")

	UnregisterProviders("src1")

	providers := GetRegisteredProviders()
	if len(providers) != 1 {
		t.Errorf("expected 1 provider after unregister, got %d", len(providers))
	}

	_, err := GetProvider(APIBedrockConverse)
	if err != nil {
		t.Error("expected bedrock to still be registered")
	}
}

func TestClearProviders(t *testing.T) {
	RegisterProvider(APIAnthropicMessages, &mockProvider{}, "test")
	ClearProviders()

	providers := GetRegisteredProviders()
	if len(providers) != 0 {
		t.Errorf("expected 0 providers after clear, got %d", len(providers))
	}
}

type mockImagesProvider struct{}

func (m *mockImagesProvider) GenerateImages(model ImagesModel, llmCtx Context, opts ImageOptions) (*AssistantImages, error) {
	return &AssistantImages{
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		StopReason: StopStop,
	}, nil
}

func TestRegisterAndGetImagesProvider(t *testing.T) {
	ClearImagesProviders()
	defer ClearImagesProviders()

	p := &mockImagesProvider{}
	RegisterImagesProvider("test-images", p, "test")

	got, err := GetImagesProvider("test-images")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(*mockImagesProvider) != p {
		t.Error("expected same provider")
	}
}

func TestGetImagesProviderNotFound(t *testing.T) {
	ClearImagesProviders()
	defer ClearImagesProviders()

	_, err := GetImagesProvider("nonexistent")
	if err == nil {
		t.Error("expected error for unregistered provider")
	}
}

func TestGetRegisteredImagesProviders(t *testing.T) {
	ClearImagesProviders()
	defer ClearImagesProviders()

	RegisterImagesProvider("test1", &mockImagesProvider{}, "test")
	RegisterImagesProvider("test2", &mockImagesProvider{}, "test")

	providers := GetRegisteredImagesProviders()
	if len(providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(providers))
	}
}

func TestClearImagesProviders(t *testing.T) {
	RegisterImagesProvider("test", &mockImagesProvider{}, "test")
	ClearImagesProviders()

	providers := GetRegisteredImagesProviders()
	if len(providers) != 0 {
		t.Errorf("expected 0 providers, got %d", len(providers))
	}
}
