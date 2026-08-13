package google

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	core "github.com/hycjack/crux-ai/core"
)

// VertexOptions holds Google Vertex AI-specific options.
type VertexOptions struct {
	ToolChoice any             `json:"toolChoice,omitempty"`
	Thinking   *ThinkingConfig `json:"thinking,omitempty"`
	Project    string          `json:"project,omitempty"`
	Location   string          `json:"location,omitempty"`
}

// VertexProvider implements the Google Vertex AI API.
type VertexProvider struct{}

// NewVertex creates a new Google Vertex AI provider.
func NewVertex() *VertexProvider { return &VertexProvider{} }

func (p *VertexProvider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.AssistantMessageEventStream, error) {
	return streamVertex(ctx, model, llmCtx, opts, VertexOptions{})
}

func (p *VertexProvider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.AssistantMessageEventStream, error) {
	vertexOpts := VertexOptions{}
	if opts.Reasoning != "" {
		vertexOpts.Thinking = &ThinkingConfig{
			Enabled: true,
			Level:   mapThinkingLevel(opts.Reasoning),
		}
		if opts.ThinkingBudgets != nil {
			if budget, ok := opts.ThinkingBudgets[string(opts.Reasoning)]; ok {
				vertexOpts.Thinking.BudgetTokens = budget
			}
		}
	}
	return streamVertex(ctx, model, llmCtx, opts.StreamOptions, vertexOpts)
}

func streamVertex(ctx context.Context, model core.Model, c core.Context, opts core.StreamOptions, vertexOpts VertexOptions) (*core.AssistantMessageEventStream, error) {
	apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)

	project := vertexOpts.Project
	if project == "" {
		project = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if project == "" {
		return nil, fmt.Errorf("google-vertex: no project specified")
	}

	location := vertexOpts.Location
	if location == "" {
		location = os.Getenv("GOOGLE_CLOUD_LOCATION")
	}
	if location == "" {
		location = "us-central1"
	}

	body, err := buildGoogleBody(model, c, opts, Options{
		ToolChoice: vertexOpts.ToolChoice,
		Thinking:   vertexOpts.Thinking,
	})
	if err != nil {
		return nil, fmt.Errorf("google-vertex: failed to build request: %w", err)
	}
	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	c.Messages = core.TransformMessages(c.Messages, model, nil)

	ps := core.NewProviderEventStream()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ps.Error(fmt.Errorf("google-vertex: panic: %v", r))
			}
		}()
		baseURL := fmt.Sprintf("https://%s-aiplatform.googleapis.com", location)
		err := doVertexStream(ctx, baseURL, apiKey, project, location, model, body, ps, opts)
		if err != nil {
			ps.Error(err)
			return
		}
		ps.End(core.ProviderEventStreamResult{})
	}()

	out := core.CanonicalizeProviderStream(ps, model.API, model.Provider, model.ID)
	return out, nil
}

func doVertexStream(ctx context.Context, baseURL, apiKey, project, location string, model core.Model, body map[string]any, ps *core.ProviderEventStream, opts core.StreamOptions) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:streamGenerateContent?alt=sse",
		baseURL, project, location, model.ID)

	headers := map[string]string{"Content-Type": "application/json"}
	if apiKey != "" {
		headers["x-goog-api-key"] = apiKey
	}
	for k, v := range core.ProviderHeadersToRecord(core.MergeProviderHeaders(model.Headers, opts.Headers)) {
		headers[k] = v
	}

	client := core.NewTimeoutClient(opts.TimeoutMs)
	resp, err := core.DoWithRetry(ctx, client, "POST", url, bodyBytes, headers, model.Provider, opts)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return processGoogleSSE(resp.Body, ps, model, opts)
}
