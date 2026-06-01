// Package openai implements OpenAI-compatible API providers.
package openai

import (
	"encoding/json"

	core "crux-ai/core"
)

const defaultCompletionsURL = "https://api.openai.com/v1"
const defaultResponsesURL = "https://api.openai.com/v1"

// ConvertMessages converts internal messages to OpenAI Chat Completions format.
func ConvertMessages(messages []core.Message, model core.Model) ([]map[string]any, error) {
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
			openaiMsg := map[string]any{"role": "assistant"}
			content, toolCalls := convertAssistantContentSplit(m.Content)
			if len(content) == 1 {
				if textBlock, ok := content[0].(map[string]any); ok && textBlock["type"] == "text" {
					openaiMsg["content"] = textBlock["text"]
				} else if len(content) > 0 {
					openaiMsg["content"] = content
				}
			} else if len(content) > 0 {
				openaiMsg["content"] = content
			}
			if len(toolCalls) > 0 {
				openaiMsg["tool_calls"] = toolCalls
			}
			result = append(result, openaiMsg)
		case core.ToolResultMessage:
			result = append(result, map[string]any{
				"role":         "tool",
				"tool_call_id": m.ToolCallID,
				"content":      convertToolResultContent(m.Content),
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
					"type": "image_url",
					"image_url": map[string]any{
						"url": "data:" + b.MimeType + ";base64," + b.Data,
					},
				})
			}
		}
		return blocks, nil
	default:
		return c, nil
	}
}

func convertAssistantContent(content []core.ContentBlock) []any {
	blocks, toolCalls := convertAssistantContentSplit(content)
	if len(toolCalls) > 0 {
		blocks = append(blocks, toolCalls...)
	}
	return blocks
}

// convertAssistantContentSplit separates text/thinking content from tool calls.
// This is needed for the OpenAI Chat Completions format where tool_calls must be
// a separate top-level field, not inside content.
func convertAssistantContentSplit(content []core.ContentBlock) (blocks []any, toolCalls []any) {
	for _, block := range content {
		switch b := block.(type) {
		case core.TextContent:
			blocks = append(blocks, map[string]any{"type": "text", "text": b.Text})
		case core.ThinkingContent:
			blocks = append(blocks, map[string]any{
				"type": "reasoning_content",
				"reasoning_content": map[string]any{"text": b.Thinking},
			})
		case core.ToolCall:
			toolCalls = append(toolCalls, map[string]any{
				"id":   b.ID,
				"type": "function",
				"function": map[string]any{
					"name":      b.Name,
					"arguments": string(b.Arguments),
				},
			})
		}
	}
	return blocks, toolCalls
}

func convertToolResultContent(content []core.ContentBlock) string {
	var parts []string
	for _, block := range content {
		if text, ok := block.(core.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return joinStrings(parts, "\n")
}

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}

// ConvertTools converts tools to OpenAI format.
func ConvertTools(tools []core.Tool) []map[string]any {
	result := make([]map[string]any, len(tools))
	for i, tool := range tools {
		t := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
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

// MapStopReason maps OpenAI finish reasons to StopReason.
func MapStopReason(reason string) core.StopReason {
	switch reason {
	case "stop":
		return core.StopStop
	case "length":
		return core.StopLength
	case "tool_calls", "function_call":
		return core.StopToolUse
	default:
		return core.StopStop
	}
}
