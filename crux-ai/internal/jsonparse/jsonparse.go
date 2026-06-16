/*
 * 功能说明：JSON 解析修复工具包
 *
 * 解决的问题：
 * 1. LLM（大语言模型）返回的 JSON 数据可能格式不规范（缺少括号、转义错误等）
 * 2. SSE 流式传输中可能收到不完整的 JSON 数据
 * 3. 日志或其他数据源中的 JSON 可能包含控制字符或无效转义
 *
 * 解决方案：
 * 1. Parse 函数：先尝试标准解析，失败后自动修复再解析
 * 2. Repair 函数：修复控制字符和无效转义序列
 * 3. Streaming 函数：支持解析不完整的流式 JSON，多阶段尝试解析策略
 * 4. completeJSON 函数：自动补全未闭合的括号结构
 *
 * 应用场景：
 * - AI Agent 工具调用：解析 LLM 返回的工具调用 JSON
 * - SSE 流式响应处理：处理流式传输中的部分 JSON
 * - 日志数据解析：修复损坏的 JSON 日志
 */
package jsonparse

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Parse 智能解析 JSON 数据
// 先尝试标准 JSON 解析，失败时自动调用 Repair 修复后重试
// 参数：
//
//	data - 待解析的 JSON 字符串
//
// 返回：
//
//	解析后的目标类型 T 和错误信息
func Parse[T any](data string) (T, error) {
	var zero T
	var result T
	// 尝试标准解析
	if err := json.Unmarshal([]byte(data), &result); err == nil {
		return result, nil
	}
	// 标准解析失败，尝试修复后再解析
	repaired := Repair(data)
	if err := json.Unmarshal([]byte(repaired), &result); err != nil {
		return zero, err
	}
	return result, nil
}

// Repair 修复常见的 JSON 格式问题
// 组合调用 fixControlChars 和 fixBadEscapes 两个修复函数
// 参数：
//
//	s - 待修复的 JSON 字符串
//
// 返回：
//
//	修复后的 JSON 字符串
func Repair(s string) string {
	// 修复字符串中的控制字符（如 \n, \r, \t）
	s = fixControlChars(s)
	// 修复无效的转义序列
	s = fixBadEscapes(s)
	return s
}

// fixControlChars 转义 JSON 字符串中未转义的控制字符
// 控制字符指 ASCII 码小于 0x20 的字符
func fixControlChars(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	inString := false // 是否在字符串内部
	escaped := false  // 是否处于转义状态

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if escaped {
			// 上一个字符是反斜杠，当前字符直接写入
			buf.WriteByte(ch)
			escaped = false
			continue
		}

		if ch == '\\' && inString {
			// 在字符串内部遇到反斜杠，标记为转义状态
			buf.WriteByte(ch)
			escaped = true
			continue
		}

		if ch == '"' {
			// 遇到引号，切换字符串状态
			inString = !inString
			buf.WriteByte(ch)
			continue
		}

		// 在字符串内部且是控制字符，进行转义
		if inString && ch < 0x20 {
			switch ch {
			case '\n':
				buf.WriteString("\\n")
			case '\r':
				buf.WriteString("\\r")
			case '\t':
				buf.WriteString("\\t")
			case '\b':
				buf.WriteString("\\b")
			case '\f':
				buf.WriteString("\\f")
			default:
				// 其他控制字符使用 \u00XX 格式转义
				buf.WriteString("\\u00")
				buf.WriteByte("0123456789abcdef"[ch>>4])
				buf.WriteByte("0123456789abcdef"[ch&0x0f])
			}
			continue
		}

		buf.WriteByte(ch)
	}
	return buf.String()
}

// fixBadEscapes 修复 JSON 字符串中无效的转义序列
// 保留合法转义，移除无效转义的反斜杠
func fixBadEscapes(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	inString := false // 是否在字符串内部

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if ch == '\\' && inString {
			if i+1 < len(s) {
				next := s[i+1]
				switch next {
				// 合法的转义序列，保留
				case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
					buf.WriteByte(ch)
					buf.WriteByte(next)
					i++
					continue
				case 'u':
					// 验证 Unicode 转义 \uXXXX
					if i+5 < len(s) {
						valid := true
						for j := i + 2; j < i+6; j++ {
							if !isHex(s[j]) {
								valid = false
								break
							}
						}
						if valid {
							buf.WriteString(s[i : i+6])
							i += 5
							continue
						}
					}
					// 无效的 \u 转义，跳过反斜杠
					buf.WriteByte(next)
					i++
					continue
				default:
					// 未知转义，移除反斜杠
					buf.WriteByte(next)
					i++
					continue
				}
			}
		}

		if ch == '"' {
			inString = !inString
		}

		buf.WriteByte(ch)
	}
	return buf.String()
}

// isHex 检查字节是否为十六进制字符
func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// Streaming 流式解析不完整的 JSON 字符串
// 采用多阶段策略：
// 1. 先尝试完整解析
// 2. 尝试补全 JSON 结构后解析
// 3. 先修复再补全后解析
// 参数：
//
//	partial - 可能不完整的 JSON 字符串
//
// 返回：
//
//	解析后的目标类型 T 和是否解析成功
func Streaming[T any](partial string) (T, bool) {
	var zero T

	// 阶段1：尝试完整解析
	var result T
	if err := json.Unmarshal([]byte(partial), &result); err == nil {
		return result, true
	}

	// 阶段2：尝试补全 JSON 结构后解析
	completed := completeJSON(partial)
	if err := json.Unmarshal([]byte(completed), &result); err == nil {
		return result, true
	}

	// 阶段3：先修复再补全后解析
	repaired := Repair(partial)
	completed = completeJSON(repaired)
	if err := json.Unmarshal([]byte(completed), &result); err == nil {
		return result, true
	}

	return zero, false
}

// completeJSON 补全不完整的 JSON 结构
// 自动闭合未关闭的括号、处理尾随逗号、补全不完整的值
func completeJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "{}"
	}

	// 追踪未闭合的括号/方括号
	var stack []byte
	inString := false // 是否在字符串内部
	escaped := false  // 是否处于转义状态

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' && inString {
			escaped = true
			continue
		}

		if ch == '"' {
			inString = !inString
			continue
		}

		if inString {
			continue
		}

		// 只在非字符串区域处理括号
		switch ch {
		case '{', '[':
			stack = append(stack, ch)
		case '}':
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				stack = stack[:len(stack)-1]
			}
		case ']':
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				stack = stack[:len(stack)-1]
			}
		}
	}

	// 构建结果，处理尾随逗号和不完整值
	var buf strings.Builder
	buf.WriteString(s)

	// 移除尾随空格和逗号
	result := buf.String()
	result = strings.TrimRight(result, " \t\n\r")

	// 处理尾随逗号
	if len(result) > 0 && result[len(result)-1] == ',' {
		result = result[:len(result)-1]
	}

	// 处理尾随冒号（不完整的值）
	if len(result) > 0 && result[len(result)-1] == ':' {
		result += "null"
	}

	// 如果仍在字符串中，闭合引号
	if inString {
		result += "\""
	}

	// 闭合所有未关闭的括号（逆序）
	for i := len(stack) - 1; i >= 0; i-- {
		switch stack[i] {
		case '{':
			result += "}"
		case '[':
			result += "]"
		}
	}

	return result
}

// TrimTrailingComma 移除闭合括号前的尾随逗号
func TrimTrailingComma(s string) string {
	s = strings.TrimRightFunc(s, unicode.IsSpace)
	if len(s) > 0 && s[len(s)-1] == ',' {
		s = s[:len(s)-1]
	}
	return s
}
