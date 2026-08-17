package ctx

// Plugin 是 agent-engine 的可组合单元。
//
// 每个能力（session/compaction/approval/memory/autolearn/observe/…）实现
// 本接口，经 Ctx.Mount 装配。约定：
//   - Mount 内解析依赖服务（x.Get）、把能力挂到统一扩展主干（x.MergeHooks）、
//     订阅事件（x.Events().On）。
//   - Mount 返回的清理函数（dispose）在 Ctx.Dispose 时逆序调用；
//     插件应自持资源，卸载即撤销其挂载的全部 effect。
type Plugin interface {
	// Name 唯一标识插件（用于生命周期 fiber 与日志）。同名重复 Mount 会失败。
	Name() string

	// Mount 把自身装配进 Ctx。返回的清理函数在 Dispose 时逆序调用。
	Mount(x *Ctx) (dispose func() error, err error)
}
