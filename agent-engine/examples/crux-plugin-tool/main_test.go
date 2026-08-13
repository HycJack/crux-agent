package main

import (
	"context"
	"encoding/json"
	"testing"

	cp "github.com/hycjack/crux-plugin"

	"github.com/hycjack/agent-engine/plugin"
)

// TestAdaptToToolPlugin 验证 crux-plugin.ToolAdapter → plugin.ToolPlugin 的适配。
func TestAdaptToToolPlugin(t *testing.T) {
	a := mockWeatherTool()

	tp := adaptToToolPlugin(a)

	// 元数据透传
	if tp.Name() != "weather.get" {
		t.Fatalf("Name = %q, want weather.get", tp.Name())
	}
	if tp.Description() == "" {
		t.Fatal("Description should not be empty")
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(tp.Parameters(), &schema); err != nil {
		t.Fatalf("Parameters should be valid JSON schema: %v", err)
	}

	// 执行透传到子进程 Execute
	res, err := tp.Execute(context.Background(), "id", []byte(`{"city":"北京"}`), nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if len(res.Content) == 0 {
		t.Fatal("expected non-empty content")
	}

	// 错误路径：子进程返回 error → IsError=true
	res, _ = tp.Execute(context.Background(), "id", []byte(`not-json`), nil)
	if !res.IsError {
		t.Fatalf("expected IsError for bad args, got %+v", res)
	}
}

// TestAdaptPluginTools 验证两步适配串起来后能被 engine.AgentTool 调用。
func TestAdaptPluginTools(t *testing.T) {
	adapters := []cp.ToolAdapter{mockWeatherTool(), mockFileTool()}
	tools := adaptPluginTools(adapters)
	if len(tools) != 2 {
		t.Fatalf("len(tools) = %d, want 2", len(tools))
	}

	// 每一个工具都能通过 engine.AgentTool.Execute 调用，且语义正确。
	result, err := tools[0].Execute(context.Background(), "id",
		json.RawMessage(`{"city":"北京"}`), func(json.RawMessage) {})
	if err != nil {
		t.Fatalf("AgentTool.Execute error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}
}

// TestToolPluginImplementsInterface 编译期断言：adapterToolPlugin 满足 plugin.ToolPlugin。
func TestToolPluginImplementsInterface(t *testing.T) {
	var _ plugin.ToolPlugin = (*adapterToolPlugin)(nil)
}
