// Package core 提供流式超时控制
//
// 参考 oh-my-pi (packages/ai/src/utils/idle-iterator.ts) 的设计：
//   双阶段超时:
//     1. firstEventTimeout: 从开始流到收到第一个事件的时间
//     2. idleTimeout: 事件之间的最大间隔
//
// 心跳/keepalive 事件通过 isProgressItem 区分——不重置 idle 计时器，
// 防止"服务器一直发心跳但模型实际卡住了"。

package core

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ============================================================================
// 超时配置
// ============================================================================

// StreamTimeoutConfig 定义流式请求的双阶段超时策略。
//
// 典型行为：
//
//	时序:      |-- firstEventTimeout --|---- idleTimeout ----|---- idleTimeout ----|-->
//	事件流:    ...(等待)...            EventStart  TextDelta  TextDelta  ...    Done
type StreamTimeoutConfig struct {
	// FirstEventTimeout 从流开始到收到第一个事件的超时。
	// 设置为 0 或负值表示不限制（永不超时）。
	// 默认: 100s（推理/冷启动可能需要较长时间）
	FirstEventTimeout time.Duration

	// IdleTimeout 相邻两个事件之间的最大间隔。
	// 设置为 0 或负值表示不限制（永不超时）。
	// 默认: 120s
	IdleTimeout time.Duration

	// IsProgressItem 判断一个事件是否算作"进度事件"。
	// 进度事件会重置 idle 计时器；非进度事件（如心跳）不会。
	// 为 nil 时所有事件都算进度事件。
	IsProgressItem func(item any) bool
}

// DefaultStreamTimeoutConfig 适合大多数 LLM provider 的默认超时配置。
func DefaultStreamTimeoutConfig() StreamTimeoutConfig {
	return StreamTimeoutConfig{
		FirstEventTimeout: 100 * time.Second,
		IdleTimeout:       120 * time.Second,
		IsProgressItem:    nil, // 所有事件都算进度
	}
}

// TimeoutError 表示超时错误。
type TimeoutError struct {
	Kind    string // "first_event" 或 "idle"
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("stream %s timeout after %v", e.Kind, e.Timeout)
}

// IsTimeoutError 检查是否是超时错误。
func IsTimeoutError(err error) bool {
	var te *TimeoutError
	return errors.As(err, &te)
}

// ============================================================================
// 带超时的 EventStream 包装
//
// StreamWithTimeout 包装一个 EventStream，自动注入双阶段超时。
// 用法:
//
//	stream := NewEventStreamWithTimeout(rawStream, timeoutConfig)
//	msg, err := stream.ForEach(ctx, func(evt AssistantMessageEvent) error {
//	    fmt.Println(evt)
//	    return nil
//	})
//
// 与原始 EventStream.ForEach 的区别:
//   1. 首帧超时：第一个事件长时间不来会报错
//   2. 空闲超时：两个事件间隔过长会报错
//   3. 心跳/keepalive 不会被等同于"进度"
// ============================================================================

// StreamWithTimeout 包装 EventStream 添加超时控制。
type StreamWithTimeout[T any, R any] struct {
	stream *EventStream[T, R]
	config StreamTimeoutConfig
}

// NewEventStreamWithTimeout 创建一个超时中间件。
func NewEventStreamWithTimeout[T any, R any](
	stream *EventStream[T, R],
	config StreamTimeoutConfig,
) *StreamWithTimeout[T, R] {
	return &StreamWithTimeout[T, R]{
		stream: stream,
		config: config,
	}
}

// ForEach 遍历事件流，自动注入双阶段超时控制。
// 返回最终的 AssistantMessage（与原始 EventStream.ForEach 行为一致）。
//
// 注意：本方法已包含超时控制，请不要在同一个 EventStream 上同时调用
// StreamWithTimeout.ForEach 和原始 EventStream.ForEach。
func (w *StreamWithTimeout[T, R]) ForEach(ctx context.Context, handler func(T) error) (R, error) {
	firstEventReceived := false
	lastProgress := time.Now()

	for {
		// 计算本次竞选的超时时间
		var timeoutCh <-chan time.Time
		if !firstEventReceived && w.config.FirstEventTimeout > 0 {
			timeoutCh = time.After(w.config.FirstEventTimeout)
		} else if firstEventReceived && w.config.IdleTimeout > 0 {
			elapsed := time.Since(lastProgress)
			remaining := w.config.IdleTimeout - elapsed
			if remaining <= 0 {
				var zero R
				return zero, &TimeoutError{Kind: "idle", Timeout: w.config.IdleTimeout}
			}
			timeoutCh = time.After(remaining)
		}

		select {
		case <-ctx.Done():
			w.stream.Stop()
			var zero R
			return zero, ctx.Err()

		case <-timeoutCh:
			kind := "idle"
			timeout := w.config.IdleTimeout
			if !firstEventReceived {
				kind = "first_event"
				timeout = w.config.FirstEventTimeout
			}
			w.stream.Stop()
			var zero R
			return zero, &TimeoutError{Kind: kind, Timeout: timeout}

		case evt, ok := <-w.stream.Events():
			if !ok {
				return w.stream.Result()
			}
			if evt.done {
				if evt.err != nil {
					var zero R
					return zero, evt.err
				}
				return w.stream.Result()
			}

			// 标记首帧已到
			if !firstEventReceived {
				firstEventReceived = true
				lastProgress = time.Now()
			}

			// 判断是否重置 idle 计时器
			if w.config.IsProgressItem == nil || w.config.IsProgressItem(evt.value) {
				lastProgress = time.Now()
			}

			if err := handler(evt.value); err != nil {
				w.stream.Stop()
				var zero R
				return zero, err
			}
		}
	}
}

// ============================================================================
// 流式竞态中间件（Promise.race 模式）
//
// 参考 omp (packages/ai/src/utils/idle-iterator.ts) 的设计：
//   把超时、中断、数据三个信号通过 Promise.race 竞态。
//   在 Go 中我们通过 select 实现，但提供更结构化的方式。
//
// Race 用于将超时、中断、数据三个信号统一处理。
// 每个 Next 调用都会返回一个 RaceResult，告知哪个信号胜出。
// ============================================================================

// RaceConfig 竞态配置
type RaceConfig struct {
	// PerEventTimeout 每个事件之间的最大等待时间。
	// 每次调用 Next() 都会重启这个计时器，因此这是"事件间空闲超时"，
	// 而非整个流的整体超时。0 表示无限制。
	PerEventTimeout time.Duration
	// Abort 可选的取消信号 channel。
	Abort <-chan struct{}
}

// Race 在 EventStream 上执行竞态迭代。
type Race[T any, R any] struct {
	stream *EventStream[T, R]
	config RaceConfig
}

// RaceResult 表示竞态结果
type RaceResult[T any] struct {
	// Item 当 Kind 为 RaceEvent 时有效
	Item T
	// Kind 表示竞态的赢家
	Kind RaceKind
	// Error 当 Kind 为 RaceError 或 RaceAbort 时有效
	Error error
}

// RaceKind 表示竞态的赢家
type RaceKind int

const (
	RaceEvent   RaceKind = iota // 正常数据事件
	RaceTimeout                 // 超时
	RaceDone                    // 流正常结束
	RaceAbort                   // 中断
	RaceError                   // 流错误
)

// NewRace 创建一个竞态迭代器。
func NewRace[T any, R any](stream *EventStream[T, R], config RaceConfig) *Race[T, R] {
	return &Race[T, R]{stream: stream, config: config}
}

// Next 等待下一个结果，返回时告知哪个信号胜出。
// 每次调用都会新建 PerEventTimeout 计时器，因此超时针对的是"两次 Next 调用的间隔"。
// 可以多次调用直到返回 RaceDone 或 RaceError。
func (r *Race[T, R]) Next(ctx context.Context) RaceResult[T] {
	var timeoutCh <-chan time.Time
	if r.config.PerEventTimeout > 0 {
		timeoutCh = time.After(r.config.PerEventTimeout)
	}

	var abortCh <-chan struct{}
	if r.config.Abort != nil {
		abortCh = r.config.Abort
	}

	// 竞态：事件 vs 超时 vs 中断
	select {
	case <-ctx.Done():
		r.stream.Stop()
		return RaceResult[T]{Kind: RaceAbort, Error: ctx.Err()}

	case <-timeoutCh:
		r.stream.Stop()
		return RaceResult[T]{Kind: RaceTimeout}

	case <-abortCh:
		r.stream.Stop()
		return RaceResult[T]{Kind: RaceAbort}

	case evt, ok := <-r.stream.Events():
		if !ok {
			// channel 关闭，获取最终结果
			_, err := r.stream.Result()
			if err != nil {
				return RaceResult[T]{Kind: RaceError, Error: err}
			}
			return RaceResult[T]{Kind: RaceDone}
		}
		if evt.done {
			if evt.err != nil {
				return RaceResult[T]{Kind: RaceError, Error: evt.err}
			}
			return RaceResult[T]{Kind: RaceDone}
		}
		return RaceResult[T]{Item: evt.value, Kind: RaceEvent}
	}
}

// ============================================================================
// 便利函数：执行带超时的流式请求
// ============================================================================

// StreamWithTimeoutFn 执行一个流式 LLM 请求，返回带超时的 StreamWithTimeout 包装。
//
// 用法:
//
//	timeoutCfg := DefaultStreamTimeoutConfig()
//	timeoutCfg.FirstEventTimeout = 30 * time.Second
//	timeoutCfg.IdleTimeout = 60 * time.Second
//
//	wrapped, err := StreamWithTimeoutFn(ctx, timeoutCfg, func(ctx) (*EventStream[AssistantMessageEvent, AssistantMessage], error) {
//	    return provider.Stream(ctx, model, llmCtx, opts)
//	})
//	if err != nil { ... }
//	msg, err := wrapped.ForEach(ctx, handler)
func StreamWithTimeoutFn[T AssistantMessageEvent, R any](
	ctx context.Context,
	timeoutCfg StreamTimeoutConfig,
	streamFn func(context.Context) (*EventStream[T, R], error),
) (*StreamWithTimeout[T, R], error) {
	innerStream, err := streamFn(ctx)
	if err != nil {
		return nil, err
	}
	wrapped := NewEventStreamWithTimeout(innerStream, timeoutCfg)
	return wrapped, nil
}

// ============================================================================
// 超时配置的辅助函数
// ============================================================================

// WithFirstEventTimeout 设置首帧超时，返回副本。
func (c StreamTimeoutConfig) WithFirstEventTimeout(d time.Duration) StreamTimeoutConfig {
	c.FirstEventTimeout = d
	return c
}

// WithIdleTimeout 设置空闲超时，返回副本。
func (c StreamTimeoutConfig) WithIdleTimeout(d time.Duration) StreamTimeoutConfig {
	c.IdleTimeout = d
	return c
}
