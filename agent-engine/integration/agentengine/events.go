// Package agentengine 提供 crux-kernel 与 agent-engine 之间的桥接。
//
// 它允许使用者通过 Container 组装 Agent 所需的服务（Model / Approval /
// Compaction 等），并把 Agent 的事件桥接到 EventBus，实现横切关注点
// （日志、审批、结果变换等）的统一管理。
//
// agent-engine 保持零侵入：不依赖 crux-kernel，集成代码在此包中提供。
package agentengine

import (
	"time"

	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/crux-kernel/events"
)

// AgentEventType 返回 AgentEvent 对应的事件类型字符串。
//
// 用于把 agent-engine 的 11+ 种 AgentEvent 映射到 EventBus 的事件类型名，
// 使调用方可以按字符串名订阅：
//
//	bus.On("tool_exec_end", events.DispatchParallel, logger.Log)
//	bus.On("turn_end", events.DispatchSerial, persistor.Save)
func AgentEventType(evt engine.AgentEvent) string {
	switch evt.(type) {
	case engine.EventAgentStart:
		return "agent_start"
	case engine.EventAgentEnd:
		return "agent_end"
	case engine.EventTurnStart:
		return "turn_start"
	case engine.EventTurnEnd:
		return "turn_end"
	case engine.EventMessageStart:
		return "message_start"
	case engine.EventMessageUpdate:
		return "message_update"
	case engine.EventMessageEnd:
		return "message_end"
	case engine.EventToolExecStart:
		return "tool_exec_start"
	case engine.EventToolExecUpdate:
		return "tool_exec_update"
	case engine.EventToolExecEnd:
		return "tool_exec_end"
	case engine.EventQueueUpdate:
		return "queue_update"
	case engine.EventRetry:
		return "retry"
	default:
		return "unknown"
	}
}

// eventAdapter 把 engine.AgentEvent 包装为 events.Event 接口。
//
// AgentEvent 本身不实现 events.Event（没有 Type/Timestamp 方法），
// 通过此适配器接入 EventBus。
type eventAdapter struct {
	evt engine.AgentEvent
	ts  time.Time
}

// Type 实现 events.Event 接口。
func (a eventAdapter) Type() string { return AgentEventType(a.evt) }

// Timestamp 实现 events.Event 接口。
func (a eventAdapter) Timestamp() time.Time { return a.ts }

// WrapEvent 把 AgentEvent 包装为 events.Event。
// 时间戳使用调用时刻。
func WrapEvent(evt engine.AgentEvent) events.Event {
	return eventAdapter{evt: evt, ts: time.Now()}
}

// WrapEventAt 把 AgentEvent 包装为 events.Event，使用指定时间戳。
func WrapEventAt(evt engine.AgentEvent, ts time.Time) events.Event {
	return eventAdapter{evt: evt, ts: ts}
}
