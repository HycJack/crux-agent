package google

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hycjack/crux-ai/internal/conv"

	core "github.com/hycjack/crux-ai/core"
)

// Options holds Google-specific options.
type Options struct {
	ToolChoice any             `json:"toolChoice,omitempty"`
	Thinking   *ThinkingConfig `json:"thinking,omitempty"`
}

// ThinkingConfig configures thinking/reasoning.
type ThinkingConfig struct {
	Enabled      bool   `json:"enabled"`
	BudgetTokens int    `json:"budgetTokens,omitempty"`
	Level        string `json:"level,omitempty"`
}

// Provider implements the Google Generative AI API.
type Provider struct{}

// New creates a new Google provider.
func New() *Provider { return &Provider{} }

func (p *Provider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.AssistantMessageEventStream, error) {
	return streamGoogle(ctx, model, llmCtx, opts, Options{})
}

func (p *Provider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.AssistantMessageEventStream, error) {
	googleOpts := Options{}
	if opts.Reasoning != "" {
		googleOpts.Thinking = &ThinkingConfig{
			Enabled: true,
			Level:   mapThinkingLevel(opts.Reasoning),
		}
		if opts.ThinkingBudgets != nil {
			if budget, ok := opts.ThinkingBudgets[string(opts.Reasoning)]; ok {
				googleOpts.Thinking.BudgetTokens = budget
			}
		}
	}
	return streamGoogle(ctx, model, llmCtx, opts.StreamOptions, googleOpts)
}

func mapThinkingLevel(level core.ThinkingLevel) string {
	switch level {
	case core.ThinkingMinimal:
		return "MINIMAL"
	case core.ThinkingLow:
		return "LOW"
	case core.ThinkingMedium:
		return "MEDIUM"
	case core.ThinkingHigh, core.ThinkingXHigh:
		return "HIGH"
	default:
		return "MEDIUM"
	}
}

func streamGoogle(ctx context.Context, model core.Model, c core.Context, opts core.StreamOptions, googleOpts Options) (*core.AssistantMessageEventStream, error) {
	apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("google: no API key provided")
	}
	baseURL := core.ResolveBaseURL(model, defaultBaseURL)

	c.Messages = core.TransformMessages(c.Messages, model, nil)

	body, err := buildGoogleBody(model, c, opts, googleOpts)
	if err != nil {
		return nil, fmt.Errorf("google: failed to build request: %w", err)
	}
	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	ps := core.NewProviderEventStream()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ps.Error(fmt.Errorf("google: panic: %v", r))
			}
		}()
		err := doGoogleStream(ctx, baseURL, apiKey, model, body, ps, opts)
		if err != nil {
			ps.Error(err)
			return
		}
		ps.End(core.ProviderEventStreamResult{})
	}()

	out := core.CanonicalizeProviderStream(ps, model.API, model.Provider, model.ID)
	return out, nil
}

func buildGoogleBody(model core.Model, c core.Context, opts core.StreamOptions, googleOpts Options) (map[string]any, error) {
	body := map[string]any{}
	contents, err := ConvertMessages(c.Messages)
	if err != nil {
		return nil, err
	}
	body["contents"] = contents
	if c.SystemPrompt != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []any{map[string]any{"text": c.SystemPrompt}},
		}
	}
	genConfig := map[string]any{}
	if opts.MaxTokens != nil && *opts.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = *opts.MaxTokens
	} else if model.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = model.MaxTokens
	}
	if opts.Temperature != nil {
		genConfig["temperature"] = *opts.Temperature
	}
	if len(genConfig) > 0 {
		body["generationConfig"] = genConfig
	}
	if len(c.Tools) > 0 {
		body["tools"] = ConvertTools(c.Tools)
	}
	if googleOpts.ToolChoice != nil {
		body["toolConfig"] = map[string]any{"functionCallingConfig": googleOpts.ToolChoice}
	}
	if googleOpts.Thinking != nil {
		thinkingConfig := map[string]any{"includeThoughts": googleOpts.Thinking.Enabled}
		if googleOpts.Thinking.BudgetTokens > 0 {
			thinkingConfig["thinkingBudget"] = googleOpts.Thinking.BudgetTokens
		} else if googleOpts.Thinking.Level != "" {
			thinkingConfig["thinkingLevel"] = googleOpts.Thinking.Level
		}
		body["thinkingConfig"] = thinkingConfig
	}
	return body, nil
}

func doGoogleStream(ctx context.Context, baseURL, apiKey string, model core.Model, body map[string]any, ps *core.ProviderEventStream, opts core.StreamOptions) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", baseURL, model.ID)

	headers := map[string]string{
		"Content-Type":   "application/json",
		"x-goog-api-key": apiKey,
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

func processGoogleSSE(body io.Reader, ps *core.ProviderEventStream, model core.Model, opts core.StreamOptions) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		textBuf      strings.Builder
		thinkBuf     strings.Builder
		toolCalls    []core.ToolCall
		textOpen     bool
		thinkingOpen bool
		usage        core.Usage
		stopReason   core.StopReason
	)

	ps.Push(core.ProviderResponseStart{Type: "response_start", Model: model.ID, Timestamp: time.Now()})

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if opts.OnResponse != nil {
			opts.OnResponse(data)
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		candidates, ok := chunk["candidates"].([]any)
		if !ok || len(candidates) == 0 {
			continue
		}
		candidate, ok := candidates[0].(map[string]any)
		if !ok {
			continue
		}
		if finishReason, ok := candidate["finishReason"].(string); ok {
			stopReason = MapStopReason(finishReason)
		}
		content, ok := candidate["content"].(map[string]any)
		if !ok {
			continue
		}
		parts, ok := content["parts"].([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if IsThinkingPart(p) {
				if text, ok := p["text"].(string); ok {
					if !thinkingOpen {
						thinkingOpen = true
					}
					thinkBuf.WriteString(text)
					ps.Push(core.ProviderThinkingDelta{Type: "thinking_delta", Delta: text})
				}
			} else if text, ok := p["text"].(string); ok {
				if !textOpen {
					textOpen = true
				}
				textBuf.WriteString(text)
				ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: text})
			} else if fc, ok := p["functionCall"].(map[string]any); ok {
				name, _ := fc["name"].(string)
				args, _ := fc["args"].(map[string]any)
				argsBytes, _ := json.Marshal(args)
				id := fmt.Sprintf("call_%d", len(toolCalls))
				tc := core.ToolCall{Type: "toolCall", ID: id, Name: name, Arguments: argsBytes}
				toolCalls = append(toolCalls, tc)
				ps.Push(core.ProviderToolCall{
					Type: "tool_call", ID: id, Name: name,
					Arguments: argsBytes,
				})
			}
		}
		if usageMetadata, ok := chunk["usageMetadata"].(map[string]any); ok {
			usage.Input = conv.GetInt(usageMetadata, "promptTokenCount")
			usage.Output = conv.GetInt(usageMetadata, "candidatesTokenCount")
			usage.TotalTokens = conv.GetInt(usageMetadata, "totalTokenCount")
		}

		// When finishReason is set, close active blocks
		if fr, ok := candidate["finishReason"].(string); ok && fr != "" {
			if textOpen {
				ps.Push(core.ProviderContentBlockStop{Type: "content_block_stop"})
				textOpen = false
			}
			if thinkingOpen {
				ps.Push(core.ProviderContentBlockStop{Type: "content_block_stop"})
				thinkingOpen = false
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("google: SSE read error: %w", err)
	}

	usage.Cost = core.CalculateCost(model, usage)

	final := core.AssistantMessage{
		Role: "assistant", API: model.API, Provider: model.Provider, Model: model.ID,
		Usage: usage, StopReason: stopReason, Timestamp: time.Now(),
	}

	ps.Push(core.ProviderResponseEnd{
		Type: "response_end", Message: final,
		FinishReason: string(stopReason),
	})

	return nil
}
