// Package anthropic implements the Anthropic Messages API provider.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	core "github.com/hycjack/crux-ai/core"
)

const defaultBaseURL = "https://api.anthropic.com"

// Options holds Anthropic-specific options.
type Options struct {
	ThinkingEnabled      bool   `json:"thinkingEnabled,omitempty"`
	ThinkingBudgetTokens int    `json:"thinkingBudgetTokens,omitempty"`
	Effort               string `json:"effort,omitempty"`          // low, medium, high, xhigh, max
	ThinkingDisplay      string `json:"thinkingDisplay,omitempty"` // summarized, omitted
	InterleavedThinking  bool   `json:"interleavedThinking,omitempty"`
	ToolChoice           any    `json:"toolChoice,omitempty"`
}

// Provider implements the Anthropic Messages API.
type Provider struct{}

// New creates a new Anthropic provider.
func New() *Provider {
	return &Provider{}
}

func (p *Provider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	return streamAnthropic(ctx, model, llmCtx, opts, Options{})
}

func (p *Provider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	// Map reasoning level to Anthropic options
	anthropicOpts := Options{}
	if opts.Reasoning != "" {
		anthropicOpts.ThinkingEnabled = true
		anthropicOpts.Effort = string(opts.Reasoning)
	}
	return streamAnthropic(ctx, model, llmCtx, opts.StreamOptions, anthropicOpts)
}

func streamAnthropic(ctx context.Context, model core.Model, c core.Context, opts core.StreamOptions, anthropicOpts Options) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic: no API key provided")
	}

	baseURL := core.ResolveBaseURL(model, defaultBaseURL)

	// Build request body
	body, err := buildRequestBody(model, c, opts, anthropicOpts)
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to build request: %w", err)
	}

	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				stream.Error(fmt.Errorf("anthropic: panic: %v", r))
			}
		}()

		msg, err := doStream(ctx, baseURL, apiKey, model, body, stream, opts)
		if err != nil {
			stream.Error(err)
			return
		}
		stream.End(msg)
	}()

	return stream, nil
}

func buildRequestBody(model core.Model, c core.Context, opts core.StreamOptions, anthropicOpts Options) (map[string]any, error) {
	body := map[string]any{
		"model":      model.ID,
		"stream":     true,
		"max_tokens": 4096,
	}

	if opts.MaxTokens != nil && *opts.MaxTokens > 0 {
		body["max_tokens"] = *opts.MaxTokens
	} else if model.MaxTokens > 0 {
		body["max_tokens"] = model.MaxTokens
	}

	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}

	// System prompt
	if c.SystemPrompt != "" {
		body["system"] = c.SystemPrompt
	}

	// Messages
	messages, err := convertMessages(c.Messages)
	if err != nil {
		return nil, err
	}
	body["messages"] = messages

	// Tools
	if len(c.Tools) > 0 {
		body["tools"] = convertTools(c.Tools, anthropicOpts.InterleavedThinking)
	}

	// Thinking
	if anthropicOpts.ThinkingEnabled {
		thinking := map[string]any{
			"type": "enabled",
		}
		if anthropicOpts.ThinkingBudgetTokens > 0 {
			thinking["budget_tokens"] = anthropicOpts.ThinkingBudgetTokens
		}
		body["thinking"] = thinking
	}

	// Tool choice
	if anthropicOpts.ToolChoice != nil {
		body["tool_choice"] = anthropicOpts.ToolChoice
	}

	return body, nil
}

func convertMessages(messages []core.Message) ([]map[string]any, error) {
	var result []map[string]any

	for _, msg := range messages {
		switch m := msg.(type) {
		case core.UserMessage:
			content, err := convertUserContent(m.Content)
			if err != nil {
				return nil, err
			}
			result = append(result, map[string]any{
				"role":    "user",
				"content": content,
			})

		case core.AssistantMessage:
			content := convertAssistantContent(m.Content)
			result = append(result, map[string]any{
				"role":    "assistant",
				"content": content,
			})

		case core.ToolResultMessage:
			content := convertToolResultContent(m.Content)
			block := map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     content,
			}
			if m.IsError {
				block["is_error"] = true
			}
			result = append(result, map[string]any{
				"role":    "user",
				"content": []any{block},
			})
		}
	}

	return result, nil
}

func convertUserContent(content any) (any, error) {
	switch c := content.(type) {
	case string:
		return c, nil
	case []core.ContentBlock:
		var blocks []any
		for _, block := range c {
			switch b := block.(type) {
			case core.TextContent:
				blocks = append(blocks, map[string]any{
					"type": "text",
					"text": b.Text,
				})
			case core.ImageContent:
				blocks = append(blocks, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": b.MimeType,
						"data":       b.Data,
					},
				})
			}
		}
		return blocks, nil
	default:
		return fmt.Sprintf("%v", content), nil
	}
}

func convertAssistantContent(content []core.ContentBlock) []any {
	var blocks []any
	for _, block := range content {
		switch b := block.(type) {
		case core.TextContent:
			block := map[string]any{
				"type": "text",
				"text": b.Text,
			}
			if b.TextSignature != "" {
				block["signature"] = b.TextSignature
			}
			blocks = append(blocks, block)
		case core.ThinkingContent:
			block := map[string]any{
				"type":     "thinking",
				"thinking": b.Thinking,
			}
			if b.ThinkingSignature != "" {
				block["signature"] = b.ThinkingSignature
			}
			blocks = append(blocks, block)
		case core.ToolCall:
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    b.ID,
				"name":  b.Name,
				"input": json.RawMessage(b.Arguments),
			})
		}
	}
	return blocks
}

func convertToolResultContent(content []core.ContentBlock) any {
	var blocks []any
	for _, block := range content {
		switch b := block.(type) {
		case core.TextContent:
			blocks = append(blocks, map[string]any{
				"type": "text",
				"text": b.Text,
			})
		case core.ImageContent:
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": b.MimeType,
					"data":       b.Data,
				},
			})
		}
	}
	if len(blocks) == 1 {
		if textBlock, ok := blocks[0].(map[string]any); ok {
			if textBlock["type"] == "text" {
				return textBlock["text"]
			}
		}
	}
	return blocks
}

func convertTools(tools []core.Tool, eagerStreaming bool) []map[string]any {
	result := make([]map[string]any, len(tools))
	for i, tool := range tools {
		t := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
		}
		if len(tool.Parameters) > 0 {
			var params map[string]any
			if err := json.Unmarshal(tool.Parameters, &params); err == nil {
				t["input_schema"] = params
			}
		}
		if eagerStreaming {
			t["eager_input_streaming"] = true
		}
		result[i] = t
	}
	return result
}

func doStream(ctx context.Context, baseURL, apiKey string, model core.Model, body map[string]any, stream *core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], opts core.StreamOptions) (core.AssistantMessage, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return core.AssistantMessage{}, err
	}

	url := baseURL + "/v1/messages"

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return core.AssistantMessage{}, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	if anthropicOpts, ok := body["thinking"]; ok {
		if thinkingMap, ok := anthropicOpts.(map[string]any); ok && thinkingMap["type"] == "enabled" {
			req.Header.Set("anthropic-beta", "interleaved-thinking-2025-05-14")
		}
	}

	// Add custom headers
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	resp, err := core.SSEClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return core.AssistantMessage{}, core.WrapHTTPTimeout(core.ProviderAnthropic, 5*time.Minute, err)
		}
		return core.AssistantMessage{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		if classified := core.ClassifyHTTPError(model.Provider, resp.StatusCode, string(bodyBytes)); classified != nil {
			return core.AssistantMessage{}, classified
		}
		return core.AssistantMessage{}, fmt.Errorf("anthropic: API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return processSSEStream(resp.Body, stream, model, opts)
}

func processSSEStream(body io.Reader, stream *core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], model core.Model, opts core.StreamOptions) (core.AssistantMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		msg               core.AssistantMessage
		textBuf           strings.Builder
		thinkingBuf       strings.Builder
		textSignature     string
		thinkingSignature string
		toolCalls         map[int]*core.ToolCall
	)

	msg.API = model.API
	msg.Provider = model.Provider
	msg.Model = model.ID
	msg.Role = "assistant"
	msg.Timestamp = time.Now()
	toolCalls = make(map[int]*core.ToolCall)

	stream.Push(core.EventStart{
		Type:      "start",
		API:       model.API,
		Provider:  model.Provider,
		Model:     model.ID,
		Timestamp: time.Now(),
	})

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
		case "content_block_start":
			block, _ := event["content_block"].(map[string]any)
			blockType, _ := block["type"].(string)
			index, _ := event["index"].(float64)

			switch blockType {
			case "text":
				if sig, ok := block["signature"].(string); ok {
					textSignature = sig
				}
				stream.Push(core.EventTextStart{Type: "text_start"})
			case "thinking":
				if sig, ok := block["signature"].(string); ok {
					thinkingSignature = sig
				}
				stream.Push(core.EventThinkingStart{Type: "thinking_start"})
			case "tool_use":
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				toolCalls[int(index)] = &core.ToolCall{
					Type: "toolCall",
					ID:   id,
					Name: name,
				}
				stream.Push(core.EventToolCallStart{
					Type: "toolcall_start",
					ID:   id,
					Name: name,
				})
			}

		case "content_block_delta":
			delta, _ := event["delta"].(map[string]any)
			deltaType, _ := delta["type"].(string)
			index, _ := event["index"].(float64)

			switch deltaType {
			case "text_delta":
				text, _ := delta["text"].(string)
				textBuf.WriteString(text)
				stream.Push(core.EventTextDelta{
					Type:  "text_delta",
					Delta: text,
				})

			case "thinking_delta":
				thinking, _ := delta["thinking"].(string)
				thinkingBuf.WriteString(thinking)
				stream.Push(core.EventThinkingDelta{
					Type:  "thinking_delta",
					Delta: thinking,
				})

			case "input_json_delta":
				partial, _ := delta["partial_json"].(string)
				if tc, ok := toolCalls[int(index)]; ok {
					tc.Arguments = append(tc.Arguments, []byte(partial)...)
					stream.Push(core.EventToolCallDelta{
						Type:           "toolcall_delta",
						ID:             tc.ID,
						ArgumentsDelta: partial,
					})
				}
			}

		case "content_block_stop":
			index, _ := event["index"].(float64)
			if tc, ok := toolCalls[int(index)]; ok {
				stream.Push(core.EventToolCallEnd{
					Type:      "toolcall_end",
					ID:        tc.ID,
					Arguments: tc.Arguments,
				})
				msg.Content = append(msg.Content, *tc)
			}
			// Capture signature from content_block_stop if present
			if sig, ok := event["signature"].(string); ok {
				// Determine which block this signature belongs to based on current state
				if thinkingBuf.Len() > 0 && thinkingSignature == "" {
					thinkingSignature = sig
				} else if textBuf.Len() > 0 && textSignature == "" {
					textSignature = sig
				}
			}

		case "message_start":
			message, _ := event["message"].(map[string]any)
			if message != nil {
				if usage, ok := message["usage"].(map[string]any); ok {
					msg.Usage.Input = int(getFloat(usage, "input_tokens"))
					msg.Usage.Output = int(getFloat(usage, "output_tokens"))
					msg.Usage.CacheRead = int(getFloat(usage, "cache_read_input_tokens"))
					msg.Usage.CacheWrite = int(getFloat(usage, "cache_creation_input_tokens"))
				}
			}

		case "message_delta":
			delta, _ := event["delta"].(map[string]any)
			if stopReason, ok := delta["stop_reason"].(string); ok {
				msg.StopReason = mapStopReason(stopReason)
			}
			if usage, ok := event["usage"].(map[string]any); ok {
				msg.Usage.Output = int(getFloat(usage, "output_tokens"))
			}

		case "message_stop":
			// message_stop signals the end; finalization happens below.
		}
	}

	// Finalize (always runs, even if message_stop was not received)
	if textBuf.Len() > 0 {
		msg.Content = append(msg.Content, core.TextContent{
			Type:          "text",
			Text:          textBuf.String(),
			TextSignature: textSignature,
		})
		stream.Push(core.EventTextEnd{
			Type:          "text_end",
			TextSignature: textSignature,
		})
	}
	if thinkingBuf.Len() > 0 {
		msg.Content = append(msg.Content, core.ThinkingContent{
			Type:              "thinking",
			Thinking:          thinkingBuf.String(),
			ThinkingSignature: thinkingSignature,
		})
		stream.Push(core.EventThinkingEnd{
			Type:              "thinking_end",
			ThinkingSignature: thinkingSignature,
		})
	}

	msg.Usage.TotalTokens = msg.Usage.Input + msg.Usage.Output + msg.Usage.CacheRead + msg.Usage.CacheWrite
	msg.Usage.Cost = core.CalculateCost(model, msg.Usage)

	stream.Push(core.EventDone{
		Type:    "done",
		Message: msg,
	})

	if err := scanner.Err(); err != nil {
		return msg, fmt.Errorf("anthropic: SSE read error: %w", err)
	}

	return msg, nil
}

func mapStopReason(reason string) core.StopReason {
	switch reason {
	case "end_turn":
		return core.StopStop
	case "stop_sequence":
		return core.StopStop
	case "max_tokens":
		return core.StopLength
	case "tool_use":
		return core.StopToolUse
	default:
		return core.StopStop
	}
}

func getFloat(m map[string]any, key string) float64 {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}
