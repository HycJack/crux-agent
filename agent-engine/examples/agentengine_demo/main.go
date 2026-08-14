// Example: 用 crux-kernel 组装 agent-engine
//
// 展示如何通过 Container 注册服务，用桥接函数构造 AgentOptions 和 AgentLoopConfig，
// 并把 Agent 事件桥接到 EventBus 供横切关注点（日志/审批）订阅。
//
// 运行：go run ./examples/agentengine_demo
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/crux-kernel/plugin"
	"github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-kernel/container"
	"github.com/hycjack/crux-kernel/events"
	"github.com/hycjack/agent-engine/integration/agentengine"
)

// --- 横切关注点：日志订阅器 ---
type eventLogger struct{}

func (l *eventLogger) Handle(ctx context.Context, evt events.Event) (any, error) {
	fmt.Fprintf(os.Stderr, "[event] %s @ %s\n", evt.Type(), evt.Timestamp().Format("15:04:05.000"))
	return nil, nil
}

// --- 插件实现：控制台审批门 ---
type consoleApproval struct{}

func (a *consoleApproval) Evaluate(ctx context.Context, toolName, toolID string, args []byte) (plugin.ApprovalResult, string, error) {
	fmt.Fprintf(os.Stderr, "\n[approval] tool=%s id=%s args=%s\n", toolName, toolID, string(args))
	fmt.Fprint(os.Stderr, "[approval] allow? (y/n): ")
	var resp string
	fmt.Scanln(&resp)
	if resp == "y" {
		return plugin.ApprovalAllow, "user approved", nil
	}
	return plugin.ApprovalBlock, "user denied", nil
}

func main() {
	// 1. 创建 Container
	c := container.New()

	// 2. 注册服务（插件 + 横切关注点）
	model := core.Model{ID: "demo-model", Name: "Demo", Provider: "demo", API: "demo"}
	tools := []engine.AgentTool{
		{Name: "bash", Description: "run shell command"},
	}
	comp := engine.CompactionConfig{MaxTokens: 10000}
	approval := &consoleApproval{}

	c.Register(model)
	c.Register(tools)
	c.Register(comp)
	// 接口类型必须用 RegisterAs 注册到接口类型，便于按接口查询
	c.RegisterAs(approval, (*plugin.ApprovalPlugin)(nil))

	// 3. 从 Container 构造 AgentOptions
	opts := agentengine.FromContainer(c)
	agent := engine.New(opts)

	// 4. 创建 EventBus + 桥接 Agent 事件
	bus := events.New()
	cancel := agentengine.BridgeEvents(agent, bus)
	defer cancel()

	// 5. 订阅事件（横切关注点：日志）
	lg := &eventLogger{}
	for _, typ := range []string{
		"agent_start", "turn_start", "turn_end",
		"tool_exec_start", "tool_exec_end", "agent_end",
	} {
		bus.On(typ, events.DispatchSerial, lg.Handle)
	}

	// 6. 从 Container 构造 AgentLoopConfig + 注入 Approval
	cfg := engine.AgentLoopConfig{
		Model:        opts.InitialState.Model,
		Tools:        opts.InitialState.Tools,
		Compaction:   opts.Compaction,
		SystemPrompt: "You are a helpful assistant.",
		GetApiKey:    func() string { return "demo-key" },
	}
	if err := agentengine.ApplyApproval(&cfg, c, nil); err != nil {
		fmt.Fprintf(os.Stderr, "approval not registered: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "=== 组装完成 ===\n")
	fmt.Fprintf(os.Stderr, "Model: %s\n", cfg.Model.ID)
	fmt.Fprintf(os.Stderr, "Tools: %d\n", len(cfg.Tools))
	fmt.Fprintf(os.Stderr, "MaxTokens: %d\n", cfg.Compaction.MaxTokens)
	fmt.Fprintf(os.Stderr, "BeforeToolCall: %v\n", cfg.BeforeToolCall != nil)

	// 实际使用（需真实 LLM API）：
	//   state, err := agent.Run(context.Background(), cfg)
	//   if err != nil { ... }
	//   _ = state

	// 7. 优雅关闭
	if err := c.Dispose(); err != nil {
		fmt.Fprintf(os.Stderr, "dispose error: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "=== 完成 ===\n")
}
