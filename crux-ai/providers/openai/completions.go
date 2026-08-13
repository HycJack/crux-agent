package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hycjack/crux-ai/internal/conv"
	"github.com/hycjack/crux-ai/providers/compat"

	core "github.com/hycjack/crux-ai/core"
)

// CompletionsOptions holds OpenAI Completions-specific options.
type CompletionsOptions struct {
	ToolChoice      any    `json:"toolChoice,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

// CompletionsProvider implements the OpenAI Chat Completions API.
type CompletionsProvider struct{}

// NewCompletions creates a new OpenAI Completions provider.
func NewCompletions() *CompletionsProvider { return &CompletionsProvider{} }

// NewCompat returns an OpenAI direct config suitable for compat.Router.
//
// All OpenAI-protocol providers (OpenAI, Xiaomi, GLM, DeepSeek, Kimi, ...)
// share the same compat engine and are dispatched by model.Provider at
// request time. Register this alongside the third-party configs to cover
// OpenAI's own /v1/chat/completions endpoint.
func NewCompat() compat.Config {
	return compat.Config{
		Provider:       core.ProviderOpenAI,
		DefaultBaseURL: "https://api.openai.com/v1",
	}
}

func (p *CompletionsProvider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.AssistantMessageEventStream, error) {
	return streamCompletions(ctx, model, llmCtx, opts, CompletionsOptions{})
}

func (p *CompletionsProvider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.AssistantMessageEventStream, error) {
	completionsOpts := CompletionsOptions{}
	if opts.Reasoning != "" {
		completionsOpts.ReasoningEffort = string(clampEffort(opts.Reasoning))
	}
	return streamCompletions(ctx, model, llmCtx, opts.StreamOptions, completionsOpts)
}

func streamCompletions(ctx context.Context, model core.Model, c core.Context, opts core.StreamOptions, completionsOpts CompletionsOptions) (*core.AssistantMessageEventStream, error) {
	apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("openai: no API key provided")
	}
	baseURL := core.ResolveBaseURL(model, defaultCompletionsURL)

	c.Messages = core.TransformMessages(c.Messages, model, nil)

	body, err := buildCompletionsBody(model, c, opts, completionsOpts)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to build request: %w", err)
	}
	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	ps := core.NewProviderEventStream()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ps.Error(fmt.Errorf("openai: panic: %v", r))
			}
		}()
		err := doCompletionsStream(ctx, baseURL, apiKey, model, body, ps, opts)
		if err != nil {
			ps.Error(err)
			return
		}
		ps.End(core.ProviderEventStreamResult{})
	}()

	out := core.CanonicalizeProviderStream(ps, model.API, model.Provider, model.ID)
	return out, nil
}

func buildCompletionsBody(model core.Model, c core.Context, opts core.StreamOptions, completionsOpts CompletionsOptions) (map[string]any, error) {
	body := map[string]any{
		"model":  model.ID,
		"stream": true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	if opts.MaxTokens != nil && *opts.MaxTokens > 0 {
		body["max_tokens"] = *opts.MaxTokens
	} else if model.MaxTokens > 0 {
		body["max_tokens"] = model.MaxTokens
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}

	messages := []map[string]any{}
	if c.SystemPrompt != "" {
		messages = append(messages, map[string]any{"role": "system", "content": c.SystemPrompt})
	}
	msgs, err := ConvertMessages(c.Messages, model)
	if err != nil {
		return nil, err
	}
	messages = append(messages, msgs...)
	body["messages"] = messages

	if len(c.Tools) > 0 {
		body["tools"] = ConvertTools(c.Tools)
	}
	if completionsOpts.ToolChoice != nil {
		body["tool_choice"] = completionsOpts.ToolChoice
	}
	if completionsOpts.ReasoningEffort != "" {
		body["reasoning_effort"] = completionsOpts.ReasoningEffort
	}
	return body, nil
}

func doCompletionsStream(ctx context.Context, baseURL, apiKey string, model core.Model, body map[string]any, ps *core.ProviderEventStream, opts core.StreamOptions) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := baseURL + "/chat/completions"

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + apiKey,
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

	return processCompletionsSSE(resp.Body, ps, model, opts)
}

func processCompletionsSSE(body io.Reader, ps *core.ProviderEventStream, model core.Model, opts core.StreamOptions) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		textBuf     strings.Builder
		textOpen    bool
		thinkingBuf strings.Builder
		thinkOpen   bool
		toolCalls   map[int]*core.ToolCall
		toolIndices []int
		usage       core.Usage
		stopReason  core.StopReason
	)
	toolCalls = make(map[int]*core.ToolCall)

	ps.Push(core.ProviderResponseStart{Type: "response_start", Model: model.ID, Timestamp: time.Now()})

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		if opts.OnResponse != nil {
			opts.OnResponse(data)
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		choices, ok := chunk["choices"].([]any)
		if !ok || len(choices) == 0 {
			continue
		}
		choice, ok := choices[0].(map[string]any)
		if !ok {
			continue
		}

		// Track finish_reason as stop reason
		if finishReason, ok := choice["finish_reason"].(string); ok && finishReason != "" {
			stopReason = MapStopReason(finishReason)
		}

		// Parse usage from the last chunk (when stream_options.include_usage is set).
		if u, ok := chunk["usage"].(map[string]any); ok {
			usage.Input = conv.GetInt(u, "prompt_tokens")
			usage.Output = conv.GetInt(u, "completion_tokens")
			usage.TotalTokens = conv.GetInt(u, "total_tokens")
			if details, ok := u["prompt_tokens_details"].(map[string]any); ok {
				usage.CacheRead = conv.GetInt(details, "cached_tokens")
			}
		}

		delta, ok := choice["delta"].(map[string]any)
		if !ok {
			continue
		}

		if content, ok := delta["content"].(string); ok && content != "" {
			if !textOpen {
				textOpen = true
			}
			textBuf.WriteString(content)
			ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: content})
		}
		if reasoning, ok := delta["reasoning_content"].(string); ok && reasoning != "" {
			if !thinkOpen {
				thinkOpen = true
			}
			thinkingBuf.WriteString(reasoning)
			ps.Push(core.ProviderThinkingDelta{Type: "thinking_delta", Delta: reasoning})
		}
		if calls, ok := delta["tool_calls"].([]any); ok {
			for _, call := range calls {
				c, ok := call.(map[string]any)
				if !ok {
					continue
				}
				index := conv.GetInt(c, "index")
				id, _ := c["id"].(string)
				function, _ := c["function"].(map[string]any)
				name, _ := function["name"].(string)
				args, _ := function["arguments"].(string)

				if id != "" {
					tc := &core.ToolCall{Type: "toolCall", ID: id, Name: name}
					if args != "" {
						tc.Arguments = []byte(args)
					}
					toolCalls[index] = tc
					toolIndices = append(toolIndices, index)
				}
				if tc, ok := toolCalls[index]; ok && args != "" && id == "" {
					tc.Arguments = append(tc.Arguments, []byte(args)...)
				}
			}
		}

		// When finish_reason is set, close all active blocks
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" && fr != "null" {
			if textOpen {
				ps.Push(core.ProviderContentBlockStop{Type: "content_block_stop"})
				textOpen = false
			}
			if thinkOpen {
				ps.Push(core.ProviderContentBlockStop{Type: "content_block_stop"})
				thinkOpen = false
			}
			// Emit tool calls (OpenAI sends full tool calls before finish_reason)
			for _, index := range toolIndices {
				if tc, ok := toolCalls[index]; ok {
					ps.Push(core.ProviderToolCall{
						Type: "tool_call", ID: tc.ID, Name: tc.Name,
						Arguments: tc.Arguments,
					})
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("openai: SSE read error: %w", err)
	}

	usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead
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

func clampEffort(effort core.ThinkingLevel) core.ThinkingLevel {
	if effort == core.ThinkingXHigh {
		return core.ThinkingHigh
	}
	return effort
}
