/*
 * 功能说明：诊断工具包
 *
 * 解决的问题：
 * 1. 需要在消息处理过程中记录诊断事件
 * 2. 需要统一的诊断数据结构来追踪处理状态
 * 3. 需要安全地格式化任意值作为错误报告
 * 4. 需要区分普通诊断和错误诊断
 *
 * 解决方案：
 * 1. 定义 Diagnostic 结构体，包含类型、时间戳、错误信息和详细信息
 * 2. 提供 New 和 NewWithError 两个构造函数
 * 3. 提供 ExtractError 函数提取错误信息
 * 4. 提供 FormatThrownValue 函数安全格式化任意值
 *
 * 应用场景：
 * - 技能加载过程中的诊断记录
 * - 工具执行过程中的错误追踪
 * - 消息处理流程的状态监控
 */
// Package diagnostics provides diagnostic utilities for tracking message processing.
// || 提供诊断工具，用于跟踪消息处理过程
package diagnostics

import (
	"fmt"
	"time"
)

// Diagnostic represents a diagnostic event during message processing.
// || 表示消息处理过程中的诊断事件
type Diagnostic struct {
	Type      string    `json:"type"`      // 诊断类型
	Timestamp time.Time `json:"timestamp"` // 时间戳
	Error     string    `json:"error"`     // 错误信息（可选）
	Details   any       `json:"details"`   // 详细信息（可选）
}

// New creates a new diagnostic with the given type and optional details.
// || 创建一个新的诊断事件，包含类型和可选的详细信息
// 参数：
//
//	diagType - 诊断类型
//	details - 可选的详细信息（只取第一个）
//
// 返回：
//
//	新创建的 Diagnostic
func New(diagType string, details ...any) Diagnostic {
	d := Diagnostic{
		Type:      diagType,
		Timestamp: time.Now(),
	}
	if len(details) > 0 {
		d.Details = details[0]
	}
	return d
}

// NewWithError creates a new diagnostic with an error.
// || 创建一个包含错误信息的诊断事件
// 参数：
//
//	diagType - 诊断类型
//	err - 错误对象
//
// 返回：
//
//	包含错误信息的 Diagnostic
func NewWithError(diagType string, err error) Diagnostic {
	return Diagnostic{
		Type:      diagType,
		Timestamp: time.Now(),
		Error:     err.Error(),
	}
}

// ExtractError returns the error string from a diagnostic, or empty string.
// || 从诊断事件中提取错误字符串，无错误时返回空字符串
// 参数：
//
//	d - Diagnostic 诊断事件
//
// 返回：
//
//	错误字符串或空字符串
func ExtractError(d Diagnostic) string {
	return d.Error
}

// FormatThrownValue safely formats any value as a string for error reporting.
// || 安全地将任意值格式化为字符串，用于错误报告
// 参数：
//
//	v - 任意类型的值
//
// 返回：
//
//	格式化后的字符串
func FormatThrownValue(v any) string {
	if v == nil {
		return "<nil>"
	}
	switch val := v.(type) {
	case error:
		return val.Error()
	case string:
		return val
	case fmt.Stringer:
		return val.String()
	default:
		return fmt.Sprintf("%v", val)
	}
}
