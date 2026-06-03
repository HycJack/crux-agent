# oh-my-pi 项目研究报告

## 一、项目概述

`oh-my-pi` 是一个从 `pi-mono` 移植而来的 **AI Coding Agent** 项目，使用 **Bun + TypeScript + Rust** 构建。它包含多个核心包：

| 包 | 说明 |
|---|---|
| `packages/agent` | 底层 Agent 框架（Harness、Compaction、事件流） |
| `packages/coding-agent` | 上层编码 Agent（CLI、TUI、工具、会话管理） |
| `packages/ai` | AI 模型提供者抽象层（Anthropic、OpenAI、Google 等） |
| `packages/tui` | 终端 UI 组件库 |
| `crates/brush-core-vendored` | Rust Shell 引擎（含 Bash 内置命令） |
| `crates/brush-builtins-vendored` | Rust Shell 内置命令实现 |
| `crates/pi-natives` | Rust 原生扩展（PTY、FS、Glob、PTY 等） |
| `crates/pi-shell` | Shell 最小化优化引擎 |
| `crates/pi-ast` | Rust AST 工具 |
| `crates/pi-iso` | 文件系统快照与克隆工具 |

---

## 二、Harness 引擎核心

### 2.1 agent-loop.ts — Agent 主循环

**`packages/agent/src/agent-loop.ts`**

核心入口：`agentLoop()` 和 `agentLoopContinue()`

#### 容错设计亮点

1. **工具执行边界校验** — `coerceToolResult()` 函数在所有工具结果进入循环前进行校验。如果工具返回格式异常（缺少 `content` 数组），会自动插入一个错误结果，而不是让整个会话崩溃

2. **中止信号分层管理** — 支持多层 AbortSignal：
   - 用户 `signal`（全局取消）
   - `harmonyAbortController`（GPT-5 Harmony 泄漏检测触发的中止）
   - `toolCallCapAbortController`（单次最大 tool call 数量限制）
   - 使用 `AbortSignal.any()` 合并多个信号

3. **Harmony Leak 检测与恢复** — 检测 GPT-5 Harmony 协议泄漏，最多重试 2 次，重试时增加 0.05 温度。

4. **工具参数验证容错** — 支持 `lenientArgValidation` 模式，验证失败时仍传递原始参数

5. **Intent 提取容错** — 当 `intent` 函数抛异常时，工具执行不受影响

6. **工具执行并发控制** — 支持 `shared`（共享）和 `exclusive`（独占）两种并发模式

7. **中止工具结果的占位处理** — 当 tool call 被中止时，生成占位工具结果以保持 `tool_use/tool_result` 配对

---

### 2.2 types.ts — 类型系统容错

**`packages/agent/src/types.ts`**

关键 Hook：

| Hook | 类型 | 容错用途 |
|---|---|---|
| `beforeToolCall` | 返回 `{ block?: boolean; reason?: string }` | 阻止危险工具调用 |
| `afterToolCall` | 覆盖 `content`, `details`, `isError` | 修正/增强工具结果 |
| `getSteeringMessages` | 返回中断消息 | 用户中断时插队 |
| `getFollowUpMessages` | 返回跟进消息 | Agent 停止后继续 |

---

### 2.3 append-only-context.ts — 上下文稳定性

**`packages/agent/src/append-only-context.ts`**

1. **StablePrefix** — 系统提示和工具规格只计算一次并冻结，复用相同的字符序列
2. **AppendOnlyLog** — 消息只增长，之前的轮次从不重新序列化
3. **滚动摘要检测** — 通过 FNV 风格滚动哈希检测消息的内联重写

---

## 三、Compaction 压缩与上下文管理

### 3.1 compaction.ts — 上下文压缩

**`packages/agent/src/compaction/compaction.ts`**

- **Token 估算** — 使用本地方言分词器，图像固定 1200 token
- **切割点检测** — 保留最近消息，永远不会在工具结果处切割
- **摘要生成** — 本地（LLM）/远程（OpenAI）双模式
- **远程压缩降级** — 远程失败时优雅回退到本地摘要

### 3.2 pruning.ts — 工具输出裁剪

- 保留最近 `protectTokens` 的工具输出完整
- 保护 `skill` 和 `skillRead` 工具的结果

### 3.3 errors.ts — 压缩错误类型

- `CompactionCancelledError` — 区分取消和失败
- `CompactionOutcome` — `"ok" | "cancelled" | "failed"`

---

## 四、重试策略

### 4.1 Non-compaction Retry Policy

**`docs/non-compaction-retry-policy.md`**

#### 可重试错误
- 瞬时传输/信封故障
- 过载/提供商标记错误
- 速率限制/使用限制/请求过多
- HTTP 429, 500, 502, 503, 504
- 网络/连接/socket 失败

#### 退避算法
```
attempt 1: 2000 ms
attempt 2: 4000 ms
attempt 3: 8000 ms
```

#### 凭据/模型回退
- 使用限制错误时自动切换凭据
- 模型回退链（`retry.fallbackChains`）

---

## 五、Run Collector 运行统计

**`packages/agent/src/run-collector.ts`**

- 工具状态分类：`"ok" | "error" | "skipped" | "blocked" | "timeout" | "aborted"`
- 使用 `WeakMap` 键绑定到 `Span`，无跨调用泄露

---

## 六、整体架构图

```
┌──────────────────────────────────────────────────────────────┐
│                   agent-loop.ts (主循环)                      │
├──────────────────────────────────────────────────────────────┤
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐   │
│  │ AbortSignal  │  │  coerce      │  │ Harmony Leak     │   │
│  │ 多层信号管理   │  │ ToolResult   │  │ 检测与恢复机制    │   │
│  └──────────────┘  └──────────────┘  └──────────────────┘   │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐   │
│  │ 工具参数验证   │  │ 并发执行控制   │  │ before/after    │   │
│  │ 容错模式      │  │ shared/excl  │  │ ToolCall Hooks  │   │
│  └──────────────┘  └──────────────┘  └──────────────────┘   │
├──────────────────────────────────────────────────────────────┤
│           append-only-context.ts (上下文稳定性)               │
├──────────────────────────────────────────────────────────────┤
│              compaction.ts (上下文压缩)                       │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐   │
│  │ Token 估算   │  │ 切割点检测    │  │ 摘要生成          │   │
│  │ 图像1200tkn │  │ 不切工具结果  │  │ 本地/远程双模式   │   │
│  └──────────────┘  └──────────────┘  └──────────────────┘   │
├──────────────────────────────────────────────────────────────┤
│                   重试策略 (Retry)                            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐   │
│  │ 指数退避      │  │ 凭据/模型切换 │  │ 重试事件流       │   │
│  │ 2000/4000/8  │  │ fallback链   │  │ 订阅通知         │   │
│  └──────────────┘  └──────────────┘  └──────────────────┘   │
├──────────────────────────────────────────────────────────────┤
│              run-collector.ts (运行指标)                      │
│  工具状态: ok / error / skipped / blocked / timeout / aborted │
└──────────────────────────────────────────────────────────────┘
```

---

## 七、总结

### 容错设计亮点

1. **多层 AbortSignal 管理** — 用户信号、Harmony 检测信号、ToolCall 上限信号三层合并
2. **工具结果边界校验** — 所有第三方工具结果在进入循环前强制校验格式
3. **参数验证容错** — `lenientArgValidation` 允许跳过验证
4. **Hook 驱动的容错** — `beforeToolCall` 可以阻止执行，`afterToolCall` 可以修正结果
5. **上下文稳定性** — Append-only log 确保 Provider 缓存命中率
6. **压缩降级** — 远程压缩失败时自动回退到本地摘要
7. **重试与压缩互斥** — 确保两者不会同时触发

### 异常处理模式

| 模式 | 应用场景 |
|---|---|
| 重试 (Retry) | 网络超时、API 限流 |
| 退避 (Backoff) | 指数级延迟，支持凭据/模型切换 |
| 降级 (Degrade) | 远程压缩失败 → 本地摘要 |
| 中止 (Abort) | 多层 AbortSignal 管理 |
| 裁剪 (Prune) | 工具输出过长时自动裁剪 |
| 压缩 (Compaction) | 上下文溢出时自动压缩 |
| 结果校验 (Coerce) | 工具返回格式异常时自动修复 |

---

*文档生成日期: 2026-06-03*
*研究范围: oh-my-pi 的 Harness、Compaction、重试策略与异常处理机制*
