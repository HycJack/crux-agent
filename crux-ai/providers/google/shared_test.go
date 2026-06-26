package google

import (
	"encoding/json"
	"testing"

	core "crux-ai/core"
)

// TestConvertToolResultPartsValidJSON 测试有效 JSON 直接返回
func TestConvertToolResultPartsValidJSON(t *testing.T) {
	content := []core.ContentBlock{
		core.TextContent{Type: "text", Text: `{"temperature": 25, "unit": "C"}`},
	}

	result := convertToolResultParts(content)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	if result["temperature"] != float64(25) {
		t.Errorf("Expected temperature 25, got: %v", result["temperature"])
	}
	if result["unit"] != "C" {
		t.Errorf("Expected unit 'C', got: %v", result["unit"])
	}
}

// TestConvertToolResultPartsInvalidJSON 测试无效 JSON 回退到 text
func TestConvertToolResultPartsInvalidJSON(t *testing.T) {
	content := []core.ContentBlock{
		core.TextContent{Type: "text", Text: "not valid json"},
	}

	result := convertToolResultParts(content)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	if result["text"] != "not valid json" {
		t.Errorf("Expected text 'not valid json', got: %v", result["text"])
	}
}

// TestConvertToolResultPartsValidJSONNotObject 测试有效 JSON 但不是对象
func TestConvertToolResultPartsValidJSONNotObject(t *testing.T) {
	content := []core.ContentBlock{
		core.TextContent{Type: "text", Text: `[1, 2, 3]`},
	}

	result := convertToolResultParts(content)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	if result["text"] != "[1, 2, 3]" {
		t.Errorf("Expected text '[1, 2, 3]', got: %v", result["text"])
	}
}

// TestConvertToolResultPartsEmpty 测试空内容
func TestConvertToolResultPartsEmpty(t *testing.T) {
	result := convertToolResultParts([]core.ContentBlock{})
	if result != nil {
		t.Errorf("Expected nil for empty content, got: %v", result)
	}
}

// TestConvertToolResultPartsNoTextBlocks 测试没有文本块
func TestConvertToolResultPartsNoTextBlocks(t *testing.T) {
	content := []core.ContentBlock{
		core.ImageContent{Type: "image", Data: "imgdata", MimeType: "image/png"},
	}

	result := convertToolResultParts(content)
	if result != nil {
		t.Errorf("Expected nil for non-text content, got: %v", result)
	}
}

// TestConvertToolResultPartsNestedJSON 测试嵌套 JSON
func TestConvertToolResultPartsNestedJSON(t *testing.T) {
	nested := map[string]any{
		"weather": map[string]any{
			"temp":    25,
			"humidity": 60,
		},
		"location": "Beijing",
	}
	jsonBytes, _ := json.Marshal(nested)
	content := []core.ContentBlock{
		core.TextContent{Type: "text", Text: string(jsonBytes)},
	}

	result := convertToolResultParts(content)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	location, ok := result["location"].(string)
	if !ok || location != "Beijing" {
		t.Errorf("Expected location 'Beijing', got: %v", result["location"])
	}
}

// TestConvertUserPartsString 测试字符串用户内容
func TestConvertUserPartsString(t *testing.T) {
	parts, err := convertUserParts("Hello")
	if err != nil {
		t.Fatalf("convertUserParts failed: %v", err)
	}

	if len(parts) != 1 {
		t.Fatalf("Expected 1 part, got %d", len(parts))
	}

	part, ok := parts[0].(map[string]any)
	if !ok {
		t.Fatal("Part is not a map")
	}
	if part["text"] != "Hello" {
		t.Errorf("Expected text 'Hello', got: %v", part["text"])
	}
}

// TestConvertUserPartsBlocks 测试多种内容块
func TestConvertUserPartsBlocks(t *testing.T) {
	content := []core.ContentBlock{
		core.TextContent{Type: "text", Text: "Hi"},
		core.ImageContent{Type: "image", Data: "imgdata", MimeType: "image/png"},
	}

	parts, err := convertUserParts(content)
	if err != nil {
		t.Fatalf("convertUserParts failed: %v", err)
	}

	if len(parts) != 2 {
		t.Errorf("Expected 2 parts, got %d", len(parts))
	}
}

// TestConvertAssistantParts 测试助手消息转换
func TestConvertAssistantParts(t *testing.T) {
	content := []core.ContentBlock{
		core.TextContent{Type: "text", Text: "Hello"},
		core.ThinkingContent{Type: "thinking", Thinking: "Let me think..."},
	}

	parts := convertAssistantParts(content)
	if len(parts) != 2 {
		t.Errorf("Expected 2 parts, got %d", len(parts))
	}

	thinkingPart, ok := parts[1].(map[string]any)
	if !ok {
		t.Fatal("Thinking part is not a map")
	}
	if thinkingPart["thought"] != true {
		t.Error("Expected thought to be true")
	}
}

// TestConvertAssistantPartsWithToolCall 测试带工具调用
func TestConvertAssistantPartsWithToolCall(t *testing.T) {
	content := []core.ContentBlock{
		core.ToolCall{
			Type:      "toolCall",
			ID:        "call_1",
			Name:      "get_weather",
			Arguments: json.RawMessage(`{"city": "BJ"}`),
		},
	}

	parts := convertAssistantParts(content)
	if len(parts) != 1 {
		t.Errorf("Expected 1 part, got %d", len(parts))
	}

	part, ok := parts[0].(map[string]any)
	if !ok {
		t.Fatal("Part is not a map")
	}
	if _, ok := part["functionCall"]; !ok {
		t.Error("Expected functionCall in part")
	}
}

// TestConvertTools 测试工具转换
func TestConvertTools(t *testing.T) {
	tools := []core.Tool{
		{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters:  json.RawMessage(`{"type": "object"}`),
		},
	}

	result := ConvertTools(tools)
	if len(result) != 1 {
		t.Fatalf("Expected 1 declaration, got %d", len(result))
	}

	decl, ok := result[0]["functionDeclarations"].([]map[string]any)
	if !ok {
		t.Fatal("Expected functionDeclarations")
	}
	if len(decl) != 1 {
		t.Errorf("Expected 1 function, got %d", len(decl))
	}
	if decl[0]["name"] != "get_weather" {
		t.Errorf("Expected name 'get_weather', got: %v", decl[0]["name"])
	}
}

// TestMapStopReason 测试 stop reason 映射
func TestMapStopReason(t *testing.T) {
	tests := []struct {
		input    string
		expected core.StopReason
	}{
		{"STOP", core.StopStop},
		{"MAX_TOKENS", core.StopLength},
		{"SAFETY", core.StopError},
		{"RECITATION", core.StopError},
		{"OTHER", core.StopError},
		{"UNKNOWN", core.StopStop},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := MapStopReason(tc.input)
			if got != tc.expected {
				t.Errorf("MapStopReason(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// TestIsThinkingPart 测试 thinking 块检测
func TestIsThinkingPart(t *testing.T) {
	thinkingPart := map[string]any{"thought": true, "text": "reasoning..."}
	if !IsThinkingPart(thinkingPart) {
		t.Error("Expected thinking part to be detected")
	}

	textPart := map[string]any{"text": "hello"}
	if IsThinkingPart(textPart) {
		t.Error("Text part should not be detected as thinking")
	}

	noThoughtKey := map[string]any{"other": "value"}
	if IsThinkingPart(noThoughtKey) {
		t.Error("Part without 'thought' key should not be detected as thinking")
	}
}

// TestConvertMessagesEmpty 测试空消息
func TestConvertMessagesEmpty(t *testing.T) {
	result, err := ConvertMessages([]core.Message{})
	if err != nil {
		t.Fatalf("ConvertMessages failed: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(result))
	}
}

// TestConvertMessagesUserWithString 测试用户消息字符串内容
func TestConvertMessagesUserWithString(t *testing.T) {
	msgs := []core.Message{
		core.UserMessage{Content: "Hello"},
	}

	result, err := ConvertMessages(msgs)
	if err != nil {
		t.Fatalf("ConvertMessages failed: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(result))
	}
	if result[0]["role"] != "user" {
		t.Errorf("Expected role 'user', got: %v", result[0]["role"])
	}
}

// TestConvertMessagesToolResult 测试工具结果消息
func TestConvertMessagesToolResult(t *testing.T) {
	msgs := []core.Message{
		core.ToolResultMessage{
			ToolCallID: "call_1",
			ToolName:   "get_weather",
			Content: []core.ContentBlock{
				core.TextContent{Type: "text", Text: `{"temp": 25}`},
			},
		},
	}

	result, err := ConvertMessages(msgs)
	if err != nil {
		t.Fatalf("ConvertMessages failed: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(result))
	}
	if result[0]["role"] != "user" {
		t.Errorf("Expected role 'user' for tool result, got: %v", result[0]["role"])
	}
}
