// Command crux-plugin-tool 演示如何把 crux-plugin 的子进程工具
// （ToolAdapter）适配成 agent-engine 的两个消费方：
//
//	1. plugin.ToolPlugin  —— agent-engine 的抽象工具接口
//	2. engine.AgentTool    —— 引擎实际执行的 Agent 工具（loop.go 消费它）
//
// 链路：
//
//	crux-plugin.ToolAdapter ──adaptToToolPlugin──▶ plugin.ToolPlugin
//	       ──toAgentTool──▶ engine.AgentTool ──(inject)──▶ engine.Agent
//
// 本示例不需要真实子进程：用一个实现了 crux-plugin.ToolAdapter 语义的
// 假插件（mockToolPlugin）来模拟 `Manager.RegisterPluginTools()` 的输出，
// 然后走完整的适配 → 注册 → 工具执行 → 回流 LLM 的流程。
//
// 运行（示例自身带 mock provider，无需任何 API key）:
//
//	go run . -query "北京的天气怎么样？顺便读一下 /tmp/notes.txt"
//
// 也可以用真实模型跑（需要 API key）:
//
//	set ANTHROPIC_API_KEY=...   # 或 OPENAI_API_KEY / ...
//	go run . -provider anthropic [-query "..."]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/agent-engine/plugin"
	cp "github.com/hycjack/crux-plugin"
	"github.com/hycjack/crux-ai/ai"
	"github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/providers"

	// Force provider registration via init()
	_ "github.com/hycjack/crux-ai/providers"
)

var (
	providerFlag = flag.String("provider", "mock", "provider id (mock/anthropic/openai/...)")
	modelFlag    = flag.String("model", "claude-sonnet-4-20250514", "model id")
	queryFlag    = flag.String("query", "你是插件适配示例，请用 get_weather 查一下北京天气", "initial query")
)

// ─────────────────────────────────────────────────────────────────────────
// 第 0 步：模拟 crux-plugin 的 ToolAdapter。
//
// 真实场景中这些 ToolAdapter 由 `Manager.RegisterPluginTools(ctx)` 产生，
// 每个 adapter 的 Execute 闭包会对插件子进程发一次 JSON-RPC tool.execute。
// 这里用一个进程内实现来等价模拟，避免示例依赖真实子进程。
// ─────────────────────────────────────────────────────────────────────────

// mockWeatherTool 模拟一个跑在子进程里的天气插件工具。
// 为了让示例自包含，它的 Execute 直接返回假数据，等价于子进程返回的
// ToolExecuteResult.Result（一个字符串）。
func mockWeatherTool() cp.ToolAdapter {
	return cp.ToolAdapter{
		Name:        "weather.get",
		Description: "Get current weather for a city. Returns a short forecast.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"city": map[string]interface{}{
					"type":        "string",
					"description": "City name",
				},
			},
			"required": []string{"city"},
		},
		PluginID: "weather-plugin",
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				City string `json:"city"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("parse args: %w", err)
			}
			return fmt.Sprintf(`{"city":%q,"temp_c":22,"condition":"partly cloudy","humidity":60}`, p.City), nil
		},
	}
}

// mockFileTool 模拟另一个子进程工具，演示多工具注册。
func mockFileTool() cp.ToolAdapter {
	return cp.ToolAdapter{
		Name:        "file.read",
		Description: "Read a text file from disk.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{"type": "string"},
			},
			"required": []string{"path"},
		},
		PluginID: "fs-plugin",
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("parse args: %w", err)
			}
			b, err := os.ReadFile(p.Path)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// 第 1 步：crux-plugin.ToolAdapter → plugin.ToolPlugin（本示例核心）。
//
// 这就是「把 crux-plugin 适配成 ToolPlugin」的粘合层。ToolPlugin 是
// agent-engine 的抽象接口（agent-engine/plugin/types.go），适配器把
// 子进程的 Execute 包装成进程内接口，这样引擎/上层代码只依赖抽象契约，
// 不依赖具体的 crux-plugin 模块。
// ─────────────────────────────────────────────────────────────────────────

type adapterToolPlugin struct {
	name        string
	description string
	parameters  json.RawMessage
	execute     func(ctx context.Context, args json.RawMessage) (string, error)
}

// adaptToToolPlugin 把一个 crux-plugin.ToolAdapter 包装成 plugin.ToolPlugin。
//
// 语义映射：
//   - Name/Description/Parameters → 原样透传
//   - ToolAdapter.Execute(ctx, args) (string, error) 返回的是「结果字符串」，
//     正好就是插件对 tool.execute 的应答文本，直接放进 ToolResult.Content
//     的 TextContent 里回给 LLM。
//   - onUpdate 回调透传给引擎的流式更新（这里暂不产生中间量）。
//
// 注意：ToolAdapter.Execute 不返回 agent-engine 的
// Terminate/Details/IsError 语义，所以这里 IsError 由 error 推导，
// Terminate 固定为 false（crux-plugin v1 无终止机制），Details 留空。
func adaptToToolPlugin(a cp.ToolAdapter) plugin.ToolPlugin {
	params, _ := json.Marshal(a.Parameters)
	return &adapterToolPlugin{
		name:        a.Name,
		description: a.Description,
		parameters:  params,
		execute:     a.Execute,
	}
}

func (t *adapterToolPlugin) Name() string        { return t.name }
func (t *adapterToolPlugin) Description() string { return t.description }
func (t *adapterToolPlugin) Parameters() []byte  { return t.parameters }

func (t *adapterToolPlugin) Execute(ctx context.Context, toolCallID string, params []byte, onUpdate func([]byte)) (plugin.ToolResult, error) {
	// crux-plugin 的 Execute 收 json.RawMessage 参数并返回结果字符串。
	out, err := t.execute(ctx, params)
	if err != nil {
		return plugin.ToolResult{
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}
	return plugin.ToolResult{
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: out}},
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────
// 第 2 步：plugin.ToolPlugin → engine.AgentTool。
//
// AgentLoop 实际执行的是 engine.AgentTool（engine/loop.go 通过
// findTool → tool.Execute 调用）。这个转换器把任意 ToolPlugin 变成
// 引擎可直接注入的工具。
// ─────────────────────────────────────────────────────────────────────────

func toAgentTool(tp plugin.ToolPlugin) engine.AgentTool {
	return engine.AgentTool{
		Name:        tp.Name(),
		Description: tp.Description(),
		Parameters:  json.RawMessage(tp.Parameters()),
		Execute: func(ctx context.Context, id string, args json.RawMessage, onUpdate func(json.RawMessage)) (engine.AgentToolResult, error) {
			// json.RawMessage 是 []byte 的别名，但 Go 类型系统不把它当同型；
			// 这里做一层签名桥接，把引擎的 json.RawMessage 回调转成 ToolPlugin 的 []byte。
			bridge := func(b []byte) { onUpdate(json.RawMessage(b)) }
			r, err := tp.Execute(ctx, id, args, bridge)
			if err != nil {
				return engine.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: err.Error()}},
					IsError: true,
				}, nil
			}
			return engine.AgentToolResult{
				Content:   r.Content,
				Details:   r.Details,
				IsError:   r.IsError,
				Terminate: r.Terminate,
			}, nil
		},
	}
}

// adaptPluginTools 是「第 1 + 2 步」的合并入口：直接把一批
// crux-plugin.ToolAdapter 变成引擎能用的 []engine.AgentTool。
// 单独抽出这两个适配器是为了可组合：上层若只想拿 ToolPlugin（例如走
// HTTP/SDK 后端）可以只调第 1 步；要走引擎则把两步串起来。
func adaptPluginTools(adapters []cp.ToolAdapter) []engine.AgentTool {
	var out []engine.AgentTool
	for _, a := range adapters {
		out = append(out, toAgentTool(adaptToToolPlugin(a)))
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────
// 第 3 步：跑一个真正能交互的 agent，验证适配后的工具被引擎执行。
// ─────────────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()

	// 模拟 crux-plugin.Manager.RegisterPluginTools() 的产物。
	adapters := []cp.ToolAdapter{mockWeatherTool(), mockFileTool()}
	fmt.Fprintln(os.Stderr, "[example] 模拟插件工具列表:")
	for _, a := range adapters {
		fmt.Fprintf(os.Stderr, "  - %s (plugin=%s)\n", a.Name, a.PluginID)
	}

	// 走完整适配：ToolAdapter → ToolPlugin → AgentTool。
	tools := adaptPluginTools(adapters)
	fmt.Fprintf(os.Stderr, "[example] 已适配 %d 个工具为 engine.AgentTool\n", len(tools))

	model, err := resolveModel(*providerFlag, *modelFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "model lookup failed:", err)
		os.Exit(1)
	}

	state := &engine.AgentState{
		Model:         model,
		SystemPrompt:  "You are a demo agent. Use the plugin-provided tools when asked.",
		Tools:         tools,
		ToolExecution: engine.ToolExecParallel,
	}
	agent := engine.New(engine.AgentOptions{InitialState: state})

	// 事件打印：工具执行过程可见。
	agent.Subscribe(func(evt engine.AgentEvent) {
		switch e := evt.(type) {
		case engine.EventToolExecStart:
			fmt.Fprintf(os.Stderr, "  → tool %s(%s)\n", e.ToolName, truncate(string(e.Args), 120))
		case engine.EventToolExecEnd:
			mark := "ok"
			if e.IsError {
				mark = "ERR"
			}
			fmt.Fprintf(os.Stderr, "  ← tool %s [%s] (%d bytes)\n", e.ToolName, mark, len(e.Result))
		}
	})

	ctx := context.Background()

	if isFaux(model) {
		// mock provider：faux LLM 只回一行文本、不会真的发起工具调用，
		// 所以我们手动驱动两个适配后的工具，证明它们在引擎执行路径上可用。
		fmt.Fprintln(os.Stderr, "[example] mock provider：直接驱动适配后的引擎工具，验证适配层在真实执行路径上工作。")
		fmt.Fprintln(os.Stderr, "[example] 用真实模型（如 -provider anthropic + API key）可看到 LLM 实际触发这些工具。")
		verifyExecute(ctx, tools[0], `{"city":"北京"}`)
		verifyExecute(ctx, tools[1], `{"path":"notes.txt"}`)
		// 再让 engine.Agent 跑一轮，验证适配后的工具能正常注册进 AgentState
		// 并进入执行路径而不报错。
		runAgent(agent, ctx, *queryFlag)
	} else if *queryFlag != "" {
		// 真实模型：让 LLM 在 agent 循环里真正调用适配后的插件工具。
		runAgent(agent, ctx, *queryFlag)
	}

	fmt.Fprintln(os.Stderr, "[example] done")
}

// runAgent 驱动 engine.Agent 跑一轮，遇错即退出。
func runAgent(agent *engine.Agent, ctx context.Context, query string) {
	if _, err := agent.Run(ctx, core.UserMessage{
		Role: core.MessageRoleUser, Content: query, Timestamp: time.Now(),
	}); err != nil {
		fmt.Fprintln(os.Stderr, "agent run error:", err)
		os.Exit(1)
	}
}

// verifyExecute 在不依赖 LLM 的情况下直接调用一个 AgentTool，验证适配层
// 在引擎的执行路径上是通的。
func verifyExecute(ctx context.Context, tool engine.AgentTool, args string) {
	result, err := tool.Execute(ctx, "demo-id", json.RawMessage(args), func(json.RawMessage) {})
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", tool.Name, err)
		return
	}
	prefix := "✓"
	if result.IsError {
		prefix = "✗"
	}
	for _, b := range result.Content {
		if t, ok := b.(core.TextContent); ok {
			fmt.Fprintf(os.Stderr, "  %s %s(%s) → %s\n", prefix, tool.Name, args, truncate(t.Text, 100))
		}
	}
}

func resolveModel(provider, modelID string) (core.Model, error) {
	if provider == "mock" {
		return core.Model{
			ID: "mock", Name: "mock",
			API: providers.FauxAPI, // core.GetProvider 会路由到 crux-ai 内建 faux provider
			Input: []core.Modality{core.ModalityText},
		}, nil
	}
	return ai.GetModel(core.KnownProvider(provider), modelID)
}

func isFaux(m core.Model) bool { return m.API == "faux" }

// ─── misc ────────────────────────────────────────────────────────────────

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
