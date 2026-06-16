/*
 * 功能说明：工具调用参数验证工具
 *
 * 解决的问题：
 * 1. LLM 返回的工具调用参数可能不符合工具定义的 schema
 * 2. 需要验证工具调用的参数是否正确（必填字段、类型匹配等）
 * 3. 需要在执行工具前检测参数错误，避免运行时异常
 * 4. 需要提供清晰的错误信息，便于调试和用户反馈
 *
 * 解决方案：
 * 1. 使用 JSON Schema 标准进行参数验证
 * 2. 支持自动类型转换（如字符串转数字、数字转字符串）
 * 3. 当 JSON Schema 编译失败时，退化为简单的必填字段检查
 * 4. 返回结构化的验证结果，包含错误字段和错误消息
 *
 * 应用场景：
 * - AI Agent 在执行工具调用前验证参数
 * - 工具定义的参数校验
 * - 错误信息的标准化输出
 */
// Package validation provides tool call argument validation.
// || 提供工具调用参数验证功能
package validation

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidationError represents a tool call validation error.
// || 表示工具调用验证错误
type ValidationError struct {
	Field   string `json:"field"`   // 错误字段名
	Message string `json:"message"` // 错误消息
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("validation error on %s: %s", e.Field, e.Message)
}

// ValidationResult contains the result of validating tool arguments.
// || 包含工具参数验证的结果
type ValidationResult struct {
	Valid  bool              `json:"valid"`            // 是否验证通过
	Errors []ValidationError `json:"errors,omitempty"` // 错误列表（验证失败时）
}

// ValidateToolCall validates a tool call against its tool definition.
// || 验证工具调用是否符合工具定义
// 参数：
//
//	tools - 工具定义列表
//	call - 待验证的工具调用
//
// 返回：
//
//	匹配的工具定义和验证结果
func ValidateToolCall(tools []ToolDef, call ToolCall) (*ToolDef, ValidationResult) {
	// Find the tool by name
	// || 按名称查找工具
	var tool *ToolDef
	for i := range tools {
		if tools[i].Name == call.Name {
			tool = &tools[i]
			break
		}
	}

	// Tool not found
	// || 工具未找到
	if tool == nil {
		return nil, ValidationResult{
			Valid: false,
			Errors: []ValidationError{{
				Field:   "name",
				Message: fmt.Sprintf("unknown tool: %s", call.Name),
			}},
		}
	}

	// Validate arguments against tool schema
	// || 验证参数是否符合工具 schema
	result := ValidateArguments(tool, call.Arguments)
	return tool, result
}

// ToolDef represents a tool definition for validation.
// || 表示用于验证的工具定义
type ToolDef struct {
	Name        string          `json:"name"`        // 工具名称
	Description string          `json:"description"` // 工具描述
	Parameters  json.RawMessage `json:"parameters"`  // 参数 JSON Schema
}

// ToolCall represents a tool call for validation.
// || 表示待验证的工具调用
type ToolCall struct {
	Name      string          `json:"name"`      // 工具名称
	Arguments json.RawMessage `json:"arguments"` // 调用参数（JSON 格式）
}

// ValidateArguments validates arguments against a tool's parameter schema.
// || 验证参数是否符合工具的参数 schema
func ValidateArguments(tool *ToolDef, arguments json.RawMessage) ValidationResult {
	// No parameters defined - skip validation
	// || 没有定义参数 - 跳过验证
	if len(tool.Parameters) == 0 || string(tool.Parameters) == "null" {
		return ValidationResult{Valid: true}
	}

	// Parse the schema
	// || 解析 schema
	var schemaMap map[string]any
	if err := json.Unmarshal(tool.Parameters, &schemaMap); err != nil {
		return ValidationResult{
			Valid: false,
			Errors: []ValidationError{{
				Field:   "parameters",
				Message: "invalid JSON schema: " + err.Error(),
			}},
		}
	}

	// Parse the arguments
	// || 解析参数
	var argsMap map[string]any
	if err := json.Unmarshal(arguments, &argsMap); err != nil {
		return ValidationResult{
			Valid: false,
			Errors: []ValidationError{{
				Field:   "arguments",
				Message: "invalid JSON: " + err.Error(),
			}},
		}
	}

	// Try to coerce types (LLM may return wrong types)
	// || 尝试类型转换（LLM 可能返回错误的类型）
	coerceTypes(argsMap, schemaMap)

	// Validate using JSON Schema
	// || 使用 JSON Schema 进行验证
	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", schemaMap); err != nil {
		// Fallback to simple validation
		// || 回退到简单验证
		return validateSimple(argsMap, schemaMap)
	}
	schema, err := c.Compile("schema.json")
	if err != nil {
		// Fallback to simple validation
		// || 回退到简单验证
		return validateSimple(argsMap, schemaMap)
	}

	if err := schema.Validate(argsMap); err != nil {
		return ValidationResult{
			Valid: false,
			Errors: []ValidationError{{
				Field:   "arguments",
				Message: err.Error(),
			}},
		}
	}

	return ValidationResult{Valid: true}
}

// coerceTypes attempts to coerce argument values to match expected types.
// || 尝试将参数值强制转换为期望的类型
func coerceTypes(args map[string]any, schema map[string]any) {
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return
	}

	for key, val := range args {
		propSchema, ok := props[key].(map[string]any)
		if !ok {
			continue
		}
		expectedType, _ := propSchema["type"].(string)
		args[key] = coerceValue(val, expectedType)
	}
}

// coerceValue coerces a single value to the expected type.
// || 将单个值强制转换为期望的类型
func coerceValue(val any, expectedType string) any {
	if val == nil {
		return val
	}

	switch expectedType {
	case "string":
		switch v := val.(type) {
		case float64:
			// Convert number to string (integers without decimal point)
			// || 将数字转换为字符串（整数不带小数点）
			if v == float64(int64(v)) {
				return strconv.FormatInt(int64(v), 10)
			}
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		}
	case "number", "integer":
		switch v := val.(type) {
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				if expectedType == "integer" {
					return int64(f)
				}
				return f
			}
		case bool:
			if v {
				return int64(1)
			}
			return int64(0)
		}
	case "boolean":
		switch v := val.(type) {
		case string:
			lower := strings.ToLower(v)
			if lower == "true" || lower == "1" {
				return true
			}
			if lower == "false" || lower == "0" {
				return false
			}
		case float64:
			return v != 0
		}
	}
	return val
}

// validateSimple performs basic validation when JSON Schema compilation fails.
// || 当 JSON Schema 编译失败时执行基本验证（仅检查必填字段）
func validateSimple(args map[string]any, schema map[string]any) ValidationResult {
	required, ok := schema["required"].([]any)
	if !ok {
		return ValidationResult{Valid: true}
	}

	var errors []ValidationError
	for _, req := range required {
		name, ok := req.(string)
		if !ok {
			continue
		}
		if _, exists := args[name]; !exists {
			errors = append(errors, ValidationError{
				Field:   name,
				Message: "required field missing",
			})
		}
	}

	if len(errors) > 0 {
		return ValidationResult{Valid: false, Errors: errors}
	}
	return ValidationResult{Valid: true}
}
