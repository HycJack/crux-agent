# crux-agent-runtime 未完善项清单

## 🔴 严重缺失 (P0)

### 1. agent 包无测试
- 57 个导出函数，0 个测试
- 核心逻辑（runLoop, executeToolCalls, streamAssistantResponse）完全未测试

### 2. 各包未集成
- `autolearn` ↔ `agent` — 自动学习未接入 Agent 循环
- `memory` ↔ `agent` — 长期记忆未注入 system prompt
- `context` ↔ `agent` — 上下文管理未接入 TransformContext 钩子
- `session` ↔ `agent` — 会话持久化未接入事件流

### 3. Agent 状态管理缺失
- `Agent.state.Messages` 未持久化
- `Agent.IsRunning()` 未实现
- `Agent.Abort()` 未实现
- `Agent.Steer()` / `Agent.FollowUp()` 未实现

## 🟡 设计问题 (P1)

### 4. cmd/demo.go 功能不完整
- 只有基本演示，未展示新功能
- 缺少 session/memory/context/autolearn 的使用示例

### 5. 缺少集成示例
- 没有完整的端到端示例
- 参考项目有 `examples/agent-with-skills`

### 6. StreamFn 签名不一致
```go
// agent 包定义
type StreamFn func(context.Context, core.Model, core.Context, core.SimpleStreamOptions) (*core.EventStream[...], error)

// llm 包实际
func StreamSimpleWithContext(ctx, model, llmCtx, opts) (*EventStream, error)
```
- core.Context vs core.ChatRequest 不一致

### 7. go.mod 模块名问题
```go
module crux-agent-runtime  // 应该是 github.com/hycjack/crux-agent-runtime
```

## 🟠 代码质量 (P2)

### 8. 重复代码
- `agent-loop.go` 中的 content manipulation 函数（appendOrUpdateText 等）与 pi-ai-go 完全重复
- Token 计数逻辑在 context 包中，但 agent-loop.go 也有一个

### 9. 错误处理不完善
- agent-loop.go 中的错误只是 log.Printf，没有结构化错误
- 缺少错误恢复机制

### 10. 并发安全
- AgentSession 的 fanout 用了非阻塞发送（drop policy），可能丢失事件
- Agent.state 的读写没有保护

### 11. 缺少 metrics/observability
- 没有 Prometheus metrics
- 没有 OpenTelemetry tracing
- 没有结构化日志

## 🔵 文档/示例 (P3)

### 12. 缺少 CONTRIBUTING.md
### 13. 缺少 Makefile
### 14. 缺少 CI/CD 配置
### 15. 缺少 CHANGELOG.md


---

## 📐 设计文档

详见 [docs/turn-fsm-design.md](docs/turn-fsm-design.md)

### Turn FSM 实现计划

| Phase | 内容 | 工作量 |
|-------|------|--------|
| Phase 1 | 核心 FSM（types, store, machine, trigger）| 1-2 天 |
| Phase 2 | 状态处理器（received, streaming, dispatching...）| 2-3 天 |
| Phase 3 | AgentRunner 集成 | 1-2 天 |
| Phase 4 | session + memory + context + autolearn 集成 | 1-2 天 |
| Phase 5 | SQLite Store | 1 天 |
| **总计** | | **6-10 天** |

---

## 🏗️ Skill 系统 & Sandbox 设计

详见 [docs/architecture-design.md](docs/architecture-design.md)

### 实现计划

| Phase | 内容 | 工作量 |
|-------|------|--------|
| Phase 1 | Skill 系统（registry, 接口, 内置技能）| 2-3 天 |
| Phase 2 | Sandbox 系统（接口, ProcessSandbox, 第三方适配）| 2-3 天 |
| Phase 3 | Harness 层分离（policy, dispatch, approval, hooks）| 3-5 天 |
| Phase 4 | 集成测试 | 2-3 天 |
| **总计** | | **9-14 天** |

### Skill 清单

| 技能 | 优先级 | 说明 |
|------|--------|------|
| terminal | P0 | Shell 命令执行 |
| readfile | P0 | 文件读取 |
| writefile | P0 | 文件写入 |
| listfiles | P1 | 目录列表 |
| memory | P1 | 长期记忆（LLM 可调用）|
| websearch | P2 | 网页搜索 |
| delegate | P2 | 跨 Agent 委托 |

### Sandbox 清单

| 实现 | 优先级 | 说明 |
|------|--------|------|
| NoneSandbox | P0 | 无限制（开发用）|
| ProcessSandbox | P0 | 进程级限制 |
| DockerSandbox | P2 | 容器隔离 |
| E2BAdapter | P2 | E2B 云沙箱 |
