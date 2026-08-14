// Example: 用 harness Register 辅助组装完整 Agent
//
// 展示如何用 agent-engine/harness/integration 一行注册各 concern，
// 再用 integration/agentengine 把它们桥接到 AgentLoopConfig。
//
// 运行：go run ./examples/harness_demo
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/crux-kernel/plugin"
	"github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-kernel/container"
	"github.com/hycjack/crux-kernel/events"
	"github.com/hycjack/agent-engine/integration/agentengine"
	harnessreg "github.com/hycjack/agent-engine/harness/integration"

	ctxm "github.com/hycjack/agent-engine/harness/context"
)

// 事件日志订阅器
type eventLogger struct{}

func (l *eventLogger) Handle(ctx context.Context, evt events.Event) (any, error) {
	fmt.Fprintf(os.Stderr, "[event] %s\n", evt.Type())
	return nil, nil
}

// 用户审批回调（处理 ApprovalAsk）
func askHandler(ctx context.Context, toolName, toolID string, args []byte) (plugin.ApprovalResult, string, error) {
	fmt.Fprintf(os.Stderr, "[ask] approve %s? auto-approve for demo\n", toolName)
	return plugin.ApprovalAllow, "auto-approved by demo", nil
}

func main() {
	c := container.New()
	defer c.Dispose()

	// 1. 注册 harness 各 concern（每行一个）
	sessionsDir := filepath.Join(os.TempDir(), "harness-demo-sessions")
	defer os.RemoveAll(sessionsDir)
	if err := harnessreg.RegisterSession(c, sessionsDir); err != nil {
		fmt.Fprintf(os.Stderr, "register session: %v\n", err)
	}

	model := core.Model{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000}
	ctxCfg := ctxm.DefaultPipelineConfig(model, 128000)
	if err := harnessreg.RegisterContext(c, ctxCfg); err != nil {
		fmt.Fprintf(os.Stderr, "register context: %v\n", err)
	}

	// 注册为 plugin.ApprovalPlugin（注册后既可查 *approval.Gate 也可查 plugin.ApprovalPlugin）
	if err := harnessreg.RegisterApprovalAsPlugin(c, harnessreg.ApprovalConfig{}); err != nil {
		fmt.Fprintf(os.Stderr, "register approval: %v\n", err)
	}

	if err := harnessreg.RegisterCheckpoint(c); err != nil {
		fmt.Fprintf(os.Stderr, "register checkpoint: %v\n", err)
	}

	// 2. 注册 agent-engine 直接需要的服务
	tools := []engine.AgentTool{
		{Name: "bash", Description: "shell"},
		{Name: "read_file", Description: "read"},
	}
	c.Register(model)
	c.Register(tools)

	// 3. 从 Container 构造 AgentOptions
	opts := agentengine.FromContainer(c)
	agent := engine.New(opts)

	// 4. 创建 EventBus + 桥接 Agent 事件
	bus := events.New()
	cancel := agentengine.BridgeEvents(agent, bus)
	defer cancel()

	lg := &eventLogger{}
	for _, typ := range []string{"agent_start", "turn_start", "turn_end", "tool_exec_start", "tool_exec_end", "agent_end"} {
		bus.On(typ, events.DispatchSerial, lg.Handle)
	}

	// 5. 构造 AgentLoopConfig
	cfg := engine.AgentLoopConfig{
		Model:        opts.InitialState.Model,
		Tools:        opts.InitialState.Tools,
		Compaction:   opts.Compaction,
		SystemPrompt: "You are a helpful assistant.",
		GetApiKey:    func() string { return "demo-key" },
	}

	// 从 Container 注入 Approval（已注册为 plugin.ApprovalPlugin）
	if err := agentengine.ApplyApproval(&cfg, c, askHandler); err != nil {
		fmt.Fprintf(os.Stderr, "apply approval: %v\n", err)
	}

	// 6. 总结
	fmt.Fprintf(os.Stderr, "=== 组装完成 ===\n")
	fmt.Fprintf(os.Stderr, "Model: %s\n", cfg.Model.ID)
	fmt.Fprintf(os.Stderr, "Tools: %d (bash, read_file)\n", len(cfg.Tools))
	fmt.Fprintf(os.Stderr, "BeforeToolCall: %v\n", cfg.BeforeToolCall != nil)
	fmt.Fprintf(os.Stderr, "EventBus: 订阅 6 种事件类型\n")
	fmt.Fprintf(os.Stderr, "\n实际使用：\n  state, err := agent.Run(ctx, cfg)\n")

	// 实际调用（需真实 LLM API）
	// state, err := agent.Run(context.Background(), cfg)
	// _ = state
	_ = context.Background()

	fmt.Fprintf(os.Stderr, "=== 完成 ===\n")
}
