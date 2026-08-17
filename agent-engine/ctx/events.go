package ctx

import (
	stdctx "context"
	"sync/atomic"
	"time"

	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/agent-engine/integration/agentengine"
	"github.com/hycjack/crux-kernel/events"
)

// BridgeEvents 把 engine.Agent 的事件桥接到 EventBus。
//
// 复用 integration/agentengine.WrapEvent 把 AgentEvent 包装为 events.Event，
// 供 x.Events().On(typeName, mode, handler) 订阅。
//
// 作用域：调用方通常针对单个 agent / scoped Ctx 调用本函数，事件只桥接到
// 该 Ctx 的自有事件总线，从而天然实现多租户隔离（每个 agent 一个 scoped bus）。
//
// 返回 cancel，cancel 后停止向 bus Emit（agent.Subscribe 不支持取消，
// 通过 atomic flag 软停止，与 integration/agentengine.BridgeEvents 一致）。
func BridgeEvents(agent *engine.Agent, bus *events.EventBus) (cancel func()) {
	var active atomic.Bool
	active.Store(true)
	agent.Subscribe(func(evt engine.AgentEvent) {
		if !active.Load() {
			return
		}
		_, _ = bus.Emit(stdctx.Background(), agentengine.AgentEventType(evt), eventAt(evt))
	})
	return func() { active.Store(false) }
}

// eventAt 包装 AgentEvent，时间戳取当前时刻。
func eventAt(evt engine.AgentEvent) events.Event { return agentengine.WrapEvent(evt) }

// AgentIDEvent 在事件上附带 agent 作用域标签，供 scope 过滤（可选增强）。
type AgentIDEvent struct {
	events.Event
	AgentID string
}

var _ events.Event = (*AgentIDEvent)(nil)

func (e *AgentIDEvent) Type() string         { return e.Event.Type() }
func (e *AgentIDEvent) Timestamp() time.Time { return e.Event.Timestamp() }
