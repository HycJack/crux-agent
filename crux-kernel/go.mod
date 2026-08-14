// Module crux-kernel — 统一底座层（原名 crux-runtime）。
// 提供 Container（服务注册/查询 + 资源清理）、PluginFiber（插件生命周期状态机）、
// EventBus（多派发模式事件总线）、plugin（agent 契约接口）。
//
// 核心包（container/fiber/events/plugin）仅依赖 crux-ai/core，不依赖任何 agent 实现。
// integration/cruxplugin 依赖 crux-plugin，提供 PluginFiber 包装 + 自动重启 + 热重载。
module github.com/hycjack/crux-kernel

go 1.25.0

require (
	github.com/hycjack/crux-ai v0.0.1
	github.com/hycjack/crux-plugin v0.0.0
)

replace (
	github.com/hycjack/crux-ai => ../crux-ai
	github.com/hycjack/crux-plugin => ../crux-plugin
)
