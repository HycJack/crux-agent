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

	"crux-ai/internal/conv"

	core "crux-ai/core"
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

	body, err := buildCompletionsBody(model, c, opts, completionsOpts)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to build request: %w", err)
	}
	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				stream.Error(fmt.Errorf("openai: panic: %v", r))
			}
		}()
		msg, err := doCompletionsStream(ctx, baseURL, apiKey, model, body, stream, opts)
		if err != nil {
			stream.Error(err)
			return
		}
		stream.End(msg)
	}()

	return stream, nil
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

func doCompletionsStream(ctx context.Context, baseURL, apiKey string, model core.Model, body map[string]any, stream *core.AssistantMessageEventStream, opts core.StreamOptions) (core.AssistantMessage, error) {
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
		return core.AssistantMessage{}, fmt.Errorf("openai: API error %d: %s", resp.StatusCode, string(bodyBytes))
	}
	return processCompletionsSSE(resp.Body, stream, model, opts)
}

func processCompletionsSSE(body io.Reader, stream *core.AssistantMessageEventStream, model core.Model, opts core.StreamOptions) (core.AssistantMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		msg          core.AssistantMessage
		textBuf      strings.Builder
		textOpen     bool
		textStartIx  int // -1 until first text delta; smaller = opened earlier
		thinkingBuf  strings.Builder
		thinkOpen    bool
		thinkStartIx int // -1 until first reasoning delta
		toolCalls    map[int]*core.ToolCall
		toolIndices  []int
		nextBlockIx  int
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
			msg.StopReason = MapStopReason(finishReason)
		}

		// Parse usage from the last chunk (when stream_options.include_usage is set).
		if usage, ok := chunk["usage"].(map[string]any); ok {
			msg.Usage.Input = conv.GetInt(usage, "prompt_tokens")
			msg.Usage.Output = conv.GetInt(usage, "completion_tokens")
			msg.Usage.TotalTokens = conv.GetInt(usage, "total_tokens")
			// OpenAI returns cached_tokens inside prompt_tokens_details.
			if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
				msg.Usage.CacheRead = conv.GetInt(details, "cached_tokens")
			}
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
				textStartIx = idx
			}
			textBuf.WriteString(content)
			stream.Push(core.EventTextDelta{Type: "text_delta", ContentIndex: textStartIx, Delta: content})
		}
		if reasoning, ok := delta["reasoning_content"].(string); ok && reasoning != "" {
			if !thinkOpen {
				idx := nextBlockIx
				nextBlockIx++
				stream.Push(core.EventThinkingStart{Type: "thinking_start", ContentIndex: idx})
				thinkOpen = true
				thinkStartIx = idx
			}
			thinkingBuf.WriteString(reasoning)
			stream.Push(core.EventThinkingDelta{Type: "thinking_delta", ContentIndex: thinkStartIx, Delta: reasoning})
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
					toolIndices = append(toolIndices, index)
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
		return msg, fmt.Errorf("openai: SSE read error: %w", err)
	}

	// Send end events for all collected content. We close in stream-order
	// (i.e. by the contentIndex each block was opened with) so msg.Content
	// matches the order in which the model emitted the parts. This is
	// critical when reasoning comes before visible text: callers expect
	// the thinking block at index 0, not after the text block.
	textFirst := textOpen && (!thinkOpen || textStartIx <= thinkStartIx)
	thinkFirst := thinkOpen && (!textOpen || thinkStartIx < textStartIx)
	if textFirst {
		msg.Content = append(msg.Content, core.TextContent{Type: "text", Text: textBuf.String()})
		stream.Push(core.EventTextEnd{Type: "text_end", ContentIndex: textStartIx, Content: textBuf.String()})
	}
	if thinkFirst || (thinkOpen && !textOpen) {
		// OpenAI's `reasoning_content` is reasoning, not user-visible text.
		// Store as ThinkingContent so downstream consumers can render it
		// distinctly (mirrors Mistral/Responses/Claude behaviour).
		msg.Content = append(msg.Content, core.ThinkingContent{Type: "thinking", Thinking: thinkingBuf.String()})
		stream.Push(core.EventThinkingEnd{Type: "thinking_end", ContentIndex: thinkStartIx, Content: thinkingBuf.String()})
	}
	if !textFirst && textOpen {
		msg.Content = append(msg.Content, core.TextContent{Type: "text", Text: textBuf.String()})
		stream.Push(core.EventTextEnd{Type: "text_end", ContentIndex: textStartIx, Content: textBuf.String()})
	}
	for _, index := range toolIndices {
		if tc, ok := toolCalls[index]; ok {
			stream.Push(core.EventToolCallEnd{Type: "toolcall_end", ID: tc.ID, Arguments: tc.Arguments})
			msg.Content = append(msg.Content, *tc)
		}
	}

	msg.Usage.TotalTokens = msg.Usage.Input + msg.Usage.Output + msg.Usage.CacheRead
	msg.Usage.Cost = core.CalculateCost(model, msg.Usage)
	stream.Push(core.EventDone{Type: "done", Reason: msg.StopReason, Message: msg})

	return msg, nil
}

func clampEffort(effort core.ThinkingLevel) core.ThinkingLevel {
	if effort == core.ThinkingXHigh {
		return core.ThinkingHigh
	}
	return effort
}
