package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	core "crux-ai/core"
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

	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				stream.Error(fmt.Errorf("google-vertex: panic: %v", r))
			}
		}()
		baseURL := fmt.Sprintf("https://%s-aiplatform.googleapis.com", location)
		msg, err := doVertexStream(ctx, baseURL, apiKey, project, location, model, body, stream, opts)
		if err != nil {
			stream.Error(err)
			return
		}
		stream.End(msg)
	}()

	return stream, nil
}

func doVertexStream(ctx context.Context, baseURL, apiKey, project, location string, model core.Model, body map[string]any, stream *core.AssistantMessageEventStream, opts core.StreamOptions) (core.AssistantMessage, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return core.AssistantMessage{}, err
	}
	url := fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:streamGenerateContent?alt=sse",
		baseURL, project, location, model.ID)
	if apiKey != "" {
		url += "&key=" + apiKey
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return core.AssistantMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return core.AssistantMessage{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return core.AssistantMessage{}, fmt.Errorf("google-vertex: API error %d: %s", resp.StatusCode, string(errBody))
	}
	return processGoogleSSE(resp.Body, stream, model, opts)
}
