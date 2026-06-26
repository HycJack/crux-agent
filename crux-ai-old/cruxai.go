// Package cruxai is the unified entry point for crux-llm.
// It re-exports types from core and functions from llm,
// so external consumers can import a single package.
//
// Architecture:
//   core/      — pure types, EventStream, constants, provider registry
//   llm/       — public API (Stream/Complete), model management
//   providers/ — LLM provider implementations
package cruxai

import (
	"context"

	"github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/llm"
)

// ============================================================
// Type aliases (re-export from core)
// ============================================================

type KnownAPI = core.KnownAPI
type KnownProvider = core.KnownProvider
type Modality = core.Modality
type ThinkingLevel = core.ThinkingLevel
type StopReason = core.StopReason
type CacheRetention = core.CacheRetention
type Transport = core.Transport
type Cost = core.Cost
type Model = core.Model
type Compat = core.Compat
type ContentBlock = core.ContentBlock
type TextContent = core.TextContent
type ThinkingContent = core.ThinkingContent
type ImageContent = core.ImageContent
type ToolCall = core.ToolCall
type Message = core.Message
type UserMessage = core.UserMessage
type AssistantMessage = core.AssistantMessage
type ToolResultMessage = core.ToolResultMessage
type Usage = core.Usage
type CostBreakdown = core.CostBreakdown
type Diagnostic = core.Diagnostic
type Context = core.Context
type Tool = core.Tool
type StreamOptions = core.StreamOptions
type SimpleStreamOptions = core.SimpleStreamOptions
type ImagesModel = core.ImagesModel
type AssistantImages = core.AssistantImages
type ImageData = core.ImageData
type ImageOptions = core.ImageOptions
type AssistantMessageEvent = core.AssistantMessageEvent
type AssistantMessageEventStream = core.AssistantMessageEventStream
type APIProvider = core.APIProvider
type ImagesAPIProvider = core.ImagesAPIProvider

// ============================================================
// Event type aliases
// ============================================================

type EventStart = core.EventStart
type EventTextStart = core.EventTextStart
type EventTextDelta = core.EventTextDelta
type EventTextEnd = core.EventTextEnd
type EventThinkingStart = core.EventThinkingStart
type EventThinkingDelta = core.EventThinkingDelta
type EventThinkingEnd = core.EventThinkingEnd
type EventToolCallStart = core.EventToolCallStart
type EventToolCallDelta = core.EventToolCallDelta
type EventToolCallEnd = core.EventToolCallEnd
type EventDone = core.EventDone
type EventError = core.EventError

// ============================================================
// Constants (re-export from core)
// ============================================================

const (
	APIOpenAICompletions    = core.APIOpenAICompletions
	APIAnthropicMessages    = core.APIAnthropicMessages
	APIBedrockConverse      = core.APIBedrockConverse
	APIOpenAIResponses      = core.APIOpenAIResponses
	APIAzureOpenAIResponses = core.APIAzureOpenAIResponses
	APIOpenAICodexResponses = core.APIOpenAICodexResponses
	APIGoogleGenerative     = core.APIGoogleGenerative
	APIGoogleVertex         = core.APIGoogleVertex
	APIMistralConversations = core.APIMistralConversations

	ProviderAnthropic     = core.ProviderAnthropic
	ProviderOpenAI        = core.ProviderOpenAI
	ProviderAmazonBedrock = core.ProviderAmazonBedrock
	ProviderGoogle        = core.ProviderGoogle
	ProviderGoogleVertex  = core.ProviderGoogleVertex
	ProviderMistral       = core.ProviderMistral
	ProviderAzureOpenAI   = core.ProviderAzureOpenAI
	ProviderOpenAICodex   = core.ProviderOpenAICodex
	ProviderGitHubCopilot = core.ProviderGitHubCopilot
	ProviderOpenRouter    = core.ProviderOpenRouter
	ProviderFireworks     = core.ProviderFireworks
	ProviderTogether      = core.ProviderTogether
	ProviderGroq          = core.ProviderGroq
	ProviderXAI           = core.ProviderXAI
	ProviderDeepSeek      = core.ProviderDeepSeek
	ProviderCerebras      = core.ProviderCerebras
	ProviderCloudflare    = core.ProviderCloudflare
	ProviderHuggingFace   = core.ProviderHuggingFace
	ProviderMoonshot      = core.ProviderMoonshot
	ProviderMoonshotCN    = core.ProviderMoonshotCN
	ProviderMinimax       = core.ProviderMinimax
	ProviderMinimaxCN     = core.ProviderMinimaxCN
	ProviderXiaomi        = core.ProviderXiaomi
	ProviderVercelGateway = core.ProviderVercelGateway
	ProviderCloudflareGW  = core.ProviderCloudflareGW

	ModalityText  = core.ModalityText
	ModalityImage = core.ModalityImage
	ModalityAudio = core.ModalityAudio

	ThinkingMinimal = core.ThinkingMinimal
	ThinkingLow     = core.ThinkingLow
	ThinkingMedium  = core.ThinkingMedium
	ThinkingHigh    = core.ThinkingHigh
	ThinkingXHigh   = core.ThinkingXHigh

	StopStop    = core.StopStop
	StopLength  = core.StopLength
	StopToolUse = core.StopToolUse
	StopError   = core.StopError
	StopAborted = core.StopAborted

	CacheNone  = core.CacheNone
	CacheShort = core.CacheShort
	CacheLong  = core.CacheLong

	TransportSSE       = core.TransportSSE
	TransportWebSocket = core.TransportWebSocket
	TransportAuto      = core.TransportAuto
)

// ============================================================
// Functions (delegate to core / ai)
// ============================================================

// --- Provider Registry (core) ---

func RegisterProvider(api KnownAPI, provider APIProvider, sourceID ...string) {
	core.RegisterProvider(api, provider, sourceID...)
}
func GetProvider(api KnownAPI) (APIProvider, error) {
	return core.GetProvider(api)
}
func GetRegisteredProviders() []KnownAPI {
	return core.GetRegisteredProviders()
}
func UnregisterProviders(sourceID string) {
	core.UnregisterProviders(sourceID)
}
func ClearProviders() {
	core.ClearProviders()
}
func RegisterImagesProvider(api KnownAPI, provider ImagesAPIProvider, sourceID ...string) {
	core.RegisterImagesProvider(api, provider, sourceID...)
}
func GetImagesProvider(api KnownAPI) (ImagesAPIProvider, error) {
	return core.GetImagesProvider(api)
}
func GetRegisteredImagesProviders() []KnownAPI {
	return core.GetRegisteredImagesProviders()
}
func UnregisterImagesProviders(sourceID string) {
	core.UnregisterImagesProviders(sourceID)
}
func ClearImagesProviders() {
	core.ClearImagesProviders()
}

// --- Model Registry (ai) ---

func LoadModels(models map[KnownProvider]map[string]Model) {
	llm.LoadModels(models)
}
func GetModel(provider KnownProvider, modelID string) (Model, error) {
	return llm.GetModel(provider, modelID)
}
func GetProviders() []KnownProvider {
	return llm.GetProviders()
}
func GetModels(provider KnownProvider) []Model {
	return llm.GetModels(provider)
}
func GetSupportedThinkingLevels(model Model) []ThinkingLevel {
	return llm.GetSupportedThinkingLevels(model)
}
func ClampThinkingLevel(model Model, level ThinkingLevel) ThinkingLevel {
	return llm.ClampThinkingLevel(model, level)
}
func ModelsAreEqual(a, b Model) bool {
	return llm.ModelsAreEqual(a, b)
}

// --- Image Model Registry (ai) ---

func LoadImageModels(models map[KnownProvider]map[string]ImagesModel) {
	llm.LoadImageModels(models)
}
func GetImageModel(provider KnownProvider, modelID string) (ImagesModel, error) {
	return llm.GetImageModel(provider, modelID)
}
func GetImageProviders() []KnownProvider {
	return llm.GetImageProviders()
}
func GetImageModels(provider KnownProvider) []ImagesModel {
	return llm.GetImageModels(provider)
}

// --- Public API (ai) ---

func Stream(ctx context.Context, model Model, msgs []Message, opts ...StreamOptions) (*core.AssistantMessageEventStream, error) {
	return llm.Stream(ctx, model, msgs, opts...)
}
func Complete(ctx context.Context, model Model, msgs []Message, opts ...StreamOptions) (AssistantMessage, error) {
	return llm.Complete(ctx, model, msgs, opts...)
}
func StreamSimple(ctx context.Context, model Model, msgs []Message, opts ...SimpleStreamOptions) (*core.AssistantMessageEventStream, error) {
	return llm.StreamSimple(ctx, model, msgs, opts...)
}
func StreamSimpleWithContext(ctx context.Context, model Model, llmCtx Context, opts ...SimpleStreamOptions) (*core.AssistantMessageEventStream, error) {
	return llm.StreamSimpleWithContext(ctx, model, llmCtx, opts...)
}
func CompleteSimple(ctx context.Context, model Model, msgs []Message, opts ...SimpleStreamOptions) (AssistantMessage, error) {
	return llm.CompleteSimple(ctx, model, msgs, opts...)
}
func GenerateImages(ctx context.Context, model ImagesModel, msgs []Message, opts ...ImageOptions) (AssistantImages, error) {
	return llm.GenerateImages(ctx, model, msgs, opts...)
}

// --- Utility Functions (core) ---

func NewEventStream[T any, R any]() *core.EventStream[T, R] {
	return core.NewEventStream[T, R]()
}
func CalculateCost(model Model, usage Usage) CostBreakdown {
	return core.CalculateCost(model, usage)
}
func ResolveAPIKey(provider KnownProvider, optsKey string) string {
	return core.ResolveAPIKey(provider, optsKey)
}
func ResolveBaseURL(model Model, defaultURL string) string {
	return core.ResolveBaseURL(model, defaultURL)
}
func GetEnvAPIKey(provider KnownProvider) string {
	return core.GetEnvAPIKey(provider)
}
func FindEnvKeys(provider KnownProvider) []string {
	return core.FindEnvKeys(provider)
}
