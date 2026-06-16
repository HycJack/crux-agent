/*
 * 功能说明：事件流和流式事件类型定义
 * 
 * 解决的问题：
 * 1. 需要异步流式传输 LLM 响应
 * 2. 需要支持生产者-消费者模式的并发安全
 * 3. 需要定义统一的流式事件类型（文本、思考、工具调用等）
 * 4. 需要支持取消操作和错误处理
 * 
 * 解决方案：
 * 1. 实现 EventStream 泛型结构，支持并发安全的推送和消费
 * 2. 使用 channel 和 mutex 实现线程安全
 * 3. 定义 AssistantMessageEvent 接口和具体事件类型
 * 4. 提供 ForEach 方法支持 context 取消
 * 
 * 应用场景：
 * - 所有 AI 提供者使用 EventStream 返回流式响应
 * - Agent 层通过 ForEach 消费事件流
 * - 支持 SSE 和 WebSocket 传输
 */
package core

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// EventStream is an async event stream for streaming LLM responses.
// || 异步事件流，用于流式传输 LLM 响应
type EventStream[T any, R any] struct {
	ch     chan streamEvt[T] // 事件 channel，缓冲大小 64
	done   chan struct{}     // 完成信号 channel
	stop   chan struct{}     // 停止信号 channel
	result R                 // 最终结果
	err    error              // 错误信息
	closed bool              // 是否已关闭
	mu     sync.Mutex        // 保护并发访问
}

// streamEvt is the internal event type used by EventStream.
// || EventStream 使用的内部事件类型
type streamEvt[T any] struct {
	value T      // 事件值
	err   error  // 错误信息
	done  bool   // 是否完成
}

// NewEventStream creates a new EventStream.
// || 创建新的 EventStream
func NewEventStream[T any, R any]() *EventStream[T, R] {
	return &EventStream[T, R]{
		ch:   make(chan streamEvt[T], 64), // 缓冲大小 64
		done: make(chan struct{}),
		stop: make(chan struct{}),
	}
}

// Push sends an event to the stream. Returns false if the stream is closed,
// the consumer has stopped reading, or the channel buffer is full.
// || 向流发送事件。如果流已关闭、消费者已停止或缓冲区满，返回 false
func (s *EventStream[T, R]) Push(event T) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}

	// Non-blocking send while holding lock to avoid race with End/Error.
	// || 非阻塞发送，持有锁以避免与 End/Error 竞态
	select {
	case <-s.stop:
		s.mu.Unlock()
		return false
	case s.ch <- streamEvt[T]{value: event}:
		s.mu.Unlock()
		return true
	default:
		s.mu.Unlock()
		return false
	}
}

// End signals successful completion with a result.
// All channel operations are done under the lock to avoid races with Push.
// || 发送成功完成信号并附带结果
// || 所有 channel 操作在锁内完成，避免与 Push 竞态
func (s *EventStream[T, R]) End(result R) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.result = result

	// Non-blocking send: if buffer is full, consumer already stopped.
	// || 非阻塞发送：如果缓冲区满，说明消费者已停止
	select {
	case s.ch <- streamEvt[T]{done: true}:
	default:
	}
	close(s.ch)
	close(s.done)
}

// Error signals an error and terminates the stream.
// || 发送错误信号并终止流
func (s *EventStream[T, R]) Error(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.err = err

	select {
	case s.ch <- streamEvt[T]{err: err, done: true}:
	default:
	}
	close(s.ch)
	close(s.done)
}

// Stop signals the producer to stop sending events.
// || 通知生产者停止发送事件
func (s *EventStream[T, R]) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.stop)
	}
}

// Result waits for the stream to complete and returns the final result.
// || 等待流完成并返回最终结果
func (s *EventStream[T, R]) Result() (R, error) {
	<-s.done
	return s.result, s.err
}

// Events returns a channel that yields stream events.
// || 返回产生流事件的 channel
func (s *EventStream[T, R]) Events() <-chan streamEvt[T] {
	return s.ch
}

// ForEach iterates over all events in the stream, calling fn for each one.
// || 遍历流中的所有事件，对每个事件调用 fn
// 参数：
//   ctx - 上下文（支持取消）
//   fn - 事件处理函数
// 返回：
//   最终结果和错误
func (s *EventStream[T, R]) ForEach(ctx context.Context, fn func(T) error) (R, error) {
	var zeroR R
	for {
		select {
		case <-ctx.Done():
			s.Stop()
			return zeroR, ctx.Err()
		case evt, ok := <-s.Events():
			if !ok {
				return s.Result()
			}
			if evt.done {
				if evt.err != nil {
					return zeroR, evt.err
				}
				return s.Result()
			}
			if err := fn(evt.value); err != nil {
				s.Stop()
				return zeroR, err
			}
		}
	}
}

// --- Streaming Events ---
// || --- 流式事件类型 ---

// AssistantMessageEvent is the interface for all streaming events.
// || 所有流式事件的接口
type AssistantMessageEvent interface {
	eventTag()
}

// EventStart signals the start of a streaming response.
// || 表示流式响应开始
type EventStart struct {
	Type      string        `json:"type"`      // 类型：start
	API       KnownAPI      `json:"api"`       // API 协议
	Provider  KnownProvider `json:"provider"`  // 提供者
	Model     string        `json:"model"`     // 模型名称
	Timestamp time.Time     `json:"timestamp"` // 时间戳
}

func (EventStart) eventTag() {}

// EventTextStart signals the start of a text block.
// || 表示文本块开始
type EventTextStart struct {
	Type string `json:"type"` // 类型：text_start
}

func (EventTextStart) eventTag() {}

// EventTextDelta represents a text streaming delta.
// || 表示文本流式增量
type EventTextDelta struct {
	Type  string `json:"type"`  // 类型：text_delta
	Delta string `json:"delta"` // 增量文本
}

func (EventTextDelta) eventTag() {}

// EventTextEnd signals the end of a text block.
// || 表示文本块结束
type EventTextEnd struct {
	Type          string `json:"type"`                    // 类型：text_end
	TextSignature string `json:"textSignature,omitempty"` // 文本签名（用于 Anthropic）
}

func (EventTextEnd) eventTag() {}

// EventThinkingStart signals the start of a thinking block.
// || 表示思考块开始
type EventThinkingStart struct {
	Type string `json:"type"` // 类型：thinking_start
}

func (EventThinkingStart) eventTag() {}

// EventThinkingDelta represents a thinking streaming delta.
// || 表示思考流式增量
type EventThinkingDelta struct {
	Type  string `json:"type"`  // 类型：thinking_delta
	Delta string `json:"delta"` // 增量思考内容
}

func (EventThinkingDelta) eventTag() {}

// EventThinkingEnd signals the end of a thinking block.
// || 表示思考块结束
type EventThinkingEnd struct {
	Type              string `json:"type"`                    // 类型：thinking_end
	ThinkingSignature string `json:"thinkingSignature,omitempty"` // 思考签名
}

func (EventThinkingEnd) eventTag() {}

// EventToolCallStart signals the start of a tool call.
// || 表示工具调用开始
type EventToolCallStart struct {
	Type string `json:"type"` // 类型：tool_call_start
	ID   string `json:"id"`   // 工具调用 ID
	Name string `json:"name"` // 工具名称
}

func (EventToolCallStart) eventTag() {}

// EventToolCallDelta represents a tool call arguments delta.
// || 表示工具调用参数增量
type EventToolCallDelta struct {
	Type           string `json:"type"`           // 类型：tool_call_delta
	ID             string `json:"id"`             // 工具调用 ID
	ArgumentsDelta string `json:"argumentsDelta"` // 参数增量
}

func (EventToolCallDelta) eventTag() {}

// EventToolCallEnd signals the end of a tool call.
// || 表示工具调用结束
type EventToolCallEnd struct {
	Type      string          `json:"type"`      // 类型：tool_call_end
	ID        string          `json:"id"`        // 工具调用 ID
	Arguments json.RawMessage `json:"arguments"` // 完整参数（JSON）
}

func (EventToolCallEnd) eventTag() {}

// EventDone signals successful completion.
// || 表示成功完成
type EventDone struct {
	Type    string           `json:"type"`    // 类型：done
	Message AssistantMessage `json:"message"` // 最终消息
}

func (EventDone) eventTag() {}

// EventError signals an error.
// || 表示错误
type EventError struct {
	Type  string `json:"type"`  // 类型：error
	Error error  `json:"error"` // 错误信息
}

func (EventError) eventTag() {}

// AssistantMessageEventStream is a type alias for the event stream.
// || 事件流的类型别名
type AssistantMessageEventStream = EventStream[AssistantMessageEvent, AssistantMessage]

// CalculateCost computes the cost of a request from per-million-token rates.
// || 根据每百万 token 的费率计算请求费用
// 参数：
//   model - 模型信息（包含定价）
//   usage - token 使用统计
// 返回：
//   费用明细
func CalculateCost(model Model, usage Usage) CostBreakdown {
	// 计算各项费用：token 数 * 单价 / 1,000,000
	inputCost := float64(usage.Input) * model.Cost.Input / 1_000_000
	outputCost := float64(usage.Output) * model.Cost.Output / 1_000_000
	cacheReadCost := float64(usage.CacheRead) * model.Cost.CacheRead / 1_000_000
	cacheWriteCost := float64(usage.CacheWrite) * model.Cost.CacheWrite / 1_000_000

	return CostBreakdown{
		Input:      inputCost,
		Output:     outputCost,
		CacheRead:  cacheReadCost,
		CacheWrite: cacheWriteCost,
		Total:      inputCost + outputCost + cacheReadCost + cacheWriteCost,
	}
}
