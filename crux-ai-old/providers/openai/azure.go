package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	core "github.com/hycjack/crux-ai/core"
)

// AzureOptions holds Azure-specific options.
type AzureOptions struct {
	ReasoningEffort    string `json:"reasoningEffort,omitempty"`
	ReasoningSummary   string `json:"reasoningSummary,omitempty"`
	AzureAPIVersion    string `json:"azureApiVersion,omitempty"`
	AzureResourceName  string `json:"azureResourceName,omitempty"`
	AzureBaseURL       string `json:"azureBaseUrl,omitempty"`
	AzureDeploymentName string `json:"azureDeploymentName,omitempty"`
}

// AzureProvider implements the Azure OpenAI Responses API.
type AzureProvider struct{}

// NewAzure creates a new Azure OpenAI provider.
func NewAzure() *AzureProvider {
	return &AzureProvider{}
}

func (p *AzureProvider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	return streamAzure(ctx, model, llmCtx, opts, AzureOptions{})
}

func (p *AzureProvider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	azureOpts := AzureOptions{}
	if opts.Reasoning != "" {
		azureOpts.ReasoningEffort = string(opts.Reasoning)
	}
	return streamAzure(ctx, model, llmCtx, opts.StreamOptions, azureOpts)
}

func streamAzure(ctx context.Context, model core.Model, c core.Context, opts core.StreamOptions, azureOpts AzureOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("azure: no API key provided")
	}

	// Resolve Azure-specific configuration
	baseURL := resolveAzureBaseURL(model, azureOpts)
	deploymentName := resolveAzureDeploymentName(model, azureOpts)
	apiVersion := azureOpts.AzureAPIVersion
	if apiVersion == "" {
		apiVersion = "2025-03-01-preview"
	}

	// Modify the model to use Azure-specific settings
	azureModel := model
	azureModel.BaseURL = fmt.Sprintf("%s/openai/deployments/%s", baseURL, deploymentName)
	azureModel.Headers = mergeMaps(model.Headers, map[string]string{
		"api-key": apiKey,
	})

	return streamResponses(ctx, azureModel, c, opts, ResponsesOptions{
		ReasoningEffort:  azureOpts.ReasoningEffort,
		ReasoningSummary: azureOpts.ReasoningSummary,
	})
}

func resolveAzureBaseURL(model core.Model, opts AzureOptions) string {
	if opts.AzureBaseURL != "" {
		return opts.AzureBaseURL
	}
	if model.BaseURL != "" {
		return model.BaseURL
	}
	if resourceName := opts.AzureResourceName; resourceName != "" {
		return fmt.Sprintf("https://%s.openai.azure.com", resourceName)
	}
	if resourceName := os.Getenv("AZURE_OPENAI_RESOURCE_NAME"); resourceName != "" {
		return fmt.Sprintf("https://%s.openai.azure.com", resourceName)
	}
	return ""
}

func resolveAzureDeploymentName(model core.Model, opts AzureOptions) string {
	if opts.AzureDeploymentName != "" {
		return opts.AzureDeploymentName
	}
	// Try environment variable mapping
	if mapping := os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP"); mapping != "" {
		// Parse JSON mapping: {"model-id": "deployment-name"}
		var m map[string]string
		if err := jsonUnmarshal(mapping, &m); err == nil {
			if deployment, ok := m[model.ID]; ok {
				return deployment
			}
		}
	}
	return model.ID
}

func jsonUnmarshal(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}

func mergeMaps(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}
