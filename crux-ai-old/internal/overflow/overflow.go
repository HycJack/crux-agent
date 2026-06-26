/*
 * 功能说明：LLM 上下文溢出检测工具
 *
 * 解决的问题：
 * 1. 不同 LLM 提供商对上下文溢出的错误消息格式各不相同
 * 2. 需要统一检测各种提供商的上下文窗口超限错误
 * 3. 需要区分真正的上下文溢出和限流/服务不可用等错误
 * 4. 需要支持基于错误消息和 token 使用量的双重检测
 *
 * 解决方案：
 * 1. 维护一个包含 20+ 种提供商错误模式的正则表达式列表
 * 2. 使用非溢出模式列表排除限流类错误（应重试而非压缩上下文）
 * 3. 先检查排除模式，再检查溢出模式，最后检查 token 使用量
 * 4. 设计为宽松匹配策略：假阳性代价低（触发压缩），假阴性代价高（破坏对话）
 *
 * 应用场景：
 * - AI Agent 在调用 LLM 前检测是否需要压缩上下文
 * - 自动重试机制中判断错误类型以决定处理策略
 * - 对话管理中动态调整上下文窗口
 *
 * 支持的提供商：
 * - Anthropic, Amazon Bedrock, OpenAI, Google Gemini
 * - xAI Grok, Groq, OpenRouter, llama.cpp
 * - LM Studio, GitHub Copilot, MiniMax, Kimi For Coding
 * - Mistral, z.ai, Ollama, Cerebras
 */
package overflow

import (
	"regexp"
	"strings"
)

// overflowPatterns 用于匹配已知的提供商上下文溢出错误消息
// 按 oh-my-pi 源码的顺序组织，覆盖 20+ 种提供商错误变体
var overflowPatterns = []*regexp.Regexp{
	// Anthropic
	regexp.MustCompile(`(?i)prompt is too long`),
	regexp.MustCompile(`(?i)request_too_large`),
	regexp.MustCompile(`(?i)request exceeds the maximum size`),
	// 通用 HTTP 413 变体（Payload Too Large）
	regexp.MustCompile(`(?i)payload too large`),
	regexp.MustCompile(`(?i)entity too large`),
	regexp.MustCompile(`(?i)\b413\b.*\b(request|payload|entity)\b.*\btoo large\b`),
	// Amazon Bedrock
	regexp.MustCompile(`(?i)input is too long for requested model`),
	// OpenAI（Completions & Responses）
	regexp.MustCompile(`(?i)exceeds? the context window`),
	regexp.MustCompile(`(?i)exceeds? (?:the )?(?:model'?s )?(?:maximum )?context length`),
	regexp.MustCompile(`(?i)exceeds? (?:the )?(?:model'?s )?maximum context length of [\d,]+ tokens?`),
	// Google（Gemini）
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),
	// xAI（Grok）
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),
	// Groq
	regexp.MustCompile(`(?i)reduce the length of the messages`),
	// OpenRouter
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),
	regexp.MustCompile(`(?i)exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`),
	// llama.cpp（本地部署）
	regexp.MustCompile(`(?i)exceeds the available context size`),
	regexp.MustCompile(`(?i)requested tokens?.*exceed.*context (window|length|size)`),
	regexp.MustCompile(`(?i)context (window|length|size).*(exceeded|overflow|too small)`),
	regexp.MustCompile(`(?i)(prompt|input).*(too long|too large).*(context|n_ctx)`),
	regexp.MustCompile(`(?i)requested tokens?.*(exceeds?|greater than).*(n_ctx|context)`),
	// LM Studio
	regexp.MustCompile(`(?i)greater than the context length`),
	// GitHub Copilot
	regexp.MustCompile(`(?i)exceeds the limit of \d+`),
	// MiniMax
	regexp.MustCompile(`(?i)context window exceeds limit`),
	// Kimi For Coding
	regexp.MustCompile(`(?i)exceeded model token limit`),
	// Mistral
	regexp.MustCompile(`(?i)too large for model with \d+ maximum context length`),
	// z.ai
	regexp.MustCompile(`(?i)model_context_window_exceeded`),
	// Ollama
	regexp.MustCompile(`(?i)prompt too long; exceeded (?:max )?context length`),
	// 通用兜底模式
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),
	regexp.MustCompile(`(?i)too many tokens`),
	regexp.MustCompile(`(?i)token limit exceeded`),
	// Cerebras: 400/413 状态码且无响应体（HTTP 前缀可选）
	regexp.MustCompile(`(?i)^(?:HTTP\s+)?4(?:00|13)\s*(?:status code)?\s*\(no body\)`),
}

// nonOverflowPatterns 用于排除看起来像溢出但实际是限流的错误
// 这些错误应该重试而非压缩上下文
var nonOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(throttling error|service unavailable):`),
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)too many requests`),
}

// IsOverflow 检测错误消息或 token 使用量是否表示上下文溢出
// 检测策略：
// 1. 先检查错误消息是否匹配非溢出模式（限流等），匹配则返回 false
// 2. 再检查错误消息是否匹配溢出模式，匹配则返回 true
// 3. 最后检查静默溢出：token 使用量是否超过上下文窗口
// 参数：
//
//	errMsg - 错误消息字符串（可为空）
//	contextWindow - 模型的最大上下文窗口（token数，为0时跳过使用量检测）
//	usage - 当前使用的 token 数量
//
// 返回：
//
//	true 表示检测到上下文溢出，false 表示未溢出或为其他类型错误
func IsOverflow(errMsg string, contextWindow int, usage int) bool {
	if errMsg != "" {
		lower := strings.ToLower(errMsg)
		// 首先排除非溢出模式（限流、节流等应重试的错误）
		for _, p := range nonOverflowPatterns {
			if p.MatchString(lower) {
				return false
			}
		}
		// 然后检查溢出模式
		for _, p := range overflowPatterns {
			if p.MatchString(lower) {
				return true
			}
		}
	}
	// 静默溢出检测：使用量超过上下文窗口（无错误消息但实际已溢出）
	if contextWindow > 0 && usage > contextWindow {
		return true
	}
	return false
}

// GetPatterns 返回溢出模式列表，用于测试目的
func GetPatterns() []*regexp.Regexp {
	return overflowPatterns
}
