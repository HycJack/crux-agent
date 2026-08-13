package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/internal/conv"
)

// ResponsesOptions holds OpenAI Responses-specific options.
type ResponsesOptions struct {
	ReasoningEffort  string `json:"reasoningEffort,omitempty"`
	ReasoningSummary string `json:"reasoningSummary,omitempty"`
	ServiceTier      string `json:"serviceTier,omitempty"`
}

// ResponsesProvider implements the OpenAI Responses API.
type ResponsesProvider struct{}

// NewResponses creates a new OpenAI Responses provider.
func NewResponses() *ResponsesProvider { return &ResponsesProvider{} }

func (p *ResponsesProvider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.AssistantMessageEventStream, error) {
	return streamResponses(ctx, model, llmCtx, opts, ResponsesOptions{})
}

func (p *ResponsesProvider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.AssistantMessageEventStream, error) {
	responsesOpts := ResponsesOptions{}
	if opts.Reasoning != "" {
		responsesOpts.ReasoningEffort = string(clampEffort(opts.Reasoning))
	}
	return streamResponses(ctx, model, llmCtx, opts.StreamOptions, responsesOpts)
}

func streamResponses(ctx context.Context, model core.Model, c core.Context, opts core.StreamOptions, responsesOpts ResponsesOptions) (*core.AssistantMessageEventStream, error) {
	apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("openai-responses: no API key provided")
	}
	baseURL := model.BaseURL
	if baseURL == "" {
		baseURL = defaultResponsesURL
	}

	c.Messages = core.TransformMessages(c.Messages, model, nil)

	body, err := buildResponsesBody(model, c, opts, responsesOpts)
	if err != nil {
		return nil, fmt.Errorf("openai-responses: failed to build request: %w", err)
	}
	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	ps := core.NewProviderEventStream()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ps.Error(fmt.Errorf("openai-responses: panic: %v", r))
			}
		}()
		err := doResponsesStream(ctx, baseURL, apiKey, model, body, ps, opts)
		if err != nil {
			ps.Error(err)
			return
		}
		ps.End(core.ProviderEventStreamResult{})
	}()

	out := core.CanonicalizeProviderStream(ps, model.API, model.Provider, model.ID)
	return out, nil
}

func buildResponsesBody(model core.Model, c core.Context, opts core.StreamOptions, responsesOpts ResponsesOptions) (map[string]any, error) {
	body := map[string]any{
		"model":  model.ID,
		"stream": true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	if opts.MaxTokens != nil && *opts.MaxTokens > 0 {
		body["max_output_tokens"] = *opts.MaxTokens
	} else if model.MaxTokens > 0 {
		body["max_output_tokens"] = model.MaxTokens
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}

	input := []map[string]any{}
	if c.SystemPrompt != "" {
		input = append(input, map[string]any{"role": "system", "content": c.SystemPrompt})
	}
	msgs, err := convertResponsesMessages(c.Messages)
	if err != nil {
		return nil, err
	}
	input = append(input, msgs...)
	body["input"] = input

	if len(c.Tools) > 0 {
		body["tools"] = convertResponsesTools(c.Tools)
	}
	if responsesOpts.ReasoningEffort != "" {
		reasoning := map[string]any{"effort": responsesOpts.ReasoningEffort}
		if responsesOpts.ReasoningSummary != "" {
			reasoning["summary"] = responsesOpts.ReasoningSummary
		}
		body["reasoning"] = reasoning
	}
	if responsesOpts.ServiceTier != "" {
		body["service_tier"] = responsesOpts.ServiceTier
	}
	if opts.PromptCacheKey != nil {
		body["prompt_cache_key"] = core.ClampOpenAIPromptCacheKey(opts.PromptCacheKey)
	}
	return body, nil
}

func convertResponsesMessages(messages []core.Message) ([]map[string]any, error) {
	var result []map[string]any
	for _, msg := range messages {
		switch m := msg.(type) {
		case core.UserMessage:
			content, err := convertUserContent(m.Content)
			if err != nil {
				return nil, err
			}
			result = append(result, map[string]any{"role": "user", "content": content})
		case core.AssistantMessage:
			// Merge all text/thinking blocks into a single message item.
			// Tool calls stay as separate function_call items.
			var textContent []any
			for _, block := range m.Content {
				if text, ok := block.(core.TextContent); ok {
					textContent = append(textContent, map[string]any{"type": "output_text", "text": text.Text})
				}
			}
			if len(textContent) > 0 {
				result = append(result, map[string]any{
					"type": "message", "role": "assistant",
					"content": textContent,
				})
			}
			for _, block := range m.Content {
				if tc, ok := block.(core.ToolCall); ok {
					result = append(result, map[string]any{
						"type": "function_call", "id": tc.ID, "name": tc.Name,
						"arguments": string(tc.Arguments), "call_id": tc.ID,
					})
				}
			}
		case core.ToolResultMessage:
			content := convertToolResultContent(m.Content)
			result = append(result, map[string]any{
				"type": "function_call_output", "call_id": m.ToolCallID, "output": content,
			})
		}
	}
	return result, nil
}

func convertResponsesTools(tools []core.Tool) []map[string]any {
	result := make([]map[string]any, len(tools))
	for i, tool := range tools {
		t := map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
		}
		if len(tool.Parameters) > 0 {
			var params map[string]any
			if err := json.Unmarshal(tool.Parameters, &params); err == nil {
				t["parameters"] = params
			}
		}
		result[i] = t
	}
	return result
}

func doResponsesStream(ctx context.Context, baseURL, apiKey string, model core.Model, body map[string]any, ps *core.ProviderEventStream, opts core.StreamOptions) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := baseURL + "/responses"

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

	return processResponsesSSE(resp.Body, ps, model, opts)
}

func processResponsesSSE(body io.Reader, ps *core.ProviderEventStream, model core.Model, opts core.StreamOptions) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		textBuf    strings.Builder
		textOpen   bool
		thinkBufs  map[int]*strings.Builder
		thinkOpen  map[int]bool
		toolCalls  map[string]*core.ToolCall
		usage      core.Usage
		stopReason core.StopReason
	)
	toolCalls = make(map[string]*core.ToolCall)
	thinkBufs = make(map[int]*strings.Builder)
	thinkOpen = make(map[int]bool)

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
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		eventType, _ := event["type"].(string)

		switch eventType {
		case "response.created":
			response, _ := event["response"].(map[string]any)
			if response != nil {
				if u, ok := response["usage"].(map[string]any); ok {
					usage.Input = conv.GetInt(u, "input_tokens")
					usage.Output = conv.GetInt(u, "output_tokens")
				}
			}
		case "response.output_item.added":
			item, _ := event["item"].(map[string]any)
			if item == nil {
				continue
			}
			itemType, _ := item["type"].(string)
			switch itemType {
			case "function_call":
				id, _ := item["id"].(string)
				name, _ := item["name"].(string)
				callID, _ := item["call_id"].(string)
				tc := &core.ToolCall{Type: "toolCall", ID: id, Name: name}
				if args, ok := item["arguments"].(string); ok && args != "" {
					tc.Arguments = []byte(args)
				}
				toolCalls[callID] = tc
			case "reasoning":
				idx := len(thinkBufs)
				thinkBufs[idx] = &strings.Builder{}
				thinkOpen[idx] = true
			}
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			idx := -1
			if _, ok := event["output_index"]; ok {
				idx = conv.GetInt(event, "output_index")
			}
			if idx < 0 {
				for k := range thinkBufs {
					idx = k
					break
				}
			}
			delta, _ := event["delta"].(string)
			if delta == "" {
				continue
			}
			buf, ok := thinkBufs[idx]
			if !ok {
				idx = len(thinkBufs)
				buf = &strings.Builder{}
				thinkBufs[idx] = buf
				thinkOpen[idx] = false
			}
			buf.WriteString(delta)
			ps.Push(core.ProviderThinkingDelta{Type: "thinking_delta", Delta: delta})
		case "response.reasoning_text.done", "response.reasoning_summary_text.done", "response.output_item.done":
			idx := -1
			if _, ok := event["output_index"]; ok {
				idx = conv.GetInt(event, "output_index")
			}
			if idx < 0 {
				for k, open := range thinkOpen {
					if open {
						idx = k
						break
					}
				}
			}
			if idx >= 0 && thinkOpen[idx] {
				ps.Push(core.ProviderContentBlockStop{
					Type: "content_block_stop",
				})
				thinkOpen[idx] = false
			}
		case "response.output_text.delta":
			delta, _ := event["delta"].(string)
			if delta != "" {
				if !textOpen {
					textOpen = true
				}
				textBuf.WriteString(delta)
				ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: delta})
			}
		case "response.function_call_arguments.delta":
			callID, _ := event["call_id"].(string)
			delta, _ := event["delta"].(string)
			if tc, ok := toolCalls[callID]; ok && delta != "" {
				tc.Arguments = append(tc.Arguments, []byte(delta)...)
			}
		case "response.function_call_arguments.done":
			callID, _ := event["call_id"].(string)
			if tc, ok := toolCalls[callID]; ok {
				ps.Push(core.ProviderToolCall{
					Type: "tool_call", ID: tc.ID, Name: tc.Name,
					Arguments: tc.Arguments,
				})
			}
		case "response.completed":
			response, _ := event["response"].(map[string]any)
			if response != nil {
				if u, ok := response["usage"].(map[string]any); ok {
					usage.Input = conv.GetInt(u, "input_tokens")
					usage.Output = conv.GetInt(u, "output_tokens")
					if cached, ok := u["input_tokens_details"].(map[string]any); ok {
						usage.CacheRead = conv.GetInt(cached, "cached_tokens")
					}
				}
				if status, ok := response["status"].(string); ok {
					stopReason = mapResponseStatus(status)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("openai-responses: SSE read error: %w", err)
	}

	usage.TotalTokens = usage.Input + usage.Output
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

func mapResponseStatus(status string) core.StopReason {
	switch status {
	case "completed":
		return core.StopStop
	case "incomplete":
		return core.StopLength
	case "failed":
		return core.StopError
	case "cancelled":
		return core.StopAborted
	default:
		return core.StopStop
	}
}
