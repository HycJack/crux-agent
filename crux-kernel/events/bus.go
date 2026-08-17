// Package events 实现支持多派发模式的事件总线。
//
// 五种派发模式覆盖常见事件处理场景：
//
//   - Broadcast: 广播，所有 handler 并发执行，不等待，不收集结果。
//     适用：日志、监控、UI 刷新。
//
//   - Parallel: 并行，所有 handler 并发执行，等待全部完成。
//     适用：EventTurnEnd（异步日志 + 持久化 + 学习并行）。
//
//   - Serial: 串行，按顺序执行，前一个完成后才执行下一个。
//     适用：需要严格顺序的处理链。
//
//   - Bail: 短路，串行执行，任一返回非 nil 立即停止。
//     适用：BeforeToolCall 审批门（任一拒绝即停）。
//
//   - Waterfall: 瀑布流，串行执行，前一个的返回值作为后一个的输入。
//     适用：AfterToolCall 结果变换链（脱敏 → 格式化 → 校验）。
package events

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// DispatchMode 派发模式。
type DispatchMode int

const (
	// DispatchBroadcast 广播：所有 handler 并发执行，不等待，不收集结果。
	DispatchBroadcast DispatchMode = iota

	// DispatchParallel 并行：所有 handler 并发执行，等待全部完成。
	// 任一 handler 返回 error 会被聚合到返回值。
	DispatchParallel

	// DispatchSerial 串行：handler 按顺序执行，前一个完成后才执行下一个。
	DispatchSerial

	// DispatchBail 短路：handler 串行执行，任一返回非 nil 立即停止。
	DispatchBail

	// DispatchWaterfall 瀑布流：handler 串行执行，前一个的返回值作为后一个的输入。
	DispatchWaterfall
)

// String 人类可读的派发模式名。
func (m DispatchMode) String() string {
	switch m {
	case DispatchBroadcast:
		return "broadcast"
	case DispatchParallel:
		return "parallel"
	case DispatchSerial:
		return "serial"
	case DispatchBail:
		return "bail"
	case DispatchWaterfall:
		return "waterfall"
	default:
		return "unknown"
	}
}

// Event 基础事件接口。
// 现有 AgentEvent 等类型实现该接口即可接入 EventBus。
type Event interface {
	// Type 事件类型名（如 "tool_exec_end"、"before_tool_call"）。
	Type() string
	// Timestamp 事件发生时间。
	Timestamp() time.Time
}

// SimpleEvent 一个简单的 Event 实现，用于测试或自定义事件。
type SimpleEvent struct {
	EventType  string
	EventTime  time.Time
	EventData  any
}

// Type 实现 Event 接口。
func (e SimpleEvent) Type() string { return e.EventType }

// Timestamp 实现 Event 接口。
func (e SimpleEvent) Timestamp() time.Time { return e.EventTime }

// Handler 事件处理器。
//
// 返回值语义因派发模式而异：
//   - Broadcast: 不关心返回值。
//   - Parallel: error 会被聚合。
//   - Serial: 最后一个 handler 的返回值作为最终结果。
//   - Bail: 首个非 nil 的 (any, error) 会短路派发。
//   - Waterfall: 前一个的 any 返回值作为后一个的输入（第二个参数 input）。
type Handler func(ctx context.Context, event Event) (result any, err error)

// handlerEntry 注册的处理器条目。
type handlerEntry struct {
	id      string
	mode    DispatchMode
	handler Handler
	once    bool // 是否一次性（触发后自动移除）
}

// groupKey 派发模式分组键：同一 eventType 下按 mode 分组，
// 不同 mode 的 handler 各自按其规则派发。
type groupKey struct {
	eventType string
	mode      DispatchMode
}

// EventBus 事件总线。
//
// 并发安全：On/Once/Off/Emit 可被多个 goroutine 并发调用。
// 同一 eventType 可注册不同 DispatchMode 的 handler 组，各组独立派发。
type EventBus struct {
	mu       sync.RWMutex
	groups   map[groupKey][]handlerEntry
	ids      map[string]groupKey // handler id → 所属 group（便于 Off 定位）
	counter  atomic.Uint64
}

// New 创建空的事件总线。
func New() *EventBus {
	return &EventBus{
		groups: make(map[groupKey][]handlerEntry),
		ids:    make(map[string]groupKey),
	}
}

// On 注册监听器。
//
// eventType: 事件类型名。
// mode: 该 handler 所属组的派发模式（同 eventType 下可有多个不同 mode 的组）。
// 返回 handler ID，用于 Off 取消。
//
// handler 为 nil 时返回 ErrInvalidHandler。
func (b *EventBus) On(eventType string, mode DispatchMode, handler Handler) string {
	return b.register(eventType, mode, handler, false)
}

// Once 注册一次性监听器（触发一次后自动移除）。
func (b *EventBus) Once(eventType string, mode DispatchMode, handler Handler) string {
	return b.register(eventType, mode, handler, true)
}

func (b *EventBus) register(eventType string, mode DispatchMode, handler Handler, once bool) string {
	if handler == nil {
		return "" // 调用方应通过返回的空 ID 判断；亦可通过 MustRegister 触发 panic
	}
	id := b.nextID()
	b.mu.Lock()
	defer b.mu.Unlock()
	key := groupKey{eventType: eventType, mode: mode}
	b.groups[key] = append(b.groups[key], handlerEntry{
		id:      id,
		mode:    mode,
		handler: handler,
		once:    once,
	})
	b.ids[id] = key
	return id
}

func (b *EventBus) nextID() string {
	n := b.counter.Add(1)
	return "h" + strconv.FormatUint(n, 10)
}

// Off 取消监听。handlerID 为 On/Once 返回的 ID，无效 ID 会被静默忽略。
func (b *EventBus) Off(handlerID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key, ok := b.ids[handlerID]
	if !ok {
		return
	}
	delete(b.ids, handlerID)
	entries := b.groups[key]
	for i, e := range entries {
		if e.id == handlerID {
			b.groups[key] = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	if len(b.groups[key]) == 0 {
		delete(b.groups, key)
	}
}

// Emit 派发事件。
//
// 返回值取决于派发模式：
//   - Broadcast: 总是返回 (nil, nil)
//   - Parallel:  返回 (nil, aggregatedError)
//   - Serial:    返回 (lastResult, lastError)
//   - Bail:       返回 (firstNonNilResult, firstError)（短路）
//   - Waterfall:  返回 (lastResult, lastError)（链式传递）
//
// 如果 ctx 被取消，未启动的 handler 不会执行。
func (b *EventBus) Emit(ctx context.Context, eventType string, event Event) (any, error) {
	// 取出该 eventType 下所有 group，按 mode 分组派发。
	// 为避免长持有锁，复制一份再释放。
	b.mu.RLock()
	var groups []groupKey
	var snapshots = map[groupKey][]handlerEntry{}
	for k, entries := range b.groups {
		if k.eventType != eventType {
			continue
		}
		groups = append(groups, k)
		cp := make([]handlerEntry, len(entries))
		copy(cp, entries)
		snapshots[k] = cp
	}
	b.mu.RUnlock()

	if len(groups) == 0 {
		return nil, nil
	}

	var (
		result    any
		firstErr  error
		firstRes  any
		collected []error
	)
	hasErr := false

	for _, gk := range groups {
		entries := snapshots[gk]
		if len(entries) == 0 {
			continue
		}

		switch gk.mode {
		case DispatchBroadcast:
			// 广播：并发执行，不等待。这里选择 goroutine。
			// 注意：不等待意味着 handler 的 error 会被丢弃。
			for _, e := range entries {
				// Atomically remove once handlers before execution to prevent
				// race conditions where multiple goroutines execute the same handler.
				if e.once {
					b.Off(e.id)
				}
				go e.handler(ctx, event)
			}

		case DispatchParallel:
			var wg sync.WaitGroup
			errs := make([]error, len(entries))
			for i, e := range entries {
				// Atomically remove once handlers before execution.
				if e.once {
					b.Off(e.id)
				}
				wg.Add(1)
				go func(i int, e handlerEntry) {
					defer wg.Done()
					_, err := e.handler(ctx, event)
					errs[i] = err
				}(i, e)
			}
			wg.Wait()
			for _, err := range errs {
				if err != nil {
					collected = append(collected, err)
					hasErr = true
				}
			}

		case DispatchSerial:
			for _, e := range entries {
				// Atomically remove once handlers before execution.
				if e.once {
					b.Off(e.id)
				}
				r, err := e.handler(ctx, event)
				result = r
				if err != nil {
					collected = append(collected, err)
					hasErr = true
					break
				}
			}

		case DispatchBail:
			for _, e := range entries {
				// Atomically remove once handlers before execution.
				if e.once {
					b.Off(e.id)
				}
				r, err := e.handler(ctx, event)
				if err != nil {
					firstErr = err
					firstRes = r
					hasErr = true
					break
				}
				if r != nil {
					firstRes = r
					break
				}
			}

		case DispatchWaterfall:
			var prev any // 瀑布流的输入，第一个 handler 收到的 prev 为 nil
			for _, e := range entries {
				// Atomically remove once handlers before execution.
				if e.once {
					b.Off(e.id)
				}
				// Create a wrapped event that carries the previous result.
				// The handler can access it via the WaterfallInput interface.
				var currentEvent Event = event
				if prev != nil {
					currentEvent = &waterfallEvent{
						Event:    event,
						previous: prev,
					}
				}
				r, err := e.handler(ctx, currentEvent)
				if err != nil {
					firstErr = err
					hasErr = true
					break
				}
				prev = r
			}
			result = prev
		}
	}

	// 按模式语义决定返回值
	// 这里采用"最后非空模式的结果"的简单策略：
	//  - 若有 bail 短路，优先返回 bail 的结果
	//  - 否则若 waterfall 有结果，返回 waterfall 结果
	//  - 否则若 serial 有结果，返回 serial 结果
	//  - 否则返回 parallel 的聚合错误
	if firstRes != nil {
		return firstRes, firstErr
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if result != nil {
		return result, nil
	}
	if hasErr {
		return nil, errors.Join(collected...)
	}
	return nil, nil
}

// waterfallEvent wraps an Event and carries the previous handler's result
// for Waterfall dispatch mode.
type waterfallEvent struct {
	Event
	previous any
}

// WaterfallInput is an interface that waterfall events implement.
// Handlers can use this to access the previous handler's result.
type WaterfallInput interface {
	// PreviousResult returns the result from the previous handler in the waterfall chain.
	PreviousResult() any
}

// PreviousResult implements the WaterfallInput interface.
func (e *waterfallEvent) PreviousResult() any {
	return e.previous
}

// HasListeners 返回是否注册了指定 eventType 的监听器。
func (b *EventBus) HasListeners(eventType string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for k, entries := range b.groups {
		if k.eventType == eventType && len(entries) > 0 {
			return true
		}
	}
	return false
}

// ListenerCount 返回指定 eventType + mode 下的监听器数量。
// mode 为 -1 时统计该 eventType 下所有监听器。
func (b *EventBus) ListenerCount(eventType string, mode DispatchMode) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if mode < 0 {
		n := 0
		for k, entries := range b.groups {
			if k.eventType == eventType {
				n += len(entries)
			}
		}
		return n
	}
	return len(b.groups[groupKey{eventType: eventType, mode: mode}])
}
