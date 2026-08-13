// Module crux-plugin-tool — 把 crux-plugin 的 ToolAdapter 适配成
// agent-engine 的 ToolPlugin / engine.AgentTool 的可运行示例。
//
// 这是一个独立示例模块：它同时依赖 agent-engine 与 crux-plugin，
// 充当两者之间的「适配层 + 演示」。agent-engine 的核心（engine/）本身
// 不依赖 crux-plugin（见 DESIGN.md 8.2 节），适配代码放在这里正是为了
// 保持 agent-engine 核心零外部依赖这一设计约束。
module github.com/hycjack/agent-engine/examples/crux-plugin-tool

go 1.25.0

require (
	github.com/hycjack/agent-engine v0.0.0
	github.com/hycjack/crux-ai v0.0.1
	github.com/hycjack/crux-plugin v0.0.0
)

replace (
	github.com/hycjack/agent-engine => ../..
	github.com/hycjack/crux-ai => ../../../crux-ai
	github.com/hycjack/crux-plugin => ../../../crux-plugin
)
