package google

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

	body, err := buildGoogleBody(model, c, opts, googleOpts)
	if err != nil {
		return nil, fmt.Errorf("google: failed to build request: %w", err)
	}
	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				stream.Error(fmt.Errorf("google: panic: %v", r))
			}
		}()
		msg, err := doGoogleStream(ctx, baseURL, apiKey, model, body, stream, opts)
		if err != nil {
			stream.Error(err)
			return
		}
		stream.End(msg)
	}()

	return stream, nil
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

func doGoogleStream(ctx context.Context, baseURL, apiKey string, model core.Model, body map[string]any, stream *core.AssistantMessageEventStream, opts core.StreamOptions) (core.AssistantMessage, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return core.AssistantMessage{}, err
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", baseURL, model.ID)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return core.AssistantMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)
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
		return core.AssistantMessage{}, fmt.Errorf("google: API error %d: %s", resp.StatusCode, string(bodyBytes))
	}
	return processGoogleSSE(resp.Body, stream, model, opts)
}

func processGoogleSSE(body io.Reader, stream *core.AssistantMessageEventStream, model core.Model, opts core.StreamOptions) (core.AssistantMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		msg          core.AssistantMessage
		textBuf      strings.Builder
		thinkBuf     strings.Builder
		toolCalls    []core.ToolCall
		textOpen     bool
		thinkingOpen bool
		nextBlockIdx int
	)
	msg.API = model.API
	msg.Provider = model.Provider
	msg.Model = model.ID
	msg.Role = "assistant"
	msg.Timestamp = time.Now()

	stream.Push(core.EventStart{Type: "start", API: model.API, Provider: model.Provider, Model: model.ID, Timestamp: time.Now()})

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
			msg.StopReason = MapStopReason(finishReason)
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
						idx := nextBlockIdx
						nextBlockIdx++
						stream.Push(core.EventThinkingStart{Type: "thinking_start", ContentIndex: idx})
						thinkingOpen = true
					}
					thinkBuf.WriteString(text)
					stream.Push(core.EventThinkingDelta{Type: "thinking_delta", Delta: text})
				}
			} else if text, ok := p["text"].(string); ok {
				if !textOpen {
					idx := nextBlockIdx
					nextBlockIdx++
					stream.Push(core.EventTextStart{Type: "text_start", ContentIndex: idx})
					textOpen = true
				}
				textBuf.WriteString(text)
				stream.Push(core.EventTextDelta{Type: "text_delta", Delta: text})
			} else if fc, ok := p["functionCall"].(map[string]any); ok {
				name, _ := fc["name"].(string)
				args, _ := fc["args"].(map[string]any)
				argsBytes, _ := json.Marshal(args)
				id := fmt.Sprintf("call_%d", len(toolCalls))
				tc := core.ToolCall{Type: "toolCall", ID: id, Name: name, Arguments: argsBytes}
				toolCalls = append(toolCalls, tc)
				idx := nextBlockIdx
				nextBlockIdx++
				stream.Push(core.EventToolCallStart{Type: "toolcall_start", ContentIndex: idx, ID: id, Name: name})
				stream.Push(core.EventToolCallEnd{Type: "toolcall_end", ContentIndex: idx, ID: id, Arguments: argsBytes})
			}
		}
		if usageMetadata, ok := chunk["usageMetadata"].(map[string]any); ok {
			msg.Usage.Input = conv.GetInt(usageMetadata, "promptTokenCount")
			msg.Usage.Output = conv.GetInt(usageMetadata, "candidatesTokenCount")
			msg.Usage.TotalTokens = conv.GetInt(usageMetadata, "totalTokenCount")
		}
	}

	if err := scanner.Err(); err != nil {
		return msg, fmt.Errorf("google: SSE read error: %w", err)
	}

	if textOpen {
		msg.Content = append(msg.Content, core.TextContent{Type: "text", Text: textBuf.String()})
		stream.Push(core.EventTextEnd{Type: "text_end", Content: textBuf.String()})
	}
	if thinkingOpen {
		msg.Content = append(msg.Content, core.ThinkingContent{Type: "thinking", Thinking: thinkBuf.String()})
		stream.Push(core.EventThinkingEnd{Type: "thinking_end", Content: thinkBuf.String()})
	}
	for _, tc := range toolCalls {
		msg.Content = append(msg.Content, tc)
	}

	msg.Usage.Cost = core.CalculateCost(model, msg.Usage)
	stream.Push(core.EventDone{Type: "done", Reason: msg.StopReason, Message: msg})

	return msg, nil
}
