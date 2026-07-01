# crux-agent-runtime 模块边界分析

> 版本: v0.1.0 | 更新: 2026-06-30
> 状态: 分析记录，待后续重构

---

## 1. 问题背景

`crux-agent-runtime` 目前包含以下包：

```
crux-agent-runtime/
├── agent/       ← AgentLoop 核心引擎
├── session/     ← 会话持久化
├── context/     ← Token 计数 + 上下文压缩
├── memory/      ← 长期记忆
├── autolearn/   ← 自动学习
├── tools/       ← 内置 Agent 工具（bash, glob, grep, read, write, webfetch）
├── sandbox/     ← 仅 DESIGN.md，未实现
├── skills/      ← 仅 DESIGN.md，未实现
├── cmd/         ← CLI 入口（repl, demo, compaction 等）
└── docs/        ← 设计文档
```

这些包的职责层次不一致：Engine 核心、可选插件、应用层工具、测试入口混在一个模块里。

---

## 2. 分析结论

### 2.1 应保留在 Engine 中的包

| 包 | 理由 |
|---|------|
| `agent/` | Agent 循环核心，Engine 的本质 |
| `session/` | AgentLoop 运行时需要会话管理，是内建依赖 |
| `context/` | AgentLoop 运行时需要上下文压缩，是内建依赖 |

### 2.2 应作为可选插件/独立模块的包

| 包 | 理由 | 建议 |
|---|------|------|
| `memory/` | 长期记忆系统，通过 Hook 接入，非核心 | 保持独立 module，或留在 Engine 但通过接口解耦 |
| `autolearn/` | 从对话中自动提取记忆，纯可选功能，依赖 memory | 随 memory 一起独立 |

### 2.3 应移到上层应用层的包

| 包 | 理由 | 建议目标 |
|---|------|---------|
| `tools/` | 具体工具实现（bash, 文件操作），不是 Engine 契约 | `crux-agent-chat/tools/` |
| `cmd/` | main 入口代码不应在库模块中 | 独立的可执行项目，或 `crux-agent-chat/cmd/` |
| `skills/` | 空壳 DESIGN.md，无代码 | 删除，或移到 `crux-agent-harness/skills/` |
| `sandbox/` | 空壳 DESIGN.md，无代码 | 删除，或移到独立项目 |

---

## 3. 推荐重构方案

### 方案 A：最小改动

```
crux-agent-runtime/        ← 保留 Engine 核心 + memory/autolearn
├── agent/
├── session/
├── context/
├── memory/
├── autolearn/
└── docs/

crux-agent-chat/           ← 吸收 tools + cmd
├── tools/                 ← 从 runtime 移入
├── cmd/crux-agent/        ← 从 runtime 移入
├── cmd/testagent/
├── agent/
├── harness/
├── command/
└── ui/
```

删掉 runtime 中的 `sandbox/` 和 `skills/` 空壳包。

### 方案 B：严格分层

```
crux-agent-engine/         ← 新 module，零工具依赖
├── agent/
├── session/
└── context/

crux-agent-memory/         ← 新 module，可选
├── memory/
└── autolearn/

crux-agent-tools/          ← 新 module，通用工具包
├── bash/
├── files/
└── ...

crux-agent-chat/           ← 应用层
├── tools/
├── cmd/
├── agent/
├── harness/
├── command/
└── ui/
```

---

## 4. 各包的依赖关系（目标状态）

```
crux-agent-chat
  ├── crux-agent-engine    ← agent, session, context
  ├── crux-agent-memory    ← memory, autolearn（可选）
  ├── crux-agent-tools     ← 具体工具实现（可选）
  └── crux-ai

crux-agent-engine  → crux-ai
crux-agent-memory  → crux-ai
crux-agent-tools   → nil（纯标准库）
crux-agent-harness → crux-ai（保持现状）
```

---

## 5. 风险与注意事项

1. **`memory/` 是否该留在 Engine 内？**
   - 如果留下：方便开箱即用，但 engine 依赖膨胀
   - 如果移出：engine 更纯净，但用户需要自行组装
   - 折中：engine 定义 `MemoryProvider` 接口，memory 作为默认实现独立 module

2. **`tools/` 拆分后 import path 变化**
   - 当前 `crux-agent-runtime/tools` 在 `chat-app` 中被引用
   - 需要同步更新所有 import

3. **`cmd/` 拆分后需保留 REPL 功能**
   - 当前 `cmd/repl.go` 是开发调试工具
   - 移入 `crux-agent-chat` 后可以作为 `cmd/crux-agent-repl/`

---

## 6. Session 存储的归属分析

### 6.1 现状：两套 session 代码

当前项目中存在两个独立的 `session/` 包：

| 特性 | `crux-agent-runtime/session/` | `crux-agent-harness/session/` |
|------|-------------------------------|-------------------------------|
| `SessionTreeEntry` 树形结构 | ✅ | ✅ |
| JSONL 持久化 | ✅ | ✅ |
| SQLite 持久化 | ✅ | ❌ |
| Branch 分支管理 | ✅ | ❌ |
| `SessionMetadata` 追踪 | ❌ | ✅ |

这是**代码重复**问题。两个包做的是同一件事，但分散在两处，导致功能碎片化。

### 6.2 Session 的最佳归属：Engine 内建，接口解耦

Session 应该归属于 Engine（`crux-agent-runtime`），理由如下：

- **AgentLoop 运行时依赖 session** — 每次 LLM 调用、工具执行后都需要持久化消息，这是 Engine 的核心流程，不应依赖外部注入
- **开箱即用** — 用户拿到 Engine 就能用，不需要额外组装 session

但 session **不应该作为硬编码内建模块**，而应该通过**接口解耦**：

```
crux-agent-engine/
├── agent/             ← AgentLoop 通过 SessionStorage 接口消费 session
│   └── types.go       ← SessionStorage interface 定义在此
├── session/           ← 默认实现（Memory/JSONL/SQLite），实现 SessionStorage 接口
│   ├── storage.go     ← SessionStorage 接口默认实现
│   ├── jsonl.go       ← JSONLStorage (impl)
│   └── sqlite.go      ← SQLiteStorage (impl)
├── session/types.go   ← SessionTreeEntry 等公共类型
└── context/
```

```go
// 接口定义在 agent/ 中（被消费方定义），session/ 包实现它
type SessionStorage interface {
    Append(entries ...SessionTreeEntry) error
    ReadAll() ([]SessionTreeEntry, error)
    Close() error
}
```

### 6.3 这样设计的好处

1. **消除重复代码** — `crux-agent-harness/session/` 可以删除，统一使用 runtime 的 session
2. **存储可替换** — 用户实现 `SessionStorage` 接口即可接入 PostgreSQL、Redis 等
3. **Engine 默认可用** — 如果没有自定义存储，Engine 自动使用 SQLite/JSONL
4. **Harness 无需另起炉灶** — 直接复用同一个接口和默认实现

### 6.4 对 Harness session 的处理

`crux-agent-harness/session/` 中 `SessionMetadata`（追踪 token 用量、压缩次数等）是 Harness 层特有的关注点。有两种处理方式：

**方式 A（推荐）**：将 metadata 追踪能力移到 Harness 自己的模块中，通过订阅 AgentEvent 来收集 token 数据，不再维护独立的 session 存储。

```go
// crux-agent-harness 通过订阅事件追踪 token，而非独立维护 session 存储
h.subscriber = func(e agent.AgentEvent) {
    switch ev := e.(type) {
    case agent.EventMessageEnd:
        metadata.TotalTokens += ev.Message.Usage.TotalTokens
    }
}
```

**方式 B**：将 `SessionMetadata` 合并到 runtime/session 中，作为 session 的可选附加信息。

---

## 7. 当前建议（更新）

**优先采用方案 A（最小改动）**：
1. 把 `tools/` 移到 `crux-agent-chat/`
2. 把 `cmd/` 移到 `crux-agent-chat/`
3. 删除 `sandbox/` 和 `skills/` 空壳
4. `memory/` 和 `autolearn/` 暂时保留不动，后续再评估是否需要独立
5. **合并两套 session**：runtime 的 session 通过接口解耦，harness 的 session 删除，其 metadata 追踪通过事件订阅实现

这样可以立即清理模块边界，同时不破坏现有代码结构。
