package validation

import (
	"encoding/json"
	"testing"
)

func TestValidateToolCallValid(t *testing.T) {
	tools := []ToolDef{
		{
			Name:        "get_weather",
			Description: "Get weather for a location",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"location": {"type": "string"}
				},
				"required": ["location"]
			}`),
		},
	}

	call := ToolCall{
		Name:      "get_weather",
		Arguments: json.RawMessage(`{"location": "NYC"}`),
	}

	tool, result := ValidateToolCall(tools, call)
	if !result.Valid {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
	if tool == nil {
		t.Error("expected tool to be found")
	}
}

func TestValidateToolCallUnknownTool(t *testing.T) {
	tools := []ToolDef{
		{Name: "known_tool"},
	}

	call := ToolCall{
		Name:      "unknown_tool",
		Arguments: json.RawMessage(`{}`),
	}

	tool, result := ValidateToolCall(tools, call)
	if result.Valid {
		t.Error("expected invalid for unknown tool")
	}
	if tool != nil {
		t.Error("expected nil tool")
	}
}

func TestValidateToolCallMissingRequired(t *testing.T) {
	tools := []ToolDef{
		{
			Name: "test_tool",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"required_field": {"type": "string"}
				},
				"required": ["required_field"]
			}`),
		},
	}

	call := ToolCall{
		Name:      "test_tool",
		Arguments: json.RawMessage(`{"other_field": "value"}`),
	}

	_, result := ValidateToolCall(tools, call)
	if result.Valid {
		t.Error("expected invalid for missing required field")
	}
}

func TestValidateToolCallTypeCoercion(t *testing.T) {
	tools := []ToolDef{
		{
			Name: "test_tool",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"count": {"type": "number"},
					"active": {"type": "boolean"}
				}
			}`),
		},
	}

	call := ToolCall{
		Name:      "test_tool",
		Arguments: json.RawMessage(`{"count": "42", "active": "true"}`),
	}

	_, result := ValidateToolCall(tools, call)
	if !result.Valid {
		t.Errorf("expected valid after coercion, got errors: %v", result.Errors)
	}
}

func TestValidateArgumentsNoParams(t *testing.T) {
	tool := &ToolDef{
		Name: "no_params",
	}

	result := ValidateArguments(tool, json.RawMessage(`{}`))
	if !result.Valid {
		t.Errorf("expected valid for tool with no params, got errors: %v", result.Errors)
	}
}

func TestValidateArgumentsInvalidJSON(t *testing.T) {
	tool := &ToolDef{
		Name: "test",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {"x": {"type": "string"}}
		}`),
	}

	result := ValidateArguments(tool, json.RawMessage(`not json`))
	if result.Valid {
		t.Error("expected invalid for bad JSON")
	}
}
