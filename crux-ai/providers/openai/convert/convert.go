// Package convert holds the OpenAI Chat Completions message/tool converters.
// It is a leaf package with no internal dependencies so it can be imported
// by both the `openai` parent package and the shared `compat` package without
// creating an import cycle.
package convert

import (
	"encoding/json"

	core "crux-ai/core"
)

// Messages converts internal messages to OpenAI Chat Completions format.
func Messages(messages []core.Message, model core.Model) ([]map[string]any, error) {
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
			openaiMsg := map[string]any{"role": "assistant"}
			textBlocks, toolCalls := convertAssistantContent(m.Content)
			// Set content: plain string if single text block, nil if only tool calls.
			if len(textBlocks) == 1 {
				if textBlock, ok := textBlocks[0].(map[string]any); ok && textBlock["type"] == "text" {
					openaiMsg["content"] = textBlock["text"]
				} else {
					openaiMsg["content"] = textBlocks
				}
			} else if len(textBlocks) > 1 {
				openaiMsg["content"] = textBlocks
			} else {
				openaiMsg["content"] = nil
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
				blocks = append(blocks, map[string]any{
					"type": "text",
					"text": b.Text,
				})
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

func convertAssistantContent(content []core.ContentBlock) (textBlocks []any, toolCalls []any) {
	for _, block := range content {
		switch b := block.(type) {
		case core.TextContent:
			textBlocks = append(textBlocks, map[string]any{
				"type": "text",
				"text": b.Text,
			})
		case core.ThinkingContent:
			// ThinkingContent is request-scoped metadata. Do NOT include it
			// in the messages array: most providers (including DeepSeek)
			// reject `reasoning_content` in conversation history, and
			// reasoning tokens are not meant to be replayed across turns.
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
	return
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

// Tools converts tools to OpenAI format.
func Tools(tools []core.Tool) []map[string]any {
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

// StopReason maps OpenAI finish reasons to core.StopReason.
func StopReason(reason string) core.StopReason {
	switch reason {
	case "stop":
		return core.StopStop
	case "length":
		return core.StopLength
	case "tool_calls":
		return core.StopToolUse
	case "function_call":
		return core.StopToolUse
	default:
		return core.StopStop
	}
}
