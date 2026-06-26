/*
 * 功能说明：Unicode 字符清理工具
 *
 * 解决的问题：
 * 1. LLM 返回的文本可能包含无效的 Unicode 代理字符（surrogate pairs）
 * 2. 无效的 UTF-8 序列会导致 JSON 序列化失败
 * 3. 需要清理文本中的非法字符，确保数据可以正常序列化和传输
 *
 * 解决方案：
 * 1. 使用 Go 标准库的 utf8 包检测和处理 Unicode 字符
 * 2. 快速路径：如果字符串已是有效 UTF-8，直接返回原字符串
 * 3. 慢速路径：逐字符解码，跳过无效字节
 * 4. 使用预分配缓冲区提高性能
 *
 * 应用场景：
 * - 清理 LLM 返回的文本数据
 * - 预处理待序列化的字符串
 * - 处理来自外部数据源的不可信文本
 */
// Package sanitize provides Unicode sanitization utilities.
// || 提供 Unicode 字符清理工具
package sanitize

import "unicode/utf8"

// Surrogates removes unpaired Unicode surrogate characters from text.
// || 从文本中移除未配对的 Unicode 代理字符
// This prevents JSON serialization errors caused by invalid UTF-8 sequences.
// || 这可以防止由无效 UTF-8 序列导致的 JSON 序列化错误
// 参数：
//
//	text - 待清理的输入文本
//
// 返回：
//
//	清理后的有效 UTF-8 字符串
func Surrogates(text string) string {
	// Fast path: if the string is valid UTF-8, return as-is.
	// || 快速路径：如果字符串已是有效 UTF-8，直接返回
	if utf8.ValidString(text) {
		return text
	}

	// Slow path: rebuild the string, skipping invalid bytes
	// || 慢速路径：重新构建字符串，跳过无效字节
	buf := make([]byte, 0, len(text)) // 预分配缓冲区，容量等于原字符串长度
	for i := 0; i < len(text); {
		// Decode the next rune
		// || 解码下一个 Unicode 字符
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			// Skip invalid byte (unpaired surrogate or invalid sequence)
			// || 跳过无效字节（未配对的代理字符或无效序列）
			i++
			continue
		}
		// Append valid rune to buffer
		// || 将有效字符追加到缓冲区
		buf = utf8.AppendRune(buf, r)
		i += size
	}
	return string(buf)
}
