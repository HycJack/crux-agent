// Package anthropic implements the Anthropic Messages API provider.
package anthropic

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

	"crux-ai/internal/conv"

	core "crux-ai/core"
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

func doStream(ctx context.Context, baseURL, apiKey string, model core.Model, body map[string]any, stream *core.AssistantMessageEventStream, opts core.StreamOptions) (core.AssistantMessage, error) {
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
		return core.AssistantMessage{}, fmt.Errorf("anthropic: API error %d: %s", resp.StatusCode, string(bodyBytes))
	}
	return processSSEStream(resp.Body, stream, model, opts)
}

func processSSEStream(body io.Reader, stream *core.AssistantMessageEventStream, model core.Model, opts core.StreamOptions) (core.AssistantMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		msg        core.AssistantMessage
		textBufs   map[int]*strings.Builder
		thinkBufs  map[int]*strings.Builder
		toolCalls  map[int]*core.ToolCall
		blockTypes map[int]string
		blockSigs  map[int]string
	)
	msg.API = model.API
	msg.Provider = model.Provider
	msg.Model = model.ID
	msg.Role = "assistant"
	msg.Timestamp = time.Now()
	toolCalls = make(map[int]*core.ToolCall)
	textBufs = make(map[int]*strings.Builder)
	thinkBufs = make(map[int]*strings.Builder)
	blockTypes = make(map[int]string)
	blockSigs = make(map[int]string)

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
		case "content_block_start":
			block, _ := event["content_block"].(map[string]any)
			blockType, _ := block["type"].(string)
			index, _ := event["index"].(float64)
			idx := int(index)
			blockTypes[idx] = blockType
			switch blockType {
			case "text":
				// Anthropic delivers the signature on the *content_block*
				// inside content_block_start, not on the outer event.
				if sig, ok := block["signature"].(string); ok && sig != "" {
					blockSigs[idx] = sig
				}
				textBufs[idx] = &strings.Builder{}
				stream.Push(core.EventTextStart{Type: "text_start", ContentIndex: idx})
			case "thinking":
				if sig, ok := block["signature"].(string); ok && sig != "" {
					blockSigs[idx] = sig
				}
				thinkBufs[idx] = &strings.Builder{}
				stream.Push(core.EventThinkingStart{Type: "thinking_start", ContentIndex: idx})
			case "tool_use":
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				toolCalls[idx] = &core.ToolCall{Type: "toolCall", ID: id, Name: name}
				stream.Push(core.EventToolCallStart{Type: "toolcall_start", ContentIndex: idx, ID: id, Name: name})
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
				stream.Push(core.EventTextDelta{Type: "text_delta", ContentIndex: idx, Delta: text})
			case "thinking_delta":
				thinking, _ := delta["thinking"].(string)
				if buf, ok := thinkBufs[idx]; ok {
					buf.WriteString(thinking)
				}
				stream.Push(core.EventThinkingDelta{Type: "thinking_delta", ContentIndex: idx, Delta: thinking})
			case "signature_delta":
				// Some Anthropic responses deliver the signature as a
				// separate delta event rather than embedded in the block.
				if sig, ok := delta["signature"].(string); ok && sig != "" {
					blockSigs[idx] = sig
				}
			case "input_json_delta":
				partial, _ := delta["partial_json"].(string)
				if tc, ok := toolCalls[idx]; ok {
					tc.Arguments = append(tc.Arguments, []byte(partial)...)
					stream.Push(core.EventToolCallDelta{Type: "toolcall_delta", ContentIndex: idx, ID: tc.ID, ArgumentsDelta: partial})
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
					stream.Push(core.EventToolCallEnd{Type: "toolcall_end", ContentIndex: idx, ID: tc.ID, Arguments: tc.Arguments})
					msg.Content = append(msg.Content, *tc)
				}
			case "text":
				content := ""
				if buf, ok := textBufs[idx]; ok {
					content = buf.String()
				}
				stream.Push(core.EventTextEnd{Type: "text_end", ContentIndex: idx, Content: content, TextSignature: sig})
				msg.Content = append(msg.Content, core.TextContent{Type: "text", Text: content, TextSignature: sig})
				delete(textBufs, idx)
			case "thinking":
				content := ""
				if buf, ok := thinkBufs[idx]; ok {
					content = buf.String()
				}
				stream.Push(core.EventThinkingEnd{Type: "thinking_end", ContentIndex: idx, Content: content, ThinkingSignature: sig})
				msg.Content = append(msg.Content, core.ThinkingContent{Type: "thinking", Thinking: content, ThinkingSignature: sig})
				delete(thinkBufs, idx)
			}

		case "message_start":
			message, _ := event["message"].(map[string]any)
			if message != nil {
				if usage, ok := message["usage"].(map[string]any); ok {
					msg.Usage.Input = conv.GetInt(usage, "input_tokens")
					msg.Usage.Output = conv.GetInt(usage, "output_tokens")
					msg.Usage.CacheRead = conv.GetInt(usage, "cache_read_input_tokens")
					msg.Usage.CacheWrite = conv.GetInt(usage, "cache_creation_input_tokens")
				}
			}

		case "message_delta":
			delta, _ := event["delta"].(map[string]any)
			if stopReason, ok := delta["stop_reason"].(string); ok {
				msg.StopReason = mapStopReason(stopReason)
			}
			if usage, ok := event["usage"].(map[string]any); ok {
				msg.Usage.Output = conv.GetInt(usage, "output_tokens")
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return msg, fmt.Errorf("anthropic: SSE read error: %w", err)
	}

	// Drain any per-block buffers whose content_block_stop never arrived
	// (e.g. provider stream ended mid-block). We append as anonymous text
	// blocks with the captured signature, if any.
	for idx, buf := range textBufs {
		if buf.Len() == 0 {
			continue
		}
		msg.Content = append(msg.Content, core.TextContent{
			Type: "text", Text: buf.String(), TextSignature: blockSigs[idx],
		})
	}
	for idx, buf := range thinkBufs {
		if buf.Len() == 0 {
			continue
		}
		msg.Content = append(msg.Content, core.ThinkingContent{
			Type: "thinking", Thinking: buf.String(), ThinkingSignature: blockSigs[idx],
		})
	}

	msg.Usage.TotalTokens = msg.Usage.Input + msg.Usage.Output + msg.Usage.CacheRead + msg.Usage.CacheWrite
	msg.Usage.Cost = core.CalculateCost(model, msg.Usage)

	stream.Push(core.EventDone{Type: "done", Reason: msg.StopReason, Message: msg})

	return msg, nil
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
