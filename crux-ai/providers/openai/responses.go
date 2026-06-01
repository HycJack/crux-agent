package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	core "crux-ai/core"
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

	body, err := buildResponsesBody(model, c, opts, responsesOpts)
	if err != nil {
		return nil, fmt.Errorf("openai-responses: failed to build request: %w", err)
	}
	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				stream.Error(fmt.Errorf("openai-responses: panic: %v", r))
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

func buildResponsesBody(model core.Model, c core.Context, opts core.StreamOptions, responsesOpts ResponsesOptions) (map[string]any, error) {
	body := map[string]any{
		"model":  model.ID,
		"stream": true,
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
			for _, block := range m.Content {
				switch b := block.(type) {
				case core.TextContent:
					result = append(result, map[string]any{
						"type": "message", "role": "assistant",
						"content": []any{map[string]any{"type": "output_text", "text": b.Text}},
					})
				case core.ToolCall:
					result = append(result, map[string]any{
						"type": "function_call", "id": b.ID, "name": b.Name,
						"arguments": string(b.Arguments), "call_id": b.ID,
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

func doResponsesStream(ctx context.Context, baseURL, apiKey string, model core.Model, body map[string]any, stream *core.AssistantMessageEventStream, opts core.StreamOptions) (core.AssistantMessage, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return core.AssistantMessage{}, err
	}
	url := baseURL + "/responses"

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return core.AssistantMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	client := core.NewTimeoutClient(opts.TimeoutMs)
	resp, err := client.Do(req)
	if err != nil {
		return core.AssistantMessage{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return core.AssistantMessage{}, fmt.Errorf("openai-responses: API error %d: %s", resp.StatusCode, string(bodyBytes))
	}
	return processResponsesSSE(resp.Body, stream, model, opts)
}

func processResponsesSSE(body io.Reader, stream *core.AssistantMessageEventStream, model core.Model, opts core.StreamOptions) (core.AssistantMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		msg       core.AssistantMessage
		textBuf   strings.Builder
		toolCalls map[string]*core.ToolCall
	)
	msg.API = model.API
	msg.Provider = model.Provider
	msg.Model = model.ID
	msg.Role = "assistant"
	msg.Timestamp = time.Now()
	toolCalls = make(map[string]*core.ToolCall)

	stream.Push(core.EventStart{Type: "start", API: model.API, Provider: model.Provider, Model: model.ID, Timestamp: time.Now()})

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
				if usage, ok := response["usage"].(map[string]any); ok {
					msg.Usage.Input = int(getFloat(usage, "input_tokens"))
					msg.Usage.Output = int(getFloat(usage, "output_tokens"))
				}
			}
		case "response.output_item.added":
			item, _ := event["item"].(map[string]any)
			if item != nil {
				if itemType, _ := item["type"].(string); itemType == "function_call" {
					id, _ := item["id"].(string)
					name, _ := item["name"].(string)
					callID, _ := item["call_id"].(string)
					toolCalls[callID] = &core.ToolCall{Type: "toolCall", ID: id, Name: name}
					stream.Push(core.EventToolCallStart{Type: "toolcall_start", ID: id, Name: name})
				}
			}
		case "response.output_text.delta":
			delta, _ := event["delta"].(string)
			if delta != "" {
				textBuf.WriteString(delta)
				stream.Push(core.EventTextDelta{Type: "text_delta", Delta: delta})
			}
		case "response.function_call_arguments.delta":
			callID, _ := event["call_id"].(string)
			delta, _ := event["delta"].(string)
			if tc, ok := toolCalls[callID]; ok && delta != "" {
				tc.Arguments = append(tc.Arguments, []byte(delta)...)
				stream.Push(core.EventToolCallDelta{Type: "toolcall_delta", ID: tc.ID, ArgumentsDelta: delta})
			}
		case "response.function_call_arguments.done":
			callID, _ := event["call_id"].(string)
			if tc, ok := toolCalls[callID]; ok {
				stream.Push(core.EventToolCallEnd{Type: "toolcall_end", ID: tc.ID, Arguments: tc.Arguments})
				msg.Content = append(msg.Content, *tc)
			}
		case "response.completed":
			response, _ := event["response"].(map[string]any)
			if response != nil {
				if usage, ok := response["usage"].(map[string]any); ok {
					msg.Usage.Input = int(getFloat(usage, "input_tokens"))
					msg.Usage.Output = int(getFloat(usage, "output_tokens"))
				}
				if status, ok := response["status"].(string); ok {
					msg.StopReason = mapResponseStatus(status)
				}
			}
		}
	}

	if textBuf.Len() > 0 {
		msg.Content = append(msg.Content, core.TextContent{Type: "text", Text: textBuf.String()})
		stream.Push(core.EventTextEnd{Type: "text_end"})
	}

	msg.Usage.TotalTokens = msg.Usage.Input + msg.Usage.Output
	msg.Usage.Cost = core.CalculateCost(model, msg.Usage)
	stream.Push(core.EventDone{Type: "done", Message: msg})

	if err := scanner.Err(); err != nil {
		return msg, fmt.Errorf("openai-responses: SSE read error: %w", err)
	}
	return msg, nil
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
