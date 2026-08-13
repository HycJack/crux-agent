// Package anthropic implements the Anthropic Messages API provider.
package anthropic

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

const defaultBaseURL = "https://api.anthropic.com"

// Options holds Anthropic-specific options.
type Options struct {
	ThinkingEnabled      bool   `json:"thinkingEnabled,omitempty"`
	ThinkingBudgetTokens int    `json:"thinkingBudgetTokens,omitempty"`
	Effort               string `json:"effort,omitempty"`
	ThinkingDisplay      string `json:"thinkingDisplay,omitempty"`
	InterleavedThinking  bool   `json:"interleavedThinking,omitempty"`
	ToolChoice           any    `json:"toolChoice,omitempty"`
}

// Provider implements the Anthropic Messages API.
type Provider struct{}

// New creates a new Anthropic provider.
func New() *Provider { return &Provider{} }

func (p *Provider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.AssistantMessageEventStream, error) {
	return streamAnthropic(ctx, model, llmCtx, opts, Options{})
}

func (p *Provider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.AssistantMessageEventStream, error) {
	anthropicOpts := Options{}
	if opts.Reasoning != "" {
		anthropicOpts.ThinkingEnabled = true
		anthropicOpts.Effort = string(opts.Reasoning)
	}
	return streamAnthropic(ctx, model, llmCtx, opts.StreamOptions, anthropicOpts)
}

func streamAnthropic(ctx context.Context, model core.Model, c core.Context, opts core.StreamOptions, anthropicOpts Options) (*core.AssistantMessageEventStream, error) {
	apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic: no API key provided")
	}
	baseURL := core.ResolveBaseURL(model, defaultBaseURL)

	// Apply TransformMessages before building request.
	c.Messages = core.TransformMessages(c.Messages, model, nil)

	body, err := buildRequestBody(model, c, opts, anthropicOpts)
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to build request: %w", err)
	}
	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	ps := core.NewProviderEventStream()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ps.Error(fmt.Errorf("anthropic: panic: %v", r))
			}
		}()
		err := doStreamAnthropic(ctx, baseURL, apiKey, model, body, ps, opts)
		if err != nil {
			ps.Error(err)
			return
		}
		ps.End(core.ProviderEventStreamResult{})
	}()

	out := core.CanonicalizeProviderStream(ps, model.API, model.Provider, model.ID)
	return out, nil
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
	if c.SystemPrompt != "" {
		body["system"] = c.SystemPrompt
	}
	messages, err := convertMessages(c.Messages)
	if err != nil {
		return nil, err
	}
	body["messages"] = messages
	if len(c.Tools) > 0 {
		body["tools"] = convertTools(c.Tools, anthropicOpts.InterleavedThinking)
	}
	if anthropicOpts.ThinkingEnabled {
		thinking := map[string]any{"type": "enabled"}
		if anthropicOpts.ThinkingBudgetTokens > 0 {
			thinking["budget_tokens"] = anthropicOpts.ThinkingBudgetTokens
		}
		body["thinking"] = thinking
	}
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
			result = append(result, map[string]any{"role": "user", "content": content})
		case core.AssistantMessage:
			content := convertAssistantContent(m.Content)
			result = append(result, map[string]any{"role": "assistant", "content": content})
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
			result = append(result, map[string]any{"role": "user", "content": []any{block}})
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
				blocks = append(blocks, map[string]any{"type": "text", "text": b.Text})
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
			blk := map[string]any{"type": "text", "text": b.Text}
			if b.TextSignature != "" {
				blk["signature"] = b.TextSignature
			}
			blocks = append(blocks, blk)
		case core.ThinkingContent:
			blk := map[string]any{"type": "thinking", "thinking": b.Thinking}
			if b.ThinkingSignature != "" {
				blk["signature"] = b.ThinkingSignature
			}
			blocks = append(blocks, blk)
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
			blocks = append(blocks, map[string]any{"type": "text", "text": b.Text})
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

func doStreamAnthropic(ctx context.Context, baseURL, apiKey string, model core.Model, body map[string]any, ps *core.ProviderEventStream, opts core.StreamOptions) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := baseURL + "/v1/messages"

	// Build headers for DoWithRetry
	headers := map[string]string{
		"Content-Type":     "application/json",
		"x-api-key":        apiKey,
		"anthropic-version": "2023-06-01",
	}
	if anthropicOpts, ok := body["thinking"]; ok {
		if thinkingMap, ok := anthropicOpts.(map[string]any); ok && thinkingMap["type"] == "enabled" {
			headers["anthropic-beta"] = "interleaved-thinking-2025-05-14"
		}
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

	return processSSEStreamAnthropic(resp.Body, ps, model, opts)
}

func processSSEStreamAnthropic(body io.Reader, ps *core.ProviderEventStream, model core.Model, opts core.StreamOptions) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		textBufs   map[int]*strings.Builder
		thinkBufs  map[int]*strings.Builder
		toolCalls  map[int]*core.ToolCall
		blockTypes map[int]string
		blockSigs  map[int]string
		usage      core.Usage
		stopReason core.StopReason
	)
	toolCalls = make(map[int]*core.ToolCall)
	textBufs = make(map[int]*strings.Builder)
	thinkBufs = make(map[int]*strings.Builder)
	blockTypes = make(map[int]string)
	blockSigs = make(map[int]string)

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
		case "content_block_start":
			block, _ := event["content_block"].(map[string]any)
			blockType, _ := block["type"].(string)
			index, _ := event["index"].(float64)
			idx := int(index)
			blockTypes[idx] = blockType
			if sig, ok := block["signature"].(string); ok && sig != "" {
				blockSigs[idx] = sig
			}
			switch blockType {
			case "text":
				textBufs[idx] = &strings.Builder{}
			case "thinking":
				thinkBufs[idx] = &strings.Builder{}
			case "tool_use":
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				// Emit the full tool call now — the bridge needs one
				// ProviderToolCall to produce toolcall_start + toolcall_end.
				// For Anthropic, tool_use fields arrive fully on
				// content_block_start, but input_json_delta may follow.
				// We emit a partial tool call now; input_json_delta appends
				// to Arguments.
				tc := &core.ToolCall{Type: "toolCall", ID: id, Name: name}
				if input, ok := block["input"].(map[string]any); ok {
					inputBytes, _ := json.Marshal(input)
					tc.Arguments = inputBytes
				}
				toolCalls[idx] = tc
			}

		case "content_block_delta":
			delta, _ := event["delta"].(map[string]any)
			deltaType, _ := delta["type"].(string)
			index, _ := event["index"].(float64)
			idx := int(index)
			switch deltaType {
			case "text_delta":
				text, _ := delta["text"].(string)
				if buf, ok := textBufs[idx]; ok {
					buf.WriteString(text)
				}
				ps.Push(core.ProviderTextDelta{Type: "text_delta", Delta: text})
			case "thinking_delta":
				thinking, _ := delta["thinking"].(string)
				if buf, ok := thinkBufs[idx]; ok {
					buf.WriteString(thinking)
				}
				ps.Push(core.ProviderThinkingDelta{Type: "thinking_delta", Delta: thinking})
			case "signature_delta":
				if sig, ok := delta["signature"].(string); ok && sig != "" {
					blockSigs[idx] = sig
				}
			case "input_json_delta":
				partial, _ := delta["partial_json"].(string)
				if tc, ok := toolCalls[idx]; ok {
					if len(tc.Arguments) == 0 {
						tc.Arguments = []byte(partial)
					} else {
						tc.Arguments = append(tc.Arguments, []byte(partial)...)
					}
				}
			}

		case "content_block_stop":
			index, _ := event["index"].(float64)
			idx := int(index)
			blockType := blockTypes[idx]
			sig := blockSigs[idx]
			switch blockType {
			case "tool_use":
				if tc, ok := toolCalls[idx]; ok {
					ps.Push(core.ProviderToolCall{
						Type: "tool_call", ID: tc.ID, Name: tc.Name,
						Arguments: tc.Arguments,
					})
				}
			case "text":
				if sig != "" {
					ps.Push(core.ProviderContentBlockStop{
						Type:          "content_block_stop",
						TextSignature: sig,
					})
				}
			case "thinking":
				if sig != "" {
					ps.Push(core.ProviderContentBlockStop{
						Type:              "content_block_stop",
						ThinkingSignature: sig,
					})
				}
			}

		case "message_start":
			message, _ := event["message"].(map[string]any)
			if message != nil {
				if u, ok := message["usage"].(map[string]any); ok {
					usage.Input = conv.GetInt(u, "input_tokens")
					usage.Output = conv.GetInt(u, "output_tokens")
					usage.CacheRead = conv.GetInt(u, "cache_read_input_tokens")
					usage.CacheWrite = conv.GetInt(u, "cache_creation_input_tokens")
				}
			}

		case "message_delta":
			delta, _ := event["delta"].(map[string]any)
			if reason, ok := delta["stop_reason"].(string); ok {
				stopReason = mapStopReason(reason)
			}
			if u, ok := event["usage"].(map[string]any); ok {
				usage.Output = conv.GetInt(u, "output_tokens")
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("anthropic: SSE read error: %w", err)
	}

	usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	usage.Cost = core.CalculateCost(model, usage)

	// Build final message with Content blocks containing signatures
	var finalContent []core.ContentBlock
	for idx, buf := range textBufs {
		if buf.Len() == 0 {
			continue
		}
		finalContent = append(finalContent, core.TextContent{
			Type: "text", Text: buf.String(), TextSignature: blockSigs[idx],
		})
	}
	for idx, buf := range thinkBufs {
		if buf.Len() == 0 {
			continue
		}
		finalContent = append(finalContent, core.ThinkingContent{
			Type: "thinking", Thinking: buf.String(), ThinkingSignature: blockSigs[idx],
		})
	}

	final := core.AssistantMessage{
		Role: "assistant", API: model.API, Provider: model.Provider, Model: model.ID,
		Usage: usage, StopReason: stopReason, Timestamp: time.Now(),
		Content: finalContent,
	}

	ps.Push(core.ProviderResponseEnd{
		Type: "response_end", Message: final,
		FinishReason: string(stopReason),
	})

	return nil
}

func mapStopReason(reason string) core.StopReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return core.StopStop
	case "max_tokens":
		return core.StopLength
	case "tool_use":
		return core.StopToolUse
	default:
		return core.StopStop
	}
}
