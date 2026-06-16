// Package bedrock implements the Amazon Bedrock Converse Stream API provider.
package bedrock

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	core "github.com/hycjack/crux-ai/core"
)

const defaultRegion = "us-east-1"

// Options holds Bedrock-specific options.
type Options struct {
	Region             string `json:"region,omitempty"`
	Profile            string `json:"profile,omitempty"`
	ToolChoice         any    `json:"toolChoice,omitempty"`
	Reasoning          bool   `json:"reasoning,omitempty"`
	ThinkingBudgets    map[string]int `json:"thinkingBudgets,omitempty"`
	InterleavedThinking bool `json:"interleavedThinking,omitempty"`
	ThinkingDisplay    string `json:"thinkingDisplay,omitempty"`
	RequestMetadata    map[string]string `json:"requestMetadata,omitempty"`
	BearerToken        string `json:"bearerToken,omitempty"`
}

// Provider implements the Amazon Bedrock Converse Stream API.
type Provider struct{}

// New creates a new Bedrock provider.
func New() *Provider {
	return &Provider{}
}

func (p *Provider) Stream(ctx context.Context, model core.Model, llmCtx core.Context, opts core.StreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	return streamBedrock(ctx, model, llmCtx, opts, Options{})
}

func (p *Provider) StreamSimple(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	bedrockOpts := Options{}
	if opts.Reasoning != "" {
		bedrockOpts.Reasoning = true
		if opts.ThinkingBudgets != nil {
			bedrockOpts.ThinkingBudgets = opts.ThinkingBudgets
		}
	}
	return streamBedrock(ctx, model, llmCtx, opts.StreamOptions, bedrockOpts)
}

func streamBedrock(ctx context.Context, model core.Model, c core.Context, opts core.StreamOptions, bedrockOpts Options) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)
	region := bedrockOpts.Region
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = defaultRegion
	}

	body, err := buildBedrockBody(model, c, opts, bedrockOpts)
	if err != nil {
		return nil, fmt.Errorf("bedrock: failed to build request: %w", err)
	}

	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				stream.Error(fmt.Errorf("bedrock: panic: %v", r))
			}
		}()

		msg, err := doBedrockStream(ctx, region, apiKey, model, body, stream, opts, bedrockOpts)
		if err != nil {
			stream.Error(err)
			return
		}
		stream.End(msg)
	}()

	return stream, nil
}

func buildBedrockBody(model core.Model, c core.Context, opts core.StreamOptions, bedrockOpts Options) (map[string]any, error) {
	// Convert messages to Bedrock format
	messages, err := convertMessages(c.Messages)
	if err != nil {
		return nil, err
	}

	body := map[string]any{
		"messages": messages,
	}

	// System
	if c.SystemPrompt != "" {
		body["system"] = []any{
			map[string]any{
				"text": c.SystemPrompt,
			},
		}
	}

	// Inference config
	inferenceConfig := map[string]any{}
	if opts.MaxTokens != nil && *opts.MaxTokens > 0 {
		inferenceConfig["maxTokens"] = *opts.MaxTokens
	} else if model.MaxTokens > 0 {
		inferenceConfig["maxTokens"] = model.MaxTokens
	}
	if opts.Temperature != nil {
		inferenceConfig["temperature"] = *opts.Temperature
	}
	if len(inferenceConfig) > 0 {
		body["inferenceConfig"] = inferenceConfig
	}

	// Tools
	if len(c.Tools) > 0 {
		body["toolConfig"] = map[string]any{
			"tools": convertTools(c.Tools),
		}
	}

	// Tool choice
	if bedrockOpts.ToolChoice != nil {
		if tc, ok := body["toolConfig"].(map[string]any); ok {
			tc["toolChoice"] = bedrockOpts.ToolChoice
		}
	}

	// Thinking/Reasoning
	if bedrockOpts.Reasoning {
		thinkingConfig := map[string]any{
			"enabled": true,
		}
		if bedrockOpts.ThinkingBudgets != nil {
			if budget, ok := bedrockOpts.ThinkingBudgets["medium"]; ok {
				thinkingConfig["budgetTokens"] = budget
			}
		}
		body["thinking"] = thinkingConfig
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
			result = append(result, map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"toolResult": map[string]any{
							"toolUseId": m.ToolCallID,
							"content":   content,
							"status":    mapStatus(m.IsError),
						},
					},
				},
			})
		}
	}

	return result, nil
}

func convertUserContent(content any) ([]any, error) {
	switch c := content.(type) {
	case string:
		return []any{
			map[string]any{"text": c},
		}, nil
	case []core.ContentBlock:
		var blocks []any
		for _, block := range c {
			switch b := block.(type) {
			case core.TextContent:
				blocks = append(blocks, map[string]any{"text": b.Text})
			case core.ImageContent:
				blocks = append(blocks, map[string]any{
					"image": map[string]any{
						"format": mimeToFormat(b.MimeType),
						"source": map[string]any{
							"bytes": b.Data,
						},
					},
				})
			}
		}
		return blocks, nil
	default:
		return []any{
			map[string]any{"text": fmt.Sprintf("%v", content)},
		}, nil
	}
}

func convertAssistantContent(content []core.ContentBlock) []any {
	var blocks []any
	for _, block := range content {
		switch b := block.(type) {
		case core.TextContent:
			blocks = append(blocks, map[string]any{"text": b.Text})
		case core.ThinkingContent:
			blocks = append(blocks, map[string]any{
				"thinking": map[string]any{
					"thinking": b.Thinking,
				},
			})
		case core.ToolCall:
			blocks = append(blocks, map[string]any{
				"toolUse": map[string]any{
					"toolUseId": b.ID,
					"name":      b.Name,
					"input":     json.RawMessage(b.Arguments),
				},
			})
		}
	}
	return blocks
}

func convertToolResultContent(content []core.ContentBlock) []any {
	var blocks []any
	for _, block := range content {
		if text, ok := block.(core.TextContent); ok {
			blocks = append(blocks, map[string]any{"text": text.Text})
		}
	}
	return blocks
}

func convertTools(tools []core.Tool) []any {
	result := make([]any, len(tools))
	for i, tool := range tools {
		t := map[string]any{
			"toolSpec": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
			},
		}
		if len(tool.Parameters) > 0 {
			var params map[string]any
			if err := json.Unmarshal(tool.Parameters, &params); err == nil {
				t["toolSpec"].(map[string]any)["inputSchema"] = map[string]any{
					"json": params,
				}
			}
		}
		result[i] = t
	}
	return result
}

func mapStatus(isError bool) string {
	if isError {
		return "error"
	}
	return "success"
}

func mimeToFormat(mimeType string) string {
	parts := strings.Split(mimeType, "/")
	if len(parts) > 1 {
		return parts[1]
	}
	return mimeType
}

func doBedrockStream(ctx context.Context, region, apiKey string, model core.Model, body map[string]any, stream *core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], opts core.StreamOptions, bedrockOpts Options) (core.AssistantMessage, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return core.AssistantMessage{}, err
	}

	url := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse-stream", region, model.ID)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return core.AssistantMessage{}, err
	}

	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}

	resp, err := core.SSEClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return core.AssistantMessage{}, core.WrapHTTPTimeout(core.ProviderAmazonBedrock, 5*time.Minute, err)
		}
		return core.AssistantMessage{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		if classified := core.ClassifyHTTPError(model.Provider, resp.StatusCode, string(bodyBytes)); classified != nil {
			return core.AssistantMessage{}, classified
		}
		return core.AssistantMessage{}, fmt.Errorf("bedrock: API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return processBedrockSSE(resp.Body, stream, model, opts)
}

func processBedrockSSE(body io.Reader, stream *core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], model core.Model, opts core.StreamOptions) (core.AssistantMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var (
		msg       core.AssistantMessage
		textBuf   strings.Builder
		thinkBuf  strings.Builder
		toolCalls []core.ToolCall
	)

	msg.API = model.API
	msg.Provider = model.Provider
	msg.Model = model.ID
	msg.Role = "assistant"
	msg.Timestamp = time.Now()

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

		if opts.OnResponse != nil {
			opts.OnResponse(data)
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		// Handle different event types
		if contentBlockDelta, ok := event["contentBlockDelta"].(map[string]any); ok {
			delta, _ := contentBlockDelta["delta"].(map[string]any)

			if text, ok := delta["text"].(string); ok {
				textBuf.WriteString(text)
				stream.Push(core.EventTextDelta{
					Type:  "text_delta",
					Delta: text,
				})
			}

			if thinking, ok := delta["thinking"].(map[string]any); ok {
				if t, ok := thinking["thinking"].(string); ok {
					thinkBuf.WriteString(t)
					stream.Push(core.EventThinkingDelta{
						Type:  "thinking_delta",
						Delta: t,
					})
				}
			}

			if toolUse, ok := delta["toolUse"].(map[string]any); ok {
				if input, ok := toolUse["input"].(string); ok {
					if len(toolCalls) > 0 {
						last := &toolCalls[len(toolCalls)-1]
						last.Arguments = append(last.Arguments, []byte(input)...)
						stream.Push(core.EventToolCallDelta{
							Type:           "toolcall_delta",
							ID:             last.ID,
							ArgumentsDelta: input,
						})
					}
				}
			}
		}

		if contentBlockStart, ok := event["contentBlockStart"].(map[string]any); ok {
			start, _ := contentBlockStart["start"].(map[string]any)
			if toolUse, ok := start["toolUse"].(map[string]any); ok {
				id, _ := toolUse["toolUseId"].(string)
				name, _ := toolUse["name"].(string)
				toolCalls = append(toolCalls, core.ToolCall{
					Type: "toolCall",
					ID:   id,
					Name: name,
				})
				stream.Push(core.EventToolCallStart{
					Type: "toolcall_start",
					ID:   id,
					Name: name,
				})
			}
		}

		if messageStop, ok := event["messageStop"].(map[string]any); ok {
			if stopReason, ok := messageStop["stopReason"].(string); ok {
				msg.StopReason = mapBedrockStopReason(stopReason)
			}
		}

		if metadata, ok := event["metadata"].(map[string]any); ok {
			if usage, ok := metadata["usage"].(map[string]any); ok {
				msg.Usage.Input = int(getFloat(usage, "inputTokens"))
				msg.Usage.Output = int(getFloat(usage, "outputTokens"))
				msg.Usage.CacheRead = int(getFloat(usage, "cacheReadInputTokens"))
				msg.Usage.CacheWrite = int(getFloat(usage, "cacheWriteInputTokens"))
			}
		}
	}

	// Finalize
	if textBuf.Len() > 0 {
		msg.Content = append(msg.Content, core.TextContent{
			Type: "text",
			Text: textBuf.String(),
		})
		stream.Push(core.EventTextEnd{Type: "text_end"})
	}

	if thinkBuf.Len() > 0 {
		msg.Content = append(msg.Content, core.ThinkingContent{
			Type:     "thinking",
			Thinking: thinkBuf.String(),
		})
	}

	for _, tc := range toolCalls {
		stream.Push(core.EventToolCallEnd{
			Type:      "toolcall_end",
			ID:        tc.ID,
			Arguments: tc.Arguments,
		})
		msg.Content = append(msg.Content, tc)
	}

	msg.Usage.TotalTokens = msg.Usage.Input + msg.Usage.Output + msg.Usage.CacheRead + msg.Usage.CacheWrite
	msg.Usage.Cost = core.CalculateCost(model, msg.Usage)

	stream.Push(core.EventDone{
		Type:    "done",
		Message: msg,
	})

	return msg, nil
}

func mapBedrockStopReason(reason string) core.StopReason {
	switch reason {
	case "end_turn":
		return core.StopStop
	case "tool_use":
		return core.StopToolUse
	case "max_tokens":
		return core.StopLength
	case "stop_sequence":
		return core.StopStop
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
