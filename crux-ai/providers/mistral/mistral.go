// Package mistral implements the Mistral Conversations API provider.
package mistral

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

	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/internal/conv"
)

const defaultBaseURL = "https://api.mistral.ai/v1"

// Options holds Mistral-specific options.
type Options struct {
	ToolChoice      any    `json:"toolChoice,omitempty"`
	PromptMode      string `json:"promptMode,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

// Provider implements the Mistral Conversations API.
type Provider struct{}

// New creates a new Mistral provider.
func New() *Provider { return &Provider{} }

func (p *Provider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.AssistantMessageEventStream, error) {
	return streamMistral(ctx, model, llmCtx, opts, Options{})
}

func (p *Provider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.AssistantMessageEventStream, error) {
	mistralOpts := Options{}
	if opts.Reasoning != "" {
		mistralOpts.PromptMode = "reasoning"
		// Map our unified thinking levels to Mistral's three-tier effort model.
		// "minimal"/"low" → "low", "medium" → "medium", "high"/"xhigh" → "high".
		switch opts.Reasoning {
		case core.ThinkingMinimal, core.ThinkingLow:
			mistralOpts.ReasoningEffort = "low"
		case core.ThinkingMedium:
			mistralOpts.ReasoningEffort = "medium"
		case core.ThinkingHigh, core.ThinkingXHigh:
			mistralOpts.ReasoningEffort = "high"
		default:
			mistralOpts.ReasoningEffort = string(opts.Reasoning)
		}
	}
	return streamMistral(ctx, model, llmCtx, opts.StreamOptions, mistralOpts)
}

func streamMistral(ctx context.Context, model core.Model, c core.Context, opts core.StreamOptions, mistralOpts Options) (*core.AssistantMessageEventStream, error) {
	apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("mistral: no API key provided")
	}
	baseURL := core.ResolveBaseURL(model, defaultBaseURL)

	body, err := buildMistralBody(model, c, opts, mistralOpts)
	if err != nil {
		return nil, fmt.Errorf("mistral: failed to build request: %w", err)
	}
	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				stream.Error(fmt.Errorf("mistral: panic: %v", r))
			}
		}()
		msg, err := doMistralStream(ctx, baseURL, apiKey, model, body, stream, opts)
		if err != nil {
			stream.Error(err)
			return
		}
		stream.End(msg)
	}()

	return stream, nil
}

func buildMistralBody(model core.Model, c core.Context, opts core.StreamOptions, mistralOpts Options) (map[string]any, error) {
	body := map[string]any{"model": model.ID, "stream": true}
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
	msgs, err := convertMessages(c.Messages)
	if err != nil {
		return nil, err
	}
	messages = append(messages, msgs...)
	body["messages"] = messages
	if len(c.Tools) > 0 {
		body["tools"] = convertTools(c.Tools)
	}
	if mistralOpts.ToolChoice != nil {
		body["tool_choice"] = mistralOpts.ToolChoice
	}
	if mistralOpts.PromptMode != "" {
		body["prompt_mode"] = mistralOpts.PromptMode
	}
	// Mistral's reasoning models expose a `reasoning_effort` parameter that
	// is independent of `prompt_mode`. Forward the user-provided value so
	// callers can tune effort directly through the unified reasoning knob.
	if mistralOpts.ReasoningEffort != "" {
		body["reasoning_effort"] = mistralOpts.ReasoningEffort
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
			result = append(result, convertAssistantMessage(m.Content))
		case core.ToolResultMessage:
			content := convertToolResultContent(m.Content)
			result = append(result, map[string]any{
				"role": "tool", "tool_call_id": normalizeToolCallID(m.ToolCallID), "content": content,
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
				blocks = append(blocks, map[string]any{"type": "text", "text": b.Text})
			case core.ImageContent:
				blocks = append(blocks, map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": "data:" + b.MimeType + ";base64," + b.Data},
				})
			}
		}
		return blocks, nil
	default:
		return fmt.Sprintf("%v", content), nil
	}
}

func convertAssistantMessage(content []core.ContentBlock) map[string]any {
	var textParts []string
	var toolCalls []any
	for _, block := range content {
		switch b := block.(type) {
		case core.TextContent:
			textParts = append(textParts, b.Text)
		case core.ToolCall:
			toolCalls = append(toolCalls, map[string]any{
				"id": normalizeToolCallID(b.ID), "type": "function",
				"function": map[string]any{"name": b.Name, "arguments": string(b.Arguments)},
			})
		}
	}
	msg := map[string]any{"role": "assistant"}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	if len(textParts) > 0 {
		msg["content"] = strings.Join(textParts, "\n")
	}
	return msg
}

func convertToolResultContent(content []core.ContentBlock) string {
	var parts []string
	for _, block := range content {
		if text, ok := block.(core.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func convertTools(tools []core.Tool) []map[string]any {
	result := make([]map[string]any, len(tools))
	for i, tool := range tools {
		t := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": tool.Name, "description": tool.Description,
			},
		}
		if len(tool.Parameters) > 0 {
			var params map[string]any
			if err := json.Unmarshal(tool.Parameters, &params); err == nil {
				t["function"].(map[string]any)["parameters"] = params
			}
		}
		result[i] = t
	}
	return result
}

func normalizeToolCallID(id string) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	const targetLen = 9
	if len(id) == targetLen {
		valid := true
		for i := 0; i < targetLen; i++ {
			c := id[i]
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
				valid = false
				break
			}
		}
		if valid {
			return id
		}
	}
	buf := make([]byte, targetLen)
	for i := 0; i < targetLen; i++ {
		if i < len(id) {
			c := id[i]
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
				buf[i] = c
			} else {
				buf[i] = chars[int(c)%len(chars)]
			}
		} else {
			buf[i] = chars[0]
		}
	}
	return string(buf)
}

func doMistralStream(ctx context.Context, baseURL, apiKey string, model core.Model, body map[string]any, stream *core.AssistantMessageEventStream, opts core.StreamOptions) (core.AssistantMessage, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return core.AssistantMessage{}, err
	}
	url := baseURL + "/chat/completions"

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return core.AssistantMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	for k, v := range core.ProviderHeadersToRecord(core.MergeProviderHeaders(model.Headers, opts.Headers)) {
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
		return core.AssistantMessage{}, fmt.Errorf("mistral: API error %d: %s", resp.StatusCode, string(bodyBytes))
	}
	return processMistralSSE(resp.Body, stream, model, opts)
}

func processMistralSSE(body io.Reader, stream *core.AssistantMessageEventStream, model core.Model, opts core.StreamOptions) (core.AssistantMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		msg         core.AssistantMessage
		textBuf     strings.Builder
		textOpen    bool
		thinkBuf    strings.Builder
		thinkOpen   bool
		toolCalls   map[int]*core.ToolCall
		nextBlockIx int
	)
	msg.API = model.API
	msg.Provider = model.Provider
	msg.Model = model.ID
	msg.Role = "assistant"
	msg.Timestamp = time.Now()
	toolCalls = make(map[int]*core.ToolCall)

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
		if finishReason, ok := choice["finish_reason"].(string); ok && finishReason != "" {
			msg.StopReason = mapStopReason(finishReason)
		}
		if usage, ok := chunk["usage"].(map[string]any); ok {
			msg.Usage.Input = conv.GetInt(usage, "prompt_tokens")
			msg.Usage.Output = conv.GetInt(usage, "completion_tokens")
			msg.Usage.TotalTokens = conv.GetInt(usage, "total_tokens")
		}
		delta, ok := choice["delta"].(map[string]any)
		if !ok {
			continue
		}
		if content, ok := delta["content"].(string); ok && content != "" {
			if !textOpen {
				idx := nextBlockIx
				nextBlockIx++
				stream.Push(core.EventTextStart{Type: "text_start", ContentIndex: idx})
				textOpen = true
			}
			textBuf.WriteString(content)
			stream.Push(core.EventTextDelta{Type: "text_delta", Delta: content})
		}
		// Mistral reasoning surfaces as delta.reasoning_content on
		// magistral-class models. We allocate a dedicated content index
		// so downstream consumers can interleave reasoning and text.
		if reasoning, ok := delta["reasoning_content"].(string); ok && reasoning != "" {
			if !thinkOpen {
				idx := nextBlockIx
				nextBlockIx++
				stream.Push(core.EventThinkingStart{Type: "thinking_start", ContentIndex: idx})
				thinkOpen = true
			}
			thinkBuf.WriteString(reasoning)
			stream.Push(core.EventThinkingDelta{Type: "thinking_delta", Delta: reasoning})
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
					idx := nextBlockIx
					nextBlockIx++
					toolCalls[index] = &core.ToolCall{Type: "toolCall", ID: id, Name: name}
					stream.Push(core.EventToolCallStart{Type: "toolcall_start", ContentIndex: idx, ID: id, Name: name})
				}
				if tc, ok := toolCalls[index]; ok && args != "" {
					tc.Arguments = append(tc.Arguments, []byte(args)...)
					stream.Push(core.EventToolCallDelta{Type: "toolcall_delta", ID: tc.ID, ArgumentsDelta: args})
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return msg, fmt.Errorf("mistral: SSE read error: %w", err)
	}

	if textOpen {
		msg.Content = append(msg.Content, core.TextContent{Type: "text", Text: textBuf.String()})
		stream.Push(core.EventTextEnd{Type: "text_end", Content: textBuf.String()})
	}
	if thinkOpen {
		msg.Content = append(msg.Content, core.ThinkingContent{Type: "thinking", Thinking: thinkBuf.String()})
		stream.Push(core.EventThinkingEnd{Type: "thinking_end", Content: thinkBuf.String()})
	}
	for _, tc := range toolCalls {
		stream.Push(core.EventToolCallEnd{Type: "toolcall_end", ID: tc.ID, Arguments: tc.Arguments})
		msg.Content = append(msg.Content, *tc)
	}

	msg.Usage.Cost = core.CalculateCost(model, msg.Usage)
	stream.Push(core.EventDone{Type: "done", Reason: msg.StopReason, Message: msg})

	return msg, nil
}

func mapStopReason(reason string) core.StopReason {
	switch reason {
	case "stop":
		return core.StopStop
	case "length":
		return core.StopLength
	case "tool_calls":
		return core.StopToolUse
	default:
		return core.StopStop
	}
}
