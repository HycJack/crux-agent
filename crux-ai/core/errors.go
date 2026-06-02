// Package core 提供 AI provider 抽象层
package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ============================================================================
// 错误分类系统
//
// 参考 oh-my-pi (packages/ai/src/utils/retry.ts) 的设计：
//   - 错误按来源/类型分类，不是每个 provider 自己写 retry
//   - retry 决策与 provider 实现解耦，通过接口组合
// ============================================================================

// ErrorKind 表示一个分类后的错误类型
type ErrorKind int

const (
	ErrorKindUnknown       ErrorKind = iota
	ErrorKindRateLimit                // 429 — 速率限制
	ErrorKindOverloaded               // 500/503 — 服务端过载
	ErrorKindAuth                     // 401/403 — 认证/授权失败
	ErrorKindModelNotFound            // 404 — 模型不存在
	ErrorKindContextLength            // 上下文超长（400+特殊 code）
	ErrorKindTimeout                  // 请求超时
	ErrorKindBadRequest               // 400（不可重试）
	ErrorKindServerError              // 500（非过载，不可重试）
)

// ErrorKind 的中文描述
func (k ErrorKind) String() string {
	switch k {
	case ErrorKindRateLimit:
		return "rate_limit"
	case ErrorKindOverloaded:
		return "overloaded"
	case ErrorKindAuth:
		return "auth"
	case ErrorKindModelNotFound:
		return "model_not_found"
	case ErrorKindContextLength:
		return "context_length"
	case ErrorKindTimeout:
		return "timeout"
	case ErrorKindBadRequest:
		return "bad_request"
	case ErrorKindServerError:
		return "server_error"
	default:
		return "unknown"
	}
}

// IsRetryable 返回该错误类型是否可重试。
// 参考 omp 的 isRetryableError() 逻辑。
func (k ErrorKind) IsRetryable() bool {
	switch k {
	case ErrorKindRateLimit, ErrorKindOverloaded, ErrorKindTimeout:
		return true
	default:
		return false
	}
}

// ProviderError 携带分类信息的错误。
// provider 在返回错误时应该用 NewProviderError 包裹，自动分类。
type ProviderError struct {
	Kind     ErrorKind
	Message  string
	HTTPCode int
	Wrapped  error
}

func (e *ProviderError) Error() string {
	if e.Wrapped != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Kind, e.Message, e.Wrapped)
	}
	return fmt.Sprintf("[%s] %s", e.Kind, e.Message)
}

func (e *ProviderError) Unwrap() error {
	return e.Wrapped
}

// NewProviderError 创建一个分类后的 provider 错误。
// 自动根据 HTTP 状态码推断错误类型。
func NewProviderError(httpCode int, message string, wrapped error) *ProviderError {
	kind := classifyHTTPCode(httpCode)
	return &ProviderError{
		Kind:     kind,
		Message:  message,
		HTTPCode: httpCode,
		Wrapped:  wrapped,
	}
}

// IsRetryableProviderError 判断一个 error 是否是分类后的可重试错误。
func IsRetryableProviderError(err error) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Kind.IsRetryable()
	}
	// 未分类的错误也保守判断
	return false
}

// ExtractErrorKind 提取错误分类，未分类的返回 ErrorKindUnknown。
func ExtractErrorKind(err error) ErrorKind {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Kind
	}
	return ErrorKindUnknown
}

// classifyHTTPCode 根据 HTTP 状态码返回错误分类。
func classifyHTTPCode(code int) ErrorKind {
	switch {
	case code == http.StatusTooManyRequests: // 429
		return ErrorKindRateLimit
	case code == http.StatusUnauthorized, code == http.StatusForbidden: // 401, 403
		return ErrorKindAuth
	case code == http.StatusNotFound: // 404
		return ErrorKindModelNotFound
	case code == http.StatusBadRequest: // 400
		return ErrorKindBadRequest // 大部分 400 不可重试
	case code == http.StatusServiceUnavailable || code == http.StatusBadGateway || code == http.StatusGatewayTimeout:
		return ErrorKindOverloaded // 503, 502, 504
	case code >= 500:
		return ErrorKindServerError
	default:
		return ErrorKindUnknown
	}
}

// Retryable 标识一个错误可以重试的接口。
// 实现该接口的类型可以用在重试装饰器中。
type Retryable interface {
	Retryable() bool
}

// ============================================================================
// 可组合的重试装饰器
//
// 参考 omp 的 callWithCopilotModelRetry() 设计：
//   - WrapRetry 不侵入 provider 代码
//   - 支持分层重试策略
//   - provider 可以通过 IsRetryableProviderError 或 Retryable 接口自定义重试决策
// ============================================================================

// RetryPolicy 定义重试策略。
type RetryPolicy struct {
	// MaxAttempts 最大尝试次数（包括第一次）。默认 3。
	MaxAttempts int
	// InitialBackoff 首次退避时间。默认 1s。
	InitialBackoff time.Duration
	// MaxBackoff 最大退避时间。默认 30s。
	MaxBackoff time.Duration
	// BackoffFactor 退避增长因子。默认 2。
	BackoffFactor float64
	// ShouldRetry 可选的判重函数。返回 true 表示应该重试。
	// 为 nil 时使用默认的 IsRetryableProviderError。
	ShouldRetry func(error) bool
	// OnRetry 每次重试前的回调（可用于日志记录）。
	OnRetry func(attempt int, err error)
}

// DefaultRetryPolicy 返回默认重试策略。
//
//	MaxAttempts: 3
//	InitialBackoff: 1s
//	MaxBackoff: 30s
//	BackoffFactor: 2
//	ShouldRetry: IsRetryableProviderError
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		ShouldRetry:    IsRetryableProviderError,
	}
}

// WrapRetry 包裹一个函数，按照 RetryPolicy 自动重试。
//
// 使用方式：
//
//	policy := DefaultRetryPolicy()
//	policy.OnRetry = func(attempt int, err error) {
//	    log.Printf("retry attempt %d: %v", attempt, err)
//	}
//	result, err := WrapRetry(ctx, policy, func(ctx context.Context) (AssistantMessage, error) {
//	    return provider.Stream(ctx, ...)
//	})
func WrapRetry[T any](ctx context.Context, policy RetryPolicy, fn func(context.Context) (T, error)) (T, error) {
	// 设置默认值
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 3
	}
	if policy.InitialBackoff <= 0 {
		policy.InitialBackoff = 1 * time.Second
	}
	if policy.MaxBackoff <= 0 {
		policy.MaxBackoff = 30 * time.Second
	}
	if policy.BackoffFactor <= 0 {
		policy.BackoffFactor = 2.0
	}
	if policy.ShouldRetry == nil {
		policy.ShouldRetry = IsRetryableProviderError
	}

	var lastErr error
	backoff := policy.InitialBackoff

	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			var zero T
			return zero, ctx.Err()
		}

		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}
		lastErr = err

		// 判断是否应该重试
		if !policy.ShouldRetry(err) {
			break
		}

		// 最后一次尝试失败后不再等待
		if attempt == policy.MaxAttempts-1 {
			break
		}

		if policy.OnRetry != nil {
			policy.OnRetry(attempt+1, err)
		}

		// 指数退避，注意 context 取消
		select {
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		case <-time.After(backoff):
		}
		backoff = time.Duration(float64(backoff) * policy.BackoffFactor)
		if backoff > policy.MaxBackoff {
			backoff = policy.MaxBackoff
		}
	}

	var zero T
	return zero, lastErr
}

// ============================================================================
// Provider 特定的错误辅助函数
//
// 参考 omp 在 single-provider 级别的特殊 retry 逻辑：
//   - isCopilotTransientModelError() — GitHub Copilot 的 400 model_not_supported
//   - 这些函数适合在 provider 实现中调用
// ============================================================================

// IsHTTPCodeError 检查错误是否来自 HTTP 状态码。
func IsHTTPCodeError(err error, code int) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.HTTPCode == code
	}
	return false
}

// IsTransient400Error 检查是否是特定 API 返回的"瞬态 400"错误。
// 有些 provider（如 GitHub Copilot）在某些情况下会返回 400 但实际可以重试。
// 调用方通过 providerID 来判断。
func IsTransient400Error(err error, transientCodes ...string) bool {
	if !IsHTTPCodeError(err, http.StatusBadRequest) {
		return false
	}
	if len(transientCodes) == 0 {
		return false
	}
	msg := err.Error()
	for _, code := range transientCodes {
		if strings.Contains(msg, code) {
			return true
		}
	}
	return false
}

// NewContextError 构造上下文超长错误，便于 error propagation 时携带分类。
func NewContextError(message string, wrapped error) *ProviderError {
	return &ProviderError{
		Kind:    ErrorKindContextLength,
		Message: message,
		Wrapped: wrapped,
	}
}
