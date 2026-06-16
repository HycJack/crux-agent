/*
 * 功能说明：自动重试机制
 * 
 * 解决的问题：
 * 1. LLM API 调用可能因临时错误失败（限流、服务器错误、网络问题）
 * 2. 需要区分可重试错误和不可重试错误
 * 3. 需要指数退避避免加重服务端负担
 * 4. 需要支持 context 取消
 * 
 * 解决方案：
 * 1. 定义 RetryConfig 配置重试参数（最大次数、基础延迟、最大延迟等）
 * 2. IsRetryableError 判断错误是否可重试
 * 3. Retry 函数实现指数退避重试逻辑
 * 4. 支持 RateLimitError 中的 RetryAfter 提示
 * 
 * 应用场景：
 * - providers/ 层在发起 HTTP 请求时使用
 * - 限流时自动等待并重试
 * - 网络错误时自动重连
 */
package core

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Retry timing constants.
// || 重试时间常量
const (
	// DefaultBaseDelay is the initial backoff delay between retry attempts.
	// || 默认基础退避延迟
	DefaultBaseDelay = 2 * time.Second

	// DefaultMaxDelay caps the exponential backoff at 5 minutes.
	// || 默认最大延迟为 5 分钟
	DefaultMaxDelay = 5 * time.Minute

	// DefaultMaxRetries is the default number of retry attempts.
	// || 默认最大重试次数
	DefaultMaxRetries = 3

	// DefaultBackoffMultiplier is the default exponential growth factor.
	// || 默认退避增长倍数
	DefaultBackoffMultiplier = 2.0
)

// RetryConfig controls the auto-retry behavior of Retry.
// || 控制 Retry 的自动重试行为
//
// Defaults follow oh-my-pi's `non-compaction-retry-policy.md`:
// || 默认值遵循 oh-my-pi 的 non-compaction-retry-policy.md
//   - Enabled:      true
//   - MaxRetries:   3
//   - BaseDelay:    2s
//   - MaxDelay:     5min (0 disables fail-fast)
//   - Multiplier:   2 (delay doubles each attempt)
//
// Context-overflow errors are NEVER retried by Retry — they are classified
// separately so the caller can hand them to compaction logic.
// || 上下文溢出错误永远不会被重试，它们需要交给压缩逻辑处理
type RetryConfig struct {
	Enabled    bool          // 是否启用重试
	MaxRetries int           // 最大重试次数
	BaseDelay  time.Duration // 基础延迟
	MaxDelay   time.Duration // 最大延迟（0 表示不限制）
	Multiplier float64       // 延迟乘数

	// OnRetry, if set, is called before each backoff sleep with the
	// upcoming attempt number (1-based). Use it to surface "retrying in Ns"
	// messages to the user.
	// || 重试回调，在每次退避等待前调用
	OnRetry func(attempt int, nextDelay time.Duration, err error)
}

// DefaultRetryConfig returns the policy defaults.
// || 返回默认的重试配置
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		Enabled:    true,
		MaxRetries: DefaultMaxRetries,
		BaseDelay:  DefaultBaseDelay,
		MaxDelay:   DefaultMaxDelay,
		Multiplier: DefaultBackoffMultiplier,
	}
}

// retryablePatterns matches transient transport / rate-limit / server errors.
// This is string-pattern classification, not typed provider error codes,
// matching the policy in oh-my-pi's non-compaction-retry-policy.md.
// || 可重试错误的正则匹配模式（匹配临时传输/限流/服务器错误）
var retryablePatterns = []*regexp.Regexp{
	// HTTP classes
	// || HTTP 状态码
	regexp.MustCompile(`(?i)\b(429|500|502|503|504)\b`),
	regexp.MustCompile(`(?i)too many requests`),
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)usage limit`),
	regexp.MustCompile(`(?i)usage cap`),
	// Overload / provider-returned
	// || 过载/提供者返回
	regexp.MustCompile(`(?i)overloaded`),
	regexp.MustCompile(`(?i)server error`),
	regexp.MustCompile(`(?i)service unavailable`),
	regexp.MustCompile(`(?i)internal server error`),
	regexp.MustCompile(`(?i)server is overloaded`),
	// Provider-suggested retry
	// || 提供者建议重试
	regexp.MustCompile(`(?i)retry your request`),
	regexp.MustCompile(`(?i)please retry`),
	regexp.MustCompile(`(?i)please try again`),
	// Transport-level
	// || 传输层错误
	regexp.MustCompile(`(?i)connection (?:reset|refused|closed)`),
	regexp.MustCompile(`(?i)connect: (?:timeout|connection refused)`),
	regexp.MustCompile(`(?i)socket hang up`),
	regexp.MustCompile(`(?i)unexpected eof`),
	regexp.MustCompile(`(?i)timeout`),
	regexp.MustCompile(`(?i)timed out`),
	regexp.MustCompile(`(?i)fetch failed`),
	regexp.MustCompile(`(?i)no such host`),
	regexp.MustCompile(`(?i)tls: `),
	regexp.MustCompile(`(?i)broken pipe`),
	regexp.MustCompile(`(?i)connection reset by peer`),
	regexp.MustCompile(`(?i)upstream (?:connect|read) (?:timeout|failed)`),
	regexp.MustCompile(`(?i)retry delay`),
	regexp.MustCompile(`(?i)retry-after`),
}

// nonRetryablePatterns exclude throttling / rate-limit errors that LOOK
// like retryable errors from auto-retry. (None today; reserved for future
// "credentials exhausted" patterns that should NOT auto-retry.)
// || 不可重试错误的排除模式（当前为空，预留给未来的"凭证耗尽"模式）
var nonRetryablePatterns = []*regexp.Regexp{}

// IsRetryableError reports whether err is a transient error that Retry
// should attempt again. Context-overflow errors are NEVER retryable —
// they must be handed to compaction logic.
// || 判断错误是否为可重试的临时错误
// 参数：
//   err - 错误
// 返回：
//   是否可重试
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	// 上下文溢出错误不可重试
	if IsContextOverflowError(err) {
		return false
	}
	// Typed sentinels.
	// || 类型化哨兵错误
	if errors.Is(err, ErrRateLimit) || errors.Is(err, ErrServer) || errors.Is(err, ErrNetwork) {
		return true
	}
	// Abort / cancellation must never be retried.
	// || 中止/取消错误不可重试
	if errors.Is(err, ErrAborted) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTimeout) {
		return false
	}
	// 字符串模式匹配
	msg := err.Error()
	lower := strings.ToLower(msg)
	for _, p := range nonRetryablePatterns {
		if p.MatchString(lower) {
			return false
		}
	}
	for _, p := range retryablePatterns {
		if p.MatchString(lower) {
			return true
		}
	}
	return false
}

// IsContextOverflowError reports whether err is a context-overflow signal.
// Callers should short-circuit auto-retry and route to compaction.
// || 判断错误是否为上下文溢出错误
// 参数：
//   err - 错误
// 返回：
//   是否为上下文溢出错误
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	var oe *OverflowError
	if errors.As(err, &oe) {
		return true
	}
	return errors.Is(err, ErrOverflow)
}

// IsAuthError reports whether err is an authentication / authorization
// failure (401/403). Such errors must NEVER be retried automatically.
// || 判断错误是否为认证错误（401/403），此类错误不可自动重试
// 参数：
//   err - 错误
// 返回：
//   是否为认证错误
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrAuth)
}

// retryKey is a small object used by Retry to coordinate the lifecycle.
// Concurrent calls with the same key share a single in-flight chain.
// || 重试键，用于协调生命周期（当前未使用）
type retryKey struct{}

// Retry runs op with exponential-backoff retry on transient errors.
// || 使用指数退避重试运行操作
//
// op is invoked once initially; on a retryable error, Retry sleeps for the
// computed backoff and re-invokes op. The function returns the first
// non-retryable error, or the final retryable error after MaxRetries is
// exhausted, or nil on success.
// || op 首先被调用一次；如果出现可重试错误，Retry 会等待计算出的退避时间后重新调用 op
// || 函数返回第一个不可重试错误，或在 MaxRetries 耗尽后返回最后的可重试错误，或成功时返回 nil
//
// Retry respects ctx cancellation during the backoff sleep; the next
// pending attempt is abandoned and ctx.Err() is returned.
// || Retry 在退避等待期间尊重 ctx 取消；下一个待处理的尝试会被放弃并返回 ctx.Err()
// 参数：
//   ctx - 上下文（支持取消）
//   cfg - 重试配置
//   op - 要执行的操作
// 返回：
//   操作返回的错误
func Retry(ctx context.Context, cfg RetryConfig, op func() error) error {
	if !cfg.Enabled {
		return op()
	}
	// 设置默认值
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 2 * time.Second
	}
	if cfg.Multiplier <= 0 {
		cfg.Multiplier = 2.0
	}

	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		// 检查 context 是否已取消
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// 执行操作
		err := op()
		if err == nil {
			return nil
		}
		lastErr = err

		// Never retry non-retryable errors.
		// || 不可重试错误直接返回
		if !IsRetryableError(err) {
			return err
		}

		// Last attempt? Return the error.
		// || 最后一次尝试？返回错误
		if attempt == cfg.MaxRetries {
			return err
		}

		// Compute the next backoff delay.
		// || 计算下一次退避延迟
		delay := computeDelay(cfg, attempt, err)

		// Fail-fast: if the delay exceeds MaxDelay (and MaxDelay > 0),
		// give up immediately rather than sleeping.
		// || 快速失败：如果延迟超过 MaxDelay（且 MaxDelay > 0），立即放弃
		if cfg.MaxDelay > 0 && delay > cfg.MaxDelay {
			return err
		}

		// 调用重试回调
		if cfg.OnRetry != nil {
			cfg.OnRetry(attempt+1, delay, err)
		}

		// Sleep, but be cancellable.
		// || 等待，但支持取消
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return lastErr
}

// computeDelay calculates the backoff delay for the given attempt.
// || 计算给定尝试次数的退避延迟
// 参数：
//   cfg - 重试配置
//   attempt - 当前尝试次数（0-based）
//   err - 错误（可能包含 RetryAfter 提示）
// 返回：
//   退避延迟
func computeDelay(cfg RetryConfig, attempt int, err error) time.Duration {
	base := float64(cfg.BaseDelay)
	mult := cfg.Multiplier
	delay := time.Duration(base * pow(mult, float64(attempt)))

	// RateLimitError can carry a retry-after hint.
	// || RateLimitError 可能包含 RetryAfter 提示
	var rl *RateLimitError
	if errors.As(err, &rl) && rl.RetryAfter > 0 {
		if h := rl.RetryAfter; h > delay {
			delay = h
		}
	}
	return delay
}

// pow is a tiny integer-safe float pow used for backoff math.
// || 用于退避计算的简单幂函数
func pow(base, exp float64) float64 {
	if exp == 0 {
		return 1
	}
	result := 1.0
	for i := 0; i < int(exp); i++ {
		result *= base
	}
	return result
}

// RetryableHTTPCheck classifies a (status, body) pair. The returned
// retryable flag tells whether Retry should re-issue the request.
// || 分类 HTTP (状态码, 响应体) 对，判断是否应该重试
// 参数：
//   status - HTTP 状态码
//   body - 响应体
// 返回：
//   是否可重试
func RetryableHTTPCheck(status int, body string) bool {
	// 408 Request Timeout, 429 Too Many Requests, 5xx -> 可重试
	if status == 408 || status == 429 || (status >= 500 && status <= 504) {
		return true
	}
	if body == "" {
		return false
	}
	// 检查响应体中的可重试模式
	lower := strings.ToLower(body)
	for _, p := range retryablePatterns {
		if p.MatchString(lower) {
			return true
		}
	}
	return false
}

// IsNetworkError reports whether err looks like a transport-level failure
// (DNS, connect, EOF, etc.).
// || 判断错误是否为传输层错误（DNS、连接、EOF 等）
// 参数：
//   err - 错误
// 返回：
//   是否为网络错误
func IsNetworkError(err error) bool {
	if err == nil {
		return false
	}
	// 检查类型化哨兵错误
	if errors.Is(err, ErrNetwork) {
		return true
	}
	// 检查 net.Error
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	// 字符串模式匹配
	msg := strings.ToLower(err.Error())
	for _, p := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)connection (?:reset|refused|closed)`),
		regexp.MustCompile(`(?i)socket hang up`),
		regexp.MustCompile(`(?i)unexpected eof`),
		regexp.MustCompile(`(?i)no such host`),
		regexp.MustCompile(`(?i)tls: `),
		regexp.MustCompile(`(?i)broken pipe`),
	} {
		if p.MatchString(msg) {
			return true
		}
	}
	return false
}

// retryOnce is a no-op import-block placeholder kept to keep net in use on
// platforms that might prune imports. (No runtime effect.)
// || 占位符，确保 net 包被使用（无运行时影响）
var _ = sync.Mutex{}
