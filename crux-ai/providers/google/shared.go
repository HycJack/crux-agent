// Package google implements Google Generative AI (Gemini) API providers.
package google

import (
	"encoding/json"
	"strings"

	core "crux-ai/core"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com"

// ConvertMessages converts internal messages to Google Gemini format.
func ConvertMessages(messages []core.Message) ([]map[string]any, error) {
	var result []map[string]any
	for _, msg := range messages {
		switch m := msg.(type) {
		case core.UserMessage:
			parts, err := convertUserParts(m.Content)
			if err != nil {
				return nil, err
			}
			result = append(result, map[string]any{"role": "user", "parts": parts})
		case core.AssistantMessage:
			parts := convertAssistantParts(m.Content)
			result = append(result, map[string]any{"role": "model", "parts": parts})
		case core.ToolResultMessage:
			parts := convertToolResultParts(m.Content)
			result = append(result, map[string]any{
				"role": "user",
				"parts": []any{
					map[string]any{
						"functionResponse": map[string]any{
							"name":     m.ToolName,
							"response": parts,
						},
					},
				},
			})
		}
	}
	return result, nil
}

func convertUserParts(content any) ([]any, error) {
	switch c := content.(type) {
	case string:
		return []any{map[string]any{"text": c}}, nil
	case []core.ContentBlock:
		var parts []any
		for _, block := range c {
			switch b := block.(type) {
			case core.TextContent:
				parts = append(parts, map[string]any{"text": b.Text})
			case core.ImageContent:
				parts = append(parts, map[string]any{
					"inlineData": map[string]any{"mimeType": b.MimeType, "data": b.Data},
				})
			}
		}
		return parts, nil
	default:
		return []any{map[string]any{"text": strings.TrimSpace(stringify(c))}}, nil
	}
}

func convertAssistantParts(content []core.ContentBlock) []any {
	var parts []any
	for _, block := range content {
		switch b := block.(type) {
		case core.TextContent:
			parts = append(parts, map[string]any{"text": b.Text})
		case core.ThinkingContent:
			parts = append(parts, map[string]any{"thought": true, "text": b.Thinking})
		case core.ToolCall:
			parts = append(parts, map[string]any{
				"functionCall": map[string]any{"name": b.Name, "args": json.RawMessage(b.Arguments)},
			})
		}
	}
	return parts
}

func convertToolResultParts(content []core.ContentBlock) map[string]any {
	result := make(map[string]any)
	for _, block := range content {
		if text, ok := block.(core.TextContent); ok {
			var v any
			if err := json.Unmarshal([]byte(text.Text), &v); err == nil {
				if m, ok := v.(map[string]any); ok {
					return m
				}
			}
			result["text"] = text.Text
		}
	}
	return result
}

// ConvertTools converts tools to Google Gemini format.
func ConvertTools(tools []core.Tool) []map[string]any {
	declarations := make([]map[string]any, len(tools))
	for i, tool := range tools {
		d := map[string]any{"name": tool.Name}
		if tool.Description != "" {
			d["description"] = tool.Description
		}
		if len(tool.Parameters) > 0 {
			var params map[string]any
			if err := json.Unmarshal(tool.Parameters, &params); err == nil {
				d["parameters"] = params
			}
		}
		declarations[i] = d
	}
	return []map[string]any{{"functionDeclarations": declarations}}
}

// MapStopReason maps Google finish reasons to StopReason.
func MapStopReason(reason string) core.StopReason {
	switch reason {
	case "STOP":
		return core.StopStop
	case "MAX_TOKENS":
		return core.StopLength
	case "SAFETY", "RECITATION", "OTHER":
		return core.StopError
	default:
		return core.StopStop
	}
}

// IsThinkingPart checks if a part is a thinking/reasoning part.
func IsThinkingPart(part map[string]any) bool {
	if thought, ok := part["thought"].(bool); ok {
		return thought
	}
	return false
}

func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}
