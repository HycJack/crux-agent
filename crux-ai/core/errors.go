/*
 * 功能说明：错误类型定义和 HTTP 错误分类
 *
 * 解决的问题：
 * 1. 需要统一的错误类型来区分不同类型的失败
 * 2. 需要支持 errors.Is/As 模式进行错误判断
 * 3. 需要将 HTTP 错误映射到语义化的错误类型
 * 4. 需要携带上下文信息（提供者、状态码等）
 *
 * 解决方案：
 * 1. 定义哨兵错误（ErrOverflow、ErrAuth 等）支持 errors.Is
 * 2. 定义具体错误类型（OverflowError、RateLimitError 等）携带详细信息
 * 3. 实现 Is/Unwrap 方法支持错误链
 * 4. ClassifyHTTPError 将 HTTP 状态码映射到错误类型
 *
 * 应用场景：
 * - providers/ 层在解析响应时创建错误
 * - retry.go 使用错误类型判断是否重试
 * - agent 层根据错误类型采取不同策略
 */
package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Sentinel error reasons used by classification helpers. They are exposed as
// plain error values so callers can use errors.Is.
// || 哨兵错误，用于 errors.Is 判断
var (
	// ErrOverflow is a generic context-overflow marker. Prefer *OverflowError
	// for richer context.
	// || 上下文溢出错误标记
	ErrOverflow = errors.New("context overflow")

	// ErrAborted indicates the caller cancelled the operation.
	// || 操作被中止错误
	ErrAborted = errors.New("operation aborted")

	// ErrAuth indicates an authentication / authorization failure.
	// || 认证/授权失败错误
	ErrAuth = errors.New("authentication failed")

	// ErrRateLimit indicates the provider rate-limited the request.
	// || 请求被限流错误
	ErrRateLimit = errors.New("rate limited")

	// ErrServer indicates a transient server-side failure.
	// || 服务器端临时错误
	ErrServer = errors.New("server error")

	// ErrNetwork indicates a transport / connection failure.
	// || 网络/连接错误
	ErrNetwork = errors.New("network error")

	// ErrTimeout indicates a timeout occurred. Use *TimeoutError for
	// richer context about the timeout source.
	// || 超时错误标记
	ErrTimeout = errors.New("timeout")
)

// OverflowError is the typed signal raised when a request exceeds the
// provider's context window. Downstream code can detect it via
// errors.As(err, &oe) and inspect the numeric context.
// || 上下文溢出错误，当请求超过提供者的上下文窗口时触发
type OverflowError struct {
	// Provider is the provider that returned the overflow.
	// || 返回溢出错误的提供者
	Provider KnownProvider
	// Message is the underlying provider error text (may be empty for
	// silent-overflow cases detected from usage).
	// || 提供者返回的错误文本
	Message string
	// ContextWindow is the model's context window (0 if unknown).
	// || 模型的上下文窗口大小
	ContextWindow int
	// Usage is the input token count that overflowed (0 if unknown).
	// || 溢出的输入 token 数
	Usage int
}

// Error returns the error message.
// || 返回错误消息
func (e *OverflowError) Error() string {
	switch {
	case e.Message != "":
		return fmt.Sprintf("%s: context overflow: %s", e.Provider, e.Message)
	case e.ContextWindow > 0 && e.Usage > 0:
		return fmt.Sprintf("%s: context overflow: usage %d > window %d", e.Provider, e.Usage, e.ContextWindow)
	default:
		return fmt.Sprintf("%s: context overflow", e.Provider)
	}
}

// Is allows errors.Is to match ErrOverflow.
// || 支持 errors.Is 匹配 ErrOverflow
func (e *OverflowError) Is(target error) bool { return target == ErrOverflow }

// Unwrap allows errors.Is to recognize *OverflowError.
// || 支持 errors.Is 识别 *OverflowError
func (e *OverflowError) Unwrap() error { return ErrOverflow }

// CompactionCancelledError is the typed signal raised when a compaction is
// explicitly aborted. It mirrors oh-my-pi's `CompactionCancelledError` so
// callers can discriminate cancellation from other failures via errors.As
// rather than string matching.
// || 压缩操作被取消错误
type CompactionCancelledError struct {
	Reason string // 取消原因
}

// Error returns the error message.
// || 返回错误消息
func (e *CompactionCancelledError) Error() string {
	if e.Reason == "" {
		return "compaction cancelled"
	}
	return "compaction cancelled: " + e.Reason
}

// AbortError signals the consumer cancelled the in-flight operation.
// || 消费者取消正在进行的操作错误
type AbortError struct {
	Cause error // 原因
}

// Error returns the error message.
// || 返回错误消息
func (e *AbortError) Error() string {
	if e.Cause == nil {
		return "operation aborted"
	}
	return "operation aborted: " + e.Cause.Error()
}

// Unwrap returns the underlying cause.
// || 返回底层原因
func (e *AbortError) Unwrap() error { return e.Cause }

// Is allows errors.Is to match ErrAborted or context.Canceled.
// || 支持 errors.Is 匹配 ErrAborted 或 context.Canceled
func (e *AbortError) Is(t error) bool {
	return t == ErrAborted || t == context.Canceled
}

// AuthError wraps a 401/403 from the provider.
// || 认证错误（401/403）
type AuthError struct {
	Provider KnownProvider // 提供者
	Cause    error         // 原因
}

// Error returns the error message.
// || 返回错误消息
func (e *AuthError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: authentication failed", e.Provider)
	}
	return fmt.Sprintf("%s: authentication failed: %v", e.Provider, e.Cause)
}

// Unwrap returns the underlying cause.
// || 返回底层原因
func (e *AuthError) Unwrap() error { return e.Cause }

// Is allows errors.Is to match ErrAuth.
// || 支持 errors.Is 匹配 ErrAuth
func (e *AuthError) Is(t error) bool { return t == ErrAuth }

// RateLimitError wraps a 429 with optional retry-after hint.
// || 限流错误（429），包含可选的重试等待时间
type RateLimitError struct {
	Provider   KnownProvider // 提供者
	RetryAfter time.Duration // 重试等待时间
	Cause      error         // 原因
}

// Error returns the error message.
// || 返回错误消息
func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s: rate limited (retry after %s)", e.Provider, e.RetryAfter)
	}
	if e.Cause == nil {
		return fmt.Sprintf("%s: rate limited", e.Provider)
	}
	return fmt.Sprintf("%s: rate limited: %v", e.Provider, e.Cause)
}

// Unwrap returns the underlying cause.
// || 返回底层原因
func (e *RateLimitError) Unwrap() error { return e.Cause }

// Is allows errors.Is to match ErrRateLimit.
// || 支持 errors.Is 匹配 ErrRateLimit
func (e *RateLimitError) Is(t error) bool { return t == ErrRateLimit }

// ServerError wraps a 5xx response.
// || 服务器错误（5xx）
type ServerError struct {
	Provider   KnownProvider // 提供者
	StatusCode int           // HTTP 状态码
	Cause      error         // 原因
}

// Error returns the error message.
// || 返回错误消息
func (e *ServerError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: server error %d", e.Provider, e.StatusCode)
	}
	return fmt.Sprintf("%s: server error %d: %v", e.Provider, e.StatusCode, e.Cause)
}

// Unwrap returns the underlying cause.
// || 返回底层原因
func (e *ServerError) Unwrap() error { return e.Cause }

// Is allows errors.Is to match ErrServer.
// || 支持 errors.Is 匹配 ErrServer
func (e *ServerError) Is(t error) bool { return t == ErrServer }

// NetworkError wraps a transport-level failure (DNS, connect, EOF, etc.).
// || 网络错误（DNS、连接、EOF 等）
type NetworkError struct {
	Provider KnownProvider // 提供者
	Cause    error         // 原因
}

// Error returns the error message.
// || 返回错误消息
func (e *NetworkError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: network error", e.Provider)
	}
	return fmt.Sprintf("%s: network error: %v", e.Provider, e.Cause)
}

// Unwrap returns the underlying cause.
// || 返回底层原因
func (e *NetworkError) Unwrap() error { return e.Cause }

// Is allows errors.Is to match ErrNetwork.
// || 支持 errors.Is 匹配 ErrNetwork
func (e *NetworkError) Is(t error) bool { return t == ErrNetwork }

// TimeoutSource identifies where a timeout originated.
// || 超时来源标识
type TimeoutSource string

const (
	// TimeoutSourceAgent indicates the entire agent run exceeded its timeout.
	// || Agent 运行超时
	TimeoutSourceAgent TimeoutSource = "agent"

	// TimeoutSourceHTTP indicates an HTTP request to the LLM provider timed out.
	// || HTTP 请求超时
	TimeoutSourceHTTP TimeoutSource = "http"

	// TimeoutSourceTool indicates a tool execution (e.g., bash) timed out.
	// || 工具执行超时
	TimeoutSourceTool TimeoutSource = "tool"
)

// TimeoutError wraps a context.DeadlineExceeded with source information.
// || 超时错误，携带来源信息
type TimeoutError struct {
	Source   TimeoutSource // 超时来源
	Duration time.Duration // 超时时长
	Provider KnownProvider // 提供者（HTTP 超时时使用）
	ToolName string        // 工具名称（工具超时时使用）
	Cause    error         // 底层原因
}

// Error returns the error message.
// || 返回错误消息
func (e *TimeoutError) Error() string {
	var parts []string
	parts = append(parts, string(e.Source))

	if e.Duration > 0 {
		parts = append(parts, fmt.Sprintf("after %s", e.Duration))
	}

	if e.Provider != "" {
		parts = append(parts, fmt.Sprintf("provider=%s", e.Provider))
	}

	if e.ToolName != "" {
		parts = append(parts, fmt.Sprintf("tool=%s", e.ToolName))
	}

	if e.Cause != nil && e.Cause != context.DeadlineExceeded {
		parts = append(parts, fmt.Sprintf("cause=%v", e.Cause))
	}

	return fmt.Sprintf("timeout: %s", strings.Join(parts, " "))
}

// Unwrap returns the underlying cause.
// || 返回底层原因
func (e *TimeoutError) Unwrap() error {
	if e.Cause != nil {
		return e.Cause
	}
	return ErrTimeout
}

// Is allows errors.Is to match ErrTimeout or context.DeadlineExceeded.
// || 支持 errors.Is 匹配 ErrTimeout 或 context.DeadlineExceeded
func (e *TimeoutError) Is(t error) bool {
	return t == ErrTimeout || t == context.DeadlineExceeded
}

// ClassifyHTTPError maps an HTTP error response to a typed error. The body
// is the error payload returned by the provider. If no classification
// applies, nil is returned and the caller is expected to surface the raw
// error.
// || 将 HTTP 错误响应映射到类型化错误
// 参数：
//
//	provider - 提供者
//	status - HTTP 状态码
//	body - 响应体
//
// 返回：
//
//	类型化错误（如果无法分类返回 nil）
func ClassifyHTTPError(provider KnownProvider, status int, body string) error {
	switch {
	case status == http.StatusRequestTimeout, status == http.StatusTooManyRequests:
		// 408 Request Timeout 或 429 Too Many Requests -> 限流错误
		return &RateLimitError{Provider: provider, Cause: fmt.Errorf("status %d: %s", status, trimBody(body))}
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		// 401 Unauthorized 或 403 Forbidden -> 认证错误
		return &AuthError{Provider: provider, Cause: fmt.Errorf("status %d: %s", status, trimBody(body))}
	case status >= 500 && status <= 599:
		// 5xx -> 服务器错误
		return &ServerError{Provider: provider, StatusCode: status, Cause: fmt.Errorf("%s", trimBody(body))}
	case status == http.StatusRequestEntityTooLarge:
		// 413 Request Entity Too Large -> 溢出错误
		return &OverflowError{Provider: provider, Message: trimBody(body)}
	default:
		return nil
	}
}

// trimBody trims the response body for error messages.
// || 截断响应体用于错误消息
func trimBody(body string) string {
	body = strings.TrimSpace(body)
	const max = 500 // 最大长度
	if len(body) > max {
		return body[:max] + "..."
	}
	return body
}

// WrapTimeout wraps a context.DeadlineExceeded error with source information.
// || 包装超时错误，添加来源信息
func WrapTimeout(source TimeoutSource, duration time.Duration, cause error) error {
	if cause == nil {
		cause = context.DeadlineExceeded
	}
	return &TimeoutError{
		Source:   source,
		Duration: duration,
		Cause:    cause,
	}
}

// WrapHTTPTimeout wraps an HTTP request timeout with provider information.
// || 包装 HTTP 请求超时错误
func WrapHTTPTimeout(provider KnownProvider, duration time.Duration, cause error) error {
	if cause == nil {
		cause = context.DeadlineExceeded
	}
	return &TimeoutError{
		Source:   TimeoutSourceHTTP,
		Duration: duration,
		Provider: provider,
		Cause:    cause,
	}
}

// WrapToolTimeout wraps a tool execution timeout with tool name.
// || 包装工具执行超时错误
func WrapToolTimeout(toolName string, duration time.Duration, cause error) error {
	if cause == nil {
		cause = context.DeadlineExceeded
	}
	return &TimeoutError{
		Source:   TimeoutSourceTool,
		Duration: duration,
		ToolName: toolName,
		Cause:    cause,
	}
}
