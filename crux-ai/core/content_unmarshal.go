package core

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// UnmarshalContentBlocks 把 JSON 数组反序列化为 []ContentBlock。
// 根据每个元素的 "type" 字段选择正确的具名类型。
//
// Go 默认无法把 JSON 数组反序列化到 []ContentBlock（接口切片），
// 会丢失类型信息。本函数弥补这一点。
//
// 兼容的类型：
//   - "text"            → TextContent
//   - "thinking"        → ThinkingContent
//   - "image"           → ImageContent
//   - "tool_use"        → ToolCall
//   - "tool_result"     → ToolResultContent（若有）
//   - 未知 type         → TextContent，JSON 原始内容作为 text
//
// 用法：
//
//	var m core.AssistantMessage
//	json.Unmarshal(raw, &m) // Content 此时是 []map[string]any
//	if blocks, err := core.UnmarshalContentBlocks(rawContent); err == nil {
//	    m.Content = blocks
//	}
func UnmarshalContentBlocks(data []byte) ([]ContentBlock, error) {
	if len(bytes.TrimSpace(data)) == 0 || string(bytes.TrimSpace(data)) == "null" {
		return nil, nil
	}

	// 先解析成 []json.RawMessage
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("unmarshal content blocks: %w", err)
	}

	blocks := make([]ContentBlock, 0, len(raws))
	for _, raw := range raws {
		b, err := unmarshalSingleContentBlock(raw)
		if err != nil {
			// 单个块失败不中断整体流程，降级为 TextContent
			blocks = append(blocks, TextContent{
				Type: "text",
				Text: string(raw),
			})
			continue
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}

// UnmarshalContentBlock 把单个 JSON 对象反序列化为 ContentBlock。
func UnmarshalContentBlock(data []byte) (ContentBlock, error) {
	return unmarshalSingleContentBlock(data)
}

func unmarshalSingleContentBlock(data []byte) (ContentBlock, error) {
	// 先取 type 字段，决定具体类型
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}

	switch probe.Type {
	case "text", "":
		var c TextContent
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, err
		}
		return c, nil
	case "thinking":
		var c ThinkingContent
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, err
		}
		return c, nil
	case "image":
		var c ImageContent
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, err
		}
		return c, nil
	case "tool_use":
		var c ToolCall
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, err
		}
		return c, nil
	default:
		// 未知类型：作为 TextContent 保留原文
		return TextContent{
			Type: "text",
			Text: string(data),
		}, nil
	}
}

// UnmarshalJSON implements json.Unmarshaler for AssistantMessage.
// 解决 Go 无法直接反序列化 []ContentBlock 接口切片的问题。
func (m *AssistantMessage) UnmarshalJSON(data []byte) error {
	// 用别名类型避免递归调用 UnmarshalJSON
	type alias AssistantMessage
	aux := struct {
		*alias
		ContentRaw json.RawMessage `json:"content,omitempty"`
	}{
		alias: (*alias)(m),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.ContentRaw) == 0 {
		m.Content = nil
		return nil
	}
	blocks, err := UnmarshalContentBlocks(aux.ContentRaw)
	if err != nil {
		return err
	}
	m.Content = blocks
	return nil
}

// UnmarshalJSON implements json.Unmarshaler for ToolResultMessage.
func (m *ToolResultMessage) UnmarshalJSON(data []byte) error {
	type alias ToolResultMessage
	aux := struct {
		*alias
		ContentRaw json.RawMessage `json:"content,omitempty"`
	}{
		alias: (*alias)(m),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.ContentRaw) == 0 {
		m.Content = nil
		return nil
	}
	blocks, err := UnmarshalContentBlocks(aux.ContentRaw)
	if err != nil {
		return err
	}
	m.Content = blocks
	return nil
}
