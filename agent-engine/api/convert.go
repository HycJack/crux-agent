package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	core "github.com/hycjack/crux-ai/core"
)

// ─── Request Conversion: OpenAI → core.Context ─────────────────────────────

// ParseChatRequest converts an OpenAI-compatible chat request into a
// core.Context suitable for passing to crux-ai or agent-engine.
//
// It also returns extracted SimpleStreamOptions, the model name, and
// whether streaming was requested.
func ParseChatRequest(req *ChatRequest, defaultModel core.Model) (core.Context, core.SimpleStreamOptions, error) {
	var ctx core.Context
	var opts core.SimpleStreamOptions

	// System prompt: extract from messages with role "system"
	systemParts := extractSystemMessages(req.Messages)
	if len(systemParts) > 0 {
		ctx.SystemPrompt = strings.Join(systemParts, "\n\n")
	}

	// Convert messages
	messages, err := convertMessages(req.Messages)
	if err != nil {
		return ctx, opts, fmt.Errorf("convert messages: %w", err)
	}
	ctx.Messages = messages

	// Convert tools
	if req.Tools != nil {
		tools, err := convertTools(req.Tools)
		if err != nil {
			return ctx, opts, fmt.Errorf("convert tools: %w", err)
		}
		ctx.Tools = tools
	}

	// Stream options
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		opts.MaxTokens = req.MaxTokens
	} else if defaultModel.MaxTokens > 0 {
		mt := defaultModel.MaxTokens
		opts.MaxTokens = &mt
	}

	if req.Temperature != nil {
		opts.Temperature = req.Temperature
	}

	if req.ReasoningEffort != "" {
		opts.Reasoning = mapReasoningEffort(req.ReasoningEffort)
	}

	return ctx, opts, nil
}

// extractSystemMessages extracts system prompt text from messages.
func extractSystemMessages(msgs []ChatMessage) []string {
	var parts []string
	for _, m := range msgs {
		if m.Role == "system" {
			text := extractTextContent(m.Content)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return parts
}

// convertMessages translates OpenAI-format messages to core.Message.
func convertMessages(msgs []ChatMessage) ([]core.Message, error) {
	var result []core.Message
	now := time.Now()

	for _, m := range msgs {
		switch m.Role {
		case "user":
			msg, err := convertUserMessage(m, now)
			if err != nil {
				return nil, err
			}
			result = append(result, msg)

		case "assistant":
			msg, err := convertAssistantMessage(m, now)
			if err != nil {
				return nil, err
			}
			result = append(result, msg)

		case "tool":
			msg, err := convertToolResultMessage(m, now)
			if err != nil {
				return nil, err
			}
			result = append(result, msg)
		}
	}

	return result, nil
}

func convertUserMessage(m ChatMessage, now time.Time) (core.UserMessage, error) {
	msg := core.UserMessage{Role: core.MessageRoleUser, Timestamp: now}

	// Try parsing as string first
	var text string
	if err := json.Unmarshal(m.Content, &text); err == nil {
		msg.Content = text
		return msg, nil
	}

	// Try parsing as array of content parts
	var parts []ContentPart
	if err := json.Unmarshal(m.Content, &parts); err != nil {
		return msg, fmt.Errorf("user message content: expected string or array, got %s", string(m.Content))
	}

	// Convert content blocks
	var blocks []core.ContentBlock
	for _, p := range parts {
		switch p.Type {
		case "text":
			blocks = append(blocks, core.TextContent{Type: "text", Text: p.Text})
		case "image_url":
			if p.ImageURL != nil {
				// Extract base64 data from data: URL
				data, mimeType := parseDataURL(p.ImageURL.URL)
				if data == "" {
					// It's an external URL — store as URL, not base64
					blocks = append(blocks, core.ImageContent{
						Type:     "image",
						Data:     p.ImageURL.URL,
						MimeType: "url",
					})
				} else {
					blocks = append(blocks, core.ImageContent{
						Type:     "image",
						Data:     data,
						MimeType: mimeType,
					})
				}
			}
		}
	}

	if len(blocks) > 0 {
		msg.Content = blocks
	} else {
		// Fall back to raw JSON string
		msg.Content = string(m.Content)
	}

	return msg, nil
}

func convertAssistantMessage(m ChatMessage, now time.Time) (core.AssistantMessage, error) {
	msg := core.AssistantMessage{
		Role:      core.MessageRoleAssistant,
		Timestamp: now,
	}

	// Extract text content
	text := extractTextContent(m.Content)
	if text != "" {
		msg.Content = append(msg.Content, core.TextContent{Type: "text", Text: text})
	}

	// Extract tool calls
	if m.ToolCalls != nil {
		var toolCalls []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}
		if err := json.Unmarshal(m.ToolCalls, &toolCalls); err != nil {
			return msg, fmt.Errorf("assistant tool_calls: %w", err)
		}
		for _, tc := range toolCalls {
			msg.Content = append(msg.Content, core.ToolCall{
				Type: "toolCall", ID: tc.ID, Name: tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			})
		}
	}

	if len(msg.Content) > 0 {
		msg.StopReason = core.StopStop
		if len(toolCalls(msg.Content)) > 0 {
			msg.StopReason = core.StopToolUse
		}
	}

	return msg, nil
}

func convertToolResultMessage(m ChatMessage, now time.Time) (core.ToolResultMessage, error) {
	text := extractTextContent(m.Content)
	return core.ToolResultMessage{
		Role:       core.MessageRoleTool,
		ToolCallID: m.ToolCallID,
		Content: []core.ContentBlock{
			core.TextContent{Type: "text", Text: text},
		},
		Timestamp: now,
	}, nil
}

// convertTools converts OpenAI tool definitions to core.Tool.
func convertTools(raw json.RawMessage) ([]core.Tool, error) {
	var openAITools []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &openAITools); err != nil {
		return nil, fmt.Errorf("tools: %w", err)
	}

	tools := make([]core.Tool, 0, len(openAITools))
	for _, t := range openAITools {
		tools = append(tools, core.Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}
	return tools, nil
}

// ─── Response Conversion: core.AssistantMessage → OpenAI Format ────────────

// ToChatResponse converts a core.AssistantMessage to a non-streaming
// OpenAI ChatResponse.
func ToChatResponse(msg core.AssistantMessage, modelName string, responseID string) ChatResponse {
	resp := ChatResponse{
		ID:      responseID,
		Object:  "chat.completion",
		Created: msg.Timestamp.Unix(),
		Model:   modelName,
		Choices: []ResponseChoice{
			{
				Index:        0,
				Message:      toResponseMessage(msg),
				FinishReason: mapStopReason(msg.StopReason),
			},
		},
	}

	if msg.Usage.TotalTokens > 0 {
		resp.Usage = &UsageInfo{
			PromptTokens:     msg.Usage.Input,
			CompletionTokens: msg.Usage.Output,
			TotalTokens:      msg.Usage.TotalTokens,
		}
	}

	return resp
}

// toResponseMessage converts an AssistantMessage to ResponseMessage.
func toResponseMessage(msg core.AssistantMessage) ResponseMessage {
	rm := ResponseMessage{Role: "assistant"}

	var textParts []string
	var toolCalls []ResponseToolCall

	for _, block := range msg.Content {
		switch b := block.(type) {
		case core.TextContent:
			textParts = append(textParts, b.Text)
		case core.ToolCall:
			toolCalls = append(toolCalls, ResponseToolCall{
				ID:   b.ID,
				Type: "function",
				Function: ResponseFunction{
					Name:      b.Name,
					Arguments: string(b.Arguments),
				},
			})
		}
	}

	if len(textParts) > 0 {
		content := strings.Join(textParts, "")
		rm.Content = &content
	}

	if len(toolCalls) > 0 {
		rm.ToolCalls = toolCalls
	}

	return rm
}

// ─── Delta Builder (for SSE streaming) ──────────────────────────────────────

// StreamDeltaBuilder accumulates streaming events and produces SSE chunks.
type StreamDeltaBuilder struct {
	Model          string
	ResponseID     string
	CreatedAt      int64
	roleSent       bool
	textBuf        strings.Builder
	thinkBuf       strings.Builder
	thinkOpen      bool
	toolCallOpen   map[int]bool
	toolCallBuffer map[int]*sseToolCallState
	toolIndices    []int
}

type sseToolCallState struct {
	id    string
	name  string
	args  strings.Builder
}

// NewStreamDeltaBuilder creates a new delta builder.
func NewStreamDeltaBuilder(model, responseID string) *StreamDeltaBuilder {
	return &StreamDeltaBuilder{
		Model:          model,
		ResponseID:     responseID,
		CreatedAt:      time.Now().Unix(),
		toolCallOpen:   make(map[int]bool),
		toolCallBuffer: make(map[int]*sseToolCallState),
	}
}

// OnEvent processes an AssistantMessageEvent and produces SSE chunks.
// Returns nil when no chunk should be emitted.
func (b *StreamDeltaBuilder) OnEvent(evt core.AssistantMessageEvent) *SSEChatChunk {
	switch e := evt.(type) {
	case core.EventStart:
		// First chunk with role
		return b.makeChunk(SSEDelta{Role: "assistant"})

	case core.EventTextDelta:
		b.textBuf.WriteString(e.Delta)
		return b.makeChunk(SSEDelta{Content: e.Delta})

	case core.EventThinkingDelta:
		if !b.thinkOpen {
			b.thinkOpen = true
		}
		b.thinkBuf.WriteString(e.Delta)
		return b.makeChunk(SSEDelta{ReasoningContent: e.Delta})

	case core.EventToolCallStart:
		b.toolCallOpen[e.ContentIndex] = true
		b.toolCallBuffer[e.ContentIndex] = &sseToolCallState{id: e.ID, name: e.Name}
		b.toolIndices = append(b.toolIndices, e.ContentIndex)
		return b.makeChunk(SSEDelta{
			ToolCalls: []SSEToolCallDelta{{
				Index: e.ContentIndex,
				ID:    e.ID,
				Type:  "function",
				Function: &SSEFunctionDelta{
					Name: e.Name,
				},
			}},
		})

	case core.EventToolCallDelta:
		if tc, ok := b.toolCallBuffer[e.ContentIndex]; ok {
			tc.args.WriteString(e.ArgumentsDelta)
			return b.makeChunk(SSEDelta{
				ToolCalls: []SSEToolCallDelta{{
					Index: e.ContentIndex,
					Function: &SSEFunctionDelta{
						Arguments: e.ArgumentsDelta,
					},
				}},
			})
		}

	case core.EventToolCallEnd:
		return nil // tool call complete, no delta needed

	case core.EventTextEnd:
	case core.EventThinkingEnd:
		b.thinkOpen = false

	case core.EventDone:
		// Final chunk with finish reason
		fr := mapStopReason(e.Reason)
		return &SSEChatChunk{
			ID:      b.ResponseID,
			Object:  "chat.completion.chunk",
			Created: b.CreatedAt,
			Model:   b.Model,
			Choices: []SSEChoice{{
				Index:        0,
				Delta:        SSEDelta{},
				FinishReason: &fr,
			}},
		}
	}

	return nil
}

func (b *StreamDeltaBuilder) makeChunk(delta SSEDelta) *SSEChatChunk {
	return &SSEChatChunk{
		ID:      b.ResponseID,
		Object:  "chat.completion.chunk",
		Created: b.CreatedAt,
		Model:   b.Model,
		Choices: []SSEChoice{{
			Index: 0,
			Delta: delta,
		}},
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// extractTextContent extracts text from an OpenAI content field (string or array).
func extractTextContent(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}

	// Try string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// Try array of content parts
	var parts []ContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		var texts []string
		for _, p := range parts {
			if p.Type == "text" {
				texts = append(texts, p.Text)
			}
		}
		return strings.Join(texts, "")
	}

	return ""
}

// toolCalls filters ToolCall blocks from content.
func toolCalls(blocks []core.ContentBlock) []core.ToolCall {
	var calls []core.ToolCall
	for _, b := range blocks {
		if tc, ok := b.(core.ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}

// mapStopReason converts internal StopReason to OpenAI finish_reason.
func mapStopReason(reason core.StopReason) string {
	switch reason {
	case core.StopStop:
		return "stop"
	case core.StopLength:
		return "length"
	case core.StopToolUse:
		return "tool_calls"
	case core.StopError:
		return "error"
	default:
		return "stop"
	}
}

// mapReasoningEffort converts OpenAI reasoning_effort to ThinkingLevel.
func mapReasoningEffort(effort string) core.ThinkingLevel {
	switch strings.ToLower(effort) {
	case "low":
		return core.ThinkingLow
	case "medium":
		return core.ThinkingMedium
	case "high":
		return core.ThinkingHigh
	default:
		return core.ThinkingMedium
	}
}

// parseDataURL extracts base64 data and mime type from a data: URL.
func parseDataURL(url string) (data string, mimeType string) {
	if !strings.HasPrefix(url, "data:") {
		return "", ""
	}
	// data:[<mediatype>][;base64],<data>
	comma := strings.IndexByte(url, ',')
	if comma < 0 {
		return "", ""
	}
	header := url[5:comma]
	data = url[comma+1:]
	parts := strings.Split(header, ";")
	for _, p := range parts {
		if p == "base64" {
			continue
		}
		if mimeType == "" {
			mimeType = p
		}
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
	return data, mimeType
}
