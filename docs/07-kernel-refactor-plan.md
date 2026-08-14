# Crux 底座（Kernel）重构与瘦身方案

> 目标：以 DeepSeek Harness（dsh）+ Cordis 的架构为参照，把 crux 目前的
> `crux-runtime` / `agent-engine` / `crux-agent-runtime` / `crux-agent-harness`
> 四者混沌的边界整理成一个清晰的分层，并解决命名与重复实现问题。
>
> 版本：v0.1 · 2026-08-14

---

## 0. TL;DR（结论先行）

1. **改名**：`crux-runtime` 更名为 **`crux-kernel`**（备选 `crux-base` / `crux-container`）。「runtime」这个名不副实——它承载的不再是「运行时/执行」，而是**容器 + 契约 + 装配（Cordis 的对等物）**。
2. **移动**：`agent-engine/plugin`（全部 8 个接口 + 配套类型）**移入 kernel**，成为 `kernel/plugin`。契约属于容器所在层。
3. **不移动**：`agent-engine/engine`（AgentLoop/Agent/Pipeline 等**执行实现**）**留在 agent-engine**。它是领域逻辑，不是机制。
4. **去重**：`crux-agent-runtime`（旧 loop）与 `crux-agent-harness`（旧 concern）**退役合并**进 `agent-engine`，结束「两套 agent 循环 + 两套 concern」的并存。
5. **依赖方向修正**：kernel 保持「零外部依赖、可被任何实现方反向依赖」的底座定位；把目前 kernel 依赖 agent-engine 的 `integration/*` 桥接包迁出。

最终分层一句话：**kernel = 通用底座（Cordis 对等物）；agent-engine = agent 领域实现；crux-ai = 跨厂商类型底层。**

---

## 1. 现状盘点（为什么需要重构）

### 1.1 当前模块与依赖矩阵（已核实）

| 模块 | 模块路径 | 依赖的本地模块 |
|---|---|---|
| `crux-ai` | `github.com/hycjack/crux-ai` | （无，真·底层） |
| `crux-plugin` | `github.com/hycjack/crux-plugin` | （无，纯 stdlib，子进程 IPC 框架） |
| `agent-engine` | `github.com/hycjack/agent-engine` | `crux-ai` |
| `crux-runtime` | `github.com/hycjack/crux-runtime` | `crux-ai` + `agent-engine` + `crux-plugin` + `crux-agent-harness` |
| `crux-agent-harness` | `crux-agent-harness` | `crux-ai` |
| `crux-agent-runtime` | `crux-agent-runtime` | `crux-ai` |
| `crux-agent-chat` | `crux-agent-chat` | `crux-ai` + `harness` + `runtime` |
| `crux-agent-tui` | `crux-agent-tui` | `crux-ai` + `agent-engine` |
| `chat-app` | `chat-app` | `crux-ai` + `agent-engine` + `crux-plugin` |

### 1.2 四个核心问题

#### 问题 A：`crux-runtime` 名不副实，且依赖方向反了
- `crux-runtime` 里装的是 `container`（服务容器）、`fiber`（插件生命周期）、`events`（事件总线）——这是**通用机制/框架**，与「agent 执行」无关。
- 但它现在 **依赖** `agent-engine`（`integration/agentengine` 桥接 engine 事件）、`crux-agent-harness`（`integration/harness` 注册 concern）、`crux-plugin`。也就是说「底座」反而向上依赖了「上层实现」。
- **这不符合 dsh 中 Cordis 的定位**：Cordis 是纯粹的通用容器，不依赖任何 agent/harness 包，所有 agent 能力（`ctx.llm`、`ctx.sessions`…）是跑在 Cordis 之上的插件。

#### 问题 B：`runtime` 这个名字已经误导
「runtime」在 agent 语境里通常指「agent 执行运行时」（本仓库的 `agent-engine/engine` 才是真正的执行运行时）。`crux-runtime` 实际是「容器/事件/插件框架」，且我们即将把契约也放进去——它更像 **Cordis 那种 kernel/base/framework**，而不是 runtime。改名是必要的。

#### 问题 C：Plugin 契约（agent 接口）放错了层
`agent-engine/plugin/types.go` 定义了 8 个接口（Session/Context/Memory/AutoLearn/Tool/Approval/Checkpoint/Observe）及配套类型，**只依赖 `crux-ai/core`**。它是「契约/Service Definition」，按 dsh 的做法契约应归属容器所在层（`ctx.<key>`），而非深埋在 agent-engine 里。当前 `crux-runtime/integration/harness` 为了拿这些接口被迫 import `agent-engine`——层次混乱的根源。

#### 问题 D：存在「两套实现」的重复
这是比命名更严重的问题：
- **两个 agent 循环**：`agent-engine/engine/*`（新，干净，可按接口注入 + Pipeline/Stage 抽象）vs `crux-agent-runtime/agent/*`（旧，无插件抽象）。两者都实现 agent loop，功能重叠。
- **两套 concern**：`agent-engine/defaults/*`（session/context/approval/checkpoint/observe/token/compaction，按 plugin 接口实现）vs `crux-agent-harness/*`（同样的 session/context/approval/checkpoint/observe/token）。
- 两个新模块（`agent-engine`、`crux-runtime`）明显是后来加的「新方向」，但旧模块（`crux-agent-runtime`、`crux-agent-harness`）没有退役，导致维护面翻倍、认知混乱。

---

## 2. 目标架构

### 2.1 目标模块分层（对照 dsh/Cordis）

```
┌─────────────────────────────────────────────────────────────┐
│  应用层（可独立采用/跳过）                                      │
│  crux-agent-chat  ·  crux-agent-tui  ·  chat-app             │
└──────────────────────────┬──────────────────────────────────┘
                           │ 依赖实现层
┌──────────────────────────▼──────────────────────────────────┐
│  领域实现层  agent-engine  （对照 dsh 的 core/* + 各 provider）│
│  ├─ engine/      AgentLoop / Agent / Pipeline / Stage /      │
│  │               AgentEvent / AgentTool / CompactionConfig   │
│  ├─ defaults/    plugin 接口的默认实现                        │
│  │               (session/context/memory/autolearn/          │
│  │                approval/checkpoint/observe/token/compaction)│
│  └─ api/         OpenAI 兼容 HTTP 层                          │
└──────────────────────────┬──────────────────────────────────┘
                           │ 实现 plugin 契约 + 反向依赖 kernel
┌──────────────────────────▼──────────────────────────────────┐
│  底座层  crux-kernel  （原名 crux-runtime，对照 vendored Cordis）│
│  ├─ container/   服务注册/查询（reflect.Type 索引）+ 生命周期   │
│  ├─ fiber/       插件生命周期状态机（pending→active→disposed）  │
│  ├─ events/      事件总线（5 种派发模式）                      │
│  ├─ plugin/      ★ 从 agent-engine 迁入：8 个 agent 契约接口   │
│  └─ assembly/(新) profile / bundle / patch 声明式装配          │
└──────────────────────────┬──────────────────────────────────┘
                           │ 唯一底层类型依赖
┌──────────────────────────▼──────────────────────────────────┐
│  类型底层  crux-ai  core/ + providers/                       │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 依赖方向（最终，单向无环）

```
crux-ai  ←  crux-kernel  ←  agent-engine  ←  crux-agent-tui / chat-app
                    ↑
        crux-agent-chat ──（旧 harness/runtime 退役后不再依赖它们）
```

- **`crux-kernel` 依赖 `crux-ai/core`**（因为 `plugin` 契约引用了 `core.Message` 等类型）。
- `agent-engine` 依赖 `crux-kernel/plugin`（拿契约）+ `crux-ai`，**不再反向要求 kernel 依赖它**。
- 退役 `crux-agent-runtime` 与 `crux-agent-harness` 后，`crux-agent-chat` 只依赖 `crux-ai` + `agent-engine`。

---

## 3. 该移动 vs 不该移动的完整清单

### 3.1 ✅ 应该移入 `crux-kernel`（契约层）

| 内容 | 现位置 | 理由 |
|---|---|---|
| `plugin` 全部 8 接口 + 全部配套类型（`SessionTreeEntry`/`SessionContext`/`ContextStats`/`ApprovalResult`/`ToolResult`/`CheckpointInfo`/`Trigger`/枚举） | `agent-engine/plugin/types.go` | 契约（Service Definition）归属容器层，与 dsh 的 `ctx.<key>` 一致 |
| `crux-runtime` 现有 `container` / `fiber` / `events` | 已在 `crux-runtime/` | 保留 |
| `assembly`（profile/bundle/patch 声明式装配，**新增**） | — | 机制层，与 dsh 的 bundle/patch 对应 |

### 3.2 ❌ 不应移入 `crux-kernel`（留在 agent-engine）

| 内容 | 理由 |
|---|---|
| `engine/*`（`Agent`/`AgentLoop`/`AgentLoopConfig`/`CompactionConfig`/`AgentEvent`+12 事件/`AgentTool`/`Stage`/`RunState`/`Pipeline`/`StreamFn`） | 是 **agent 领域执行逻辑**，不是通用机制；放进 kernel 会让底座依赖 `crux-ai/ai` + tiktoken，破坏「轻量可嵌入、零依赖」定位，并造成 kernel↔engine 纠缠 |
| `defaults/*`（各 plugin 接口的实现） | 是**实现**，不是契约；契约在 kernel，实现在领域层 |
| `api/*`（OpenAI 兼容 HTTP） | 领域/接口输出层 |
| `core.Message` / `Model` / `EventStream` / `ContentBlock`… | 留在 `crux-ai/core`（真·底层，谁都别动） |

> **判断标准一句话**：「跟 agent 无关的**通用机制/契约**进 kernel；跟 agent 强相关的**领域执行与实现**留 agent-engine；**跨厂商类型**留 crux-ai。」

### 3.3 🗑️ 退役（合并进 agent-engine）

| 模块 | 处理 | 理由 |
|---|---|---|
| `crux-agent-runtime` | **删除**，其功能由 `agent-engine/engine` 承接 | 旧 agent 循环，无插件抽象，与新 engine 重复 |
| `crux-agent-harness` | **删除**，有用的 concern 实现迁入 `agent-engine/defaults`（大部分已在 defaults 存在） | 与 `defaults/*` 重复 |

> 合并前需逐一核对 defaults 与 harness 的功能覆盖（见 §5 迁移步骤），确保不丢能力。

---

## 4. 命名决策

### 4.1 为什么 `runtime` 不合适

- 字面「运行时」在 agent 语境里指向「agent 执行运行时」——但真正的执行运行时在 `agent-engine/engine`。
- 改名后的模块承载：容器 + 事件 + 生命周期 + **agent 契约** + 未来装配层。这是一个**通用框架/底座**，等价于 dsh 里 vendored 的 **Cordis**（Cordis 就叫 Cordis，不叫 runtime）。
- `runtime` 还会与 `crux-agent-runtime`（旧模块）造成同名混淆，加剧认知负担。

### 4.2 候选名与建议

| 候选 | 评价 |
|---|---|
| **`crux-kernel`** ⭐ 推荐 | 准确表达「一切运行的底座/内核」，无「执行运行时」误导；短、好打 |
| `crux-base` | 也可，但「base」较泛，易与各模块里已有的 `base` 子目录混淆 |
| `crux-container` | 只覆盖了容器，装不下 events/plugin/assembly |
| `crux-framework` | 准确但略长 |
| `crux-core` | ❌ 与 `crux-ai/core` 路径撞名，绝对避免 |
| `crux` | 保留给顶层聚合概念，不宜占用 |

**结论：采用 `crux-kernel`**（后续文档统一使用该名；`crux-runtime` 全部替换）。

> 附带解决一个已有的命名碰撞：`crux-plugin`（子进程 IPC 模块）与「plugin 契约」是两回事。契约移入 `kernel/plugin` 后，`crux-plugin` 可考虑在文档中明确标注为「子进程 IPC 框架」，或后续视情况改名 `crux-ipc`，以彻底消除歧义。

---

## 5. 迁移步骤（可分批、每步可编译、可回滚）

### 阶段 0：基线固化（0.5 天）
- [x] 备份现状 —— 已打 git tag `kernel-refactor-baseline`（基于 commit `7210ad2`）。
- [ ] 绘制目标依赖图，加入 CI 依赖门禁（`go list -deps` 或 `godepgraph` 断言「kernel 不依赖 agent-engine / harness / runtime」）。

### 阶段 1：新建 `crux-kernel` 并迁入契约（1 天）✅ 已完成
- [x] `crux-runtime` → `crux-kernel`，全局替换模块路径 `github.com/hycjack/crux-runtime` → `github.com/hycjack/crux-kernel`（go.mod / go.work / import / replace）。
- [x] `git mv agent-engine/plugin crux-kernel/plugin`；更新 `types.go` 包注释，去除对旧 `crux-agent-runtime` 的引用。
- [x] 建立 `crux-kernel/plugin` 对 `crux-ai/core` 的依赖。
- [x] `agent-engine` 改依赖 `crux-kernel/plugin`；所有 `defaults/*` 的 import 改为 `crux-kernel/plugin`。
- [x] `go build ./... && go test ./...` 全量通过（kernel / agent-engine / harness / apps 均绿）。

### 阶段 2：桥接包迁出 kernel（1 天，让 kernel 纯净）✅ 已完成
- [x] `integration/agentengine`（engine 事件 → EventBus、approval 注入）→ 迁到 `agent-engine/integration/agentengine`，**kernel 不再依赖 engine**。
- [x] `integration/harness`（注册 harness concern）→ **迁到 `crux-agent-harness/integration`**（采纳「kernel 不依赖 harness」的决定，随 harness 走；harness 退役并入 defaults 时再收敛）。
- [x] `integration/cruxplugin`（进程生命周期适配）：保留在 kernel（只依赖 fiber + crux-plugin）。
- [x] 验证：`crux-kernel` 的 `container/fiber/events/plugin` 不再 import `agent-engine / crux-agent-harness / crux-agent-runtime`，只依赖 `crux-ai/core` + `crux-plugin`。
  - 相关 demo 归属：`agentengine_demo` → `agent-engine/examples/`；`harness_demo` → `crux-agent-harness/examples/`；`cruxplugin_demo`/`multitenant_demo`/`fake_plugin` 留在 `crux-kernel/examples/`。

### 阶段 3：去重/退役旧模块 ✅ 已完成（`crux-agent-runtime` 已删除；`crux-agent-harness` 已并入 `agent-engine/harness/`）
- [x] 核对 `agent-engine/engine/*` 与 `crux-agent-runtime/agent/*`：**engine 是 runtime/agent 的严格超集**（同名同字段，额外多了 ProviderStreamFn / EventQueueUpdate / EventRetry / Pipeline）。
- [x] 所有调用方（`crux-agent-chat`）从 `crux-agent-runtime/agent` 无缝切换到 `github.com/hycjack/agent-engine/engine`（纯 import 路径替换，API 无变化）。
- [x] 从 go.work 移除 `crux-agent-runtime`，删除目录（其真重复 role 已被 engine 取代）。
- [x] 核对 `agent-engine/defaults/*` 与 `crux-agent-harness/*`：approval/context 目的重叠但 API 形状不同（new 为 plugin 契约式）；`session.SessionManager`/`observe.RunCollector`/`skills.LoadSkills` 在 defaults **无对应（gap）**。
  - **本阶段采用“合并模块”而非“逐包并入 defaults”**：把 `crux-agent-harness/{approval,checkpoint,context,observe,prompt,session,skills,token,integration}` 整体迁入 `agent-engine/harness/`，使其不再是独立模块。
- [x] 从 go.work 移除 `crux-agent-harness`，删除目录；`crux-agent-chat` 改依赖 `agent-engine`（内含 harness），去掉对 harness/runtime 的独立依赖。
- [x] 全量 `go build ./... && go test ./... && go vet ./...` 全绿。
- [ ] **(遗留)** `agent-engine/harness/*`（旧 concern 实现）与 `agent-engine/defaults/*`（新 plugin 契约实现）仍在同一模块内并存，属同一职责的两套实现。后续可逐步把 `harness` 的功能合并进 `defaults`（尤其 approval/context/session），最终删掉 `harness/`。这不是模块层问题，不影响依赖方向。

### 阶段 4：装配层 + 文档（2 天）
- [ ] 在 `crux-kernel/assembly` 实现轻量声明式装配（YAML/Go struct：profile→bundle(patch)→plugin 挂载顺序），参考 dsh 的 cordis.yml/patch 与现有 `harnessreg` 模式。
- [ ] 提供 `--dump-config` 等价物（打印最终插件树）。
- [ ] 更新 `README.zh-CN.md` / 各 docs 的模块职责表与依赖图。
- [ ] 清理：删除迁移期残留注释、统一 module 路径 `github.com/hycjack/crux-*`。

---

## 6. 重构后收益

1. **依赖单向干净**：`crux-ai → crux-kernel → agent-engine → 应用`，无环、可独立采用/跳过。
2. **底座（kernel）真正通用**：等价 dsh 的 Cordis，任何 Go 程序都能嵌入，不背 agent/LLM 包袱。
3. **契约独立演进**：`kernel/plugin` 是唯一「Service Definition」来源，新增能力（sandbox/mcp/web）只需在 kernel 加接口 + 独立模块实现，不动 engine。
4. **消灭双实现**：仅此一项就把「两个 agent 循环 + 两套 concern」收敛为一套，显著降低维护与认知成本。
5. **命名清晰**：`kernel`（底座）/`engine`（执行）/`defaults`（实现）/`ai`（类型）职责一目了然，也不再与旧的 `crux-agent-runtime` 撞名。

---

## 7. 待确认 / 风险

- **`crux-kernel` 是否接受依赖 `crux-ai/core`？** 我的建议：接受（crux-ai 是自有底层模块，非第三方），换取「契约与容器同层」的简洁。若你更追求「kernel 零依赖、连 crux-ai 都不碰」（纯 Cordis 式），则 `plugin` 契约应留在 agent-engine 或单独立 `crux-contracts` 模块——需要二选一。**本文采用前者（merged）**。
- **`agent-engine` 与 `crux-kernel` 是否合并为一个 module？** 我的建议：**保持分离**。agent-engine 作为「仅依赖 crux-ai + tiktoken」的轻量可嵌入库是一大卖点（想只用 agent 循环、不背容器的人直接拉它）；合并会破坏这点。这与 dsh 中 Cordis 与 core/agent-loop 分属不同包的做法一致。
- **退役 `crux-agent-runtime`/`crux-agent-harness` 的破坏面**：`crux-agent-chat` 是目前唯一同时依赖它们的模块，切换需谨慎，先跑通端到端再删。
- **`assembly`（声明式装配）**：Go 生态无 cordis.yml，本方案只做「对已编译 provider 排序 + patch」，**不做**反射动态加载（除非确有热加载需求）。

---

## 8. 附录：关键命令与验证

```bash
# 全量构建
cd crux-agent
go build ./...

# 全量测试
go test ./...

# 依赖门禁（断言 kernel 不依赖 agent-engine / harness / runtime）
cd crux-kernel
go list -deps ./container ./fiber ./events ./plugin | grep -E 'agent-engine|crux-agent-(harness|runtime)' && echo "FAIL: kernel leaks deps" || echo "PASS"
```
