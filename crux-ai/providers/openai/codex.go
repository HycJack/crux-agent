package openai

import (
	"context"
	"fmt"

	core "github.com/hycjack/crux-ai/core"
)

// CodexOptions holds OpenAI Codex-specific options.
type CodexOptions struct {
	ReasoningEffort  string `json:"reasoningEffort,omitempty"`
	ReasoningSummary string `json:"reasoningSummary,omitempty"`
	ServiceTier      string `json:"serviceTier,omitempty"`
	TextVerbosity    string `json:"textVerbosity,omitempty"`
}

// CodexProvider implements the OpenAI Codex/ChatGPT Responses API.
type CodexProvider struct{}

// NewCodex creates a new OpenAI Codex provider.
func NewCodex() *CodexProvider { return &CodexProvider{} }

func (p *CodexProvider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.AssistantMessageEventStream, error) {
	return streamCodex(ctx, model, llmCtx, opts, CodexOptions{})
}

func (p *CodexProvider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.AssistantMessageEventStream, error) {
	codexOpts := CodexOptions{}
	if opts.Reasoning != "" {
		codexOpts.ReasoningEffort = string(clampEffort(opts.Reasoning))
	}
	return streamCodex(ctx, model, llmCtx, opts.StreamOptions, codexOpts)
}

func streamCodex(ctx context.Context, model core.Model, c core.Context, opts core.StreamOptions, codexOpts CodexOptions) (*core.AssistantMessageEventStream, error) {
	apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("openai-codex: no API key provided")
	}
	baseURL := core.ResolveBaseURL(model, "https://api.openai.com/v1")

	body, err := buildResponsesBody(model, c, opts, ResponsesOptions{
		ReasoningEffort:  codexOpts.ReasoningEffort,
		ReasoningSummary: codexOpts.ReasoningSummary,
		ServiceTier:      codexOpts.ServiceTier,
	})
	if err != nil {
		return nil, fmt.Errorf("openai-codex: failed to build request: %w", err)
	}

	if codexOpts.TextVerbosity != "" {
		body["text"] = map[string]any{"verbosity": codexOpts.TextVerbosity}
	}
	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				stream.Error(fmt.Errorf("openai-codex: panic: %v", r))
			}
		}()
		msg, err := doResponsesStream(ctx, baseURL, apiKey, model, body, stream, opts)
		if err != nil {
			stream.Error(err)
			return
		}
		stream.End(msg)
	}()

	return stream, nil
}
