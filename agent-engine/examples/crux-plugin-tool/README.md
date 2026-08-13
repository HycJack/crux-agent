# crux-plugin-tool — 把 crux-plugin 适配成 ToolPlugin 的示例

这个目录是一个**独立示例模块**，演示如何把
[`crux-plugin`](../../../crux-plugin)（子进程 JSON-RPC 插件系统）的工具
适配成 `agent-engine` 的两个消费方：

1. **`plugin.ToolPlugin`** — agent-engine 的抽象工具接口
   （`agent-engine/plugin/types.go`）
2. **`engine.AgentTool`** — 引擎实际执行的 Agent 工具（`engine/loop.go` 消费）

## 适配链路

```
crux-plugin.ToolAdapter ──adaptToToolPlugin──▶ plugin.ToolPlugin
       ──toAgentTool──▶ engine.AgentTool ──(inject)──▶ engine.Agent
```

- `adaptToToolPlugin`（`main.go`）：`crux-plugin.ToolAdapter` → `plugin.ToolPlugin`
  —— 把子进程工具的 `Execute` 闭包包装成进程内的 `ToolPlugin` 接口。
- `toAgentTool`（`main.go`）：`plugin.ToolPlugin` → `engine.AgentTool`
  —— 把任意抽象工具转换成引擎可直接注入的工具。
- `adaptPluginTools`：两者的合并入口，`[]ToolAdapter` → `[]engine.AgentTool`。

## 为什么是独立模块？

`agent-engine` 的核心（`engine/`、`plugin/`）**设计上不依赖** `crux-plugin`
（见 `agent-engine/DESIGN.md` 8.2 节）。适配代码需要同时 import 两者，所以
放在独立的示例模块里，作为「胶水层 + 演示」，保持 agent-engine 核心零外部依赖。

## 运行

示例自带 mock provider，无需任何 API key：

```bash
go run . -query "查一下北京天气"
```

用真实模型（让 LLM 真正触发插件工具）：

```bash
set ANTHROPIC_API_KEY=...   # 或 OPENAI_API_KEY / ...
go run . -provider anthropic -query "北京的天气怎么样？顺便读一下 notes.txt"
```

## 测试

```bash
go test ./...
```

`main_test.go` 断言：
- `adaptToToolPlugin` 的元数据透传与执行/错误语义；
- 两步串起来后 `engine.AgentTool.Execute` 可用；
- `adapterToolPlugin` 编译期满足 `plugin.ToolPlugin` 接口。

## 文件

| 文件 | 说明 |
|------|------|
| `main.go` | 适配器实现 + 可运行演示（含 mock 插件工具） |
| `main_test.go` | 适配层单元测试 |
| `go.mod` | 独立模块，`replace` 指向仓库内相关模块 |
| `notes.txt` | demo 用到的示例文本文件（`file.read` 工具读取它） |
