// Package openai implements OpenAI-compatible API providers.
//
// The conversion helpers (ConvertMessages, ConvertTools, MapStopReason) live
// in the leaf sub-package `providers/openai/convert`. The streaming engine
// shared by all OpenAI-compat providers lives in `providers/compat`.
//
// This file re-exports the public converters (so the pre-refactor test
// suite and external callers keep working) and provides a handful of
// package-internal helpers used by the Responses / Codex providers.
package openai

import (
	"strings"

	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/providers/openai/convert"
)

const defaultCompletionsURL = "https://api.openai.com/v1"
const defaultResponsesURL = "https://api.openai.com/v1"

// ConvertMessages is a backward-compatible alias for convert.Messages.
func ConvertMessages(messages []core.Message, model core.Model) ([]map[string]any, error) {
	return convert.Messages(messages, model)
}

// ConvertTools is a backward-compatible alias for convert.Tools.
func ConvertTools(tools []core.Tool) []map[string]any {
	return convert.Tools(tools)
}

// MapStopReason is a backward-compatible alias for convert.StopReason.
func MapStopReason(reason string) core.StopReason {
	return convert.StopReason(reason)
}

// --- package-internal helpers used by the Responses / Codex providers ---

// convertUserContent wraps convert.convertUserContent for use within this
// package (the convert package's helper is unexported).
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
		return c, nil
	}
}

// convertToolResultContent joins tool result text content into a single
// string (newline-separated).
func convertToolResultContent(content []core.ContentBlock) string {
	var parts []string
	for _, block := range content {
		if text, ok := block.(core.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// getFloat retrieves a possibly-nested float value from a JSON-decoded map.
func getFloat(m map[string]any, key string) float64 {
	keys := strings.Split(key, ".")
	current := m
	for i, k := range keys {
		if i == len(keys)-1 {
			if v, ok := current[k]; ok {
				if f, ok := v.(float64); ok {
					return f
				}
			}
			return 0
		}
		next, ok := current[k].(map[string]any)
		if !ok {
			return 0
		}
		current = next
	}
	return 0
}

// clampEffort clamps "xhigh" to "high" for providers that don't support it.
func clampEffort(effort core.ThinkingLevel) core.ThinkingLevel {
	if effort == core.ThinkingXHigh {
		return core.ThinkingHigh
	}
	return effort
}
