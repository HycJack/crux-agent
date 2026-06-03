# pi-mono 项目研究报告

## 一、项目概述

`pi-mono` 是一个 **monorepo（单体仓库）** 项目，核心是一个 **AI Coding Agent**（AI 编码助手），由多个子包组成：

| 子包 | 说明 |
|------|------|
| `packages/agent` | 底层 Agent 框架，包含 Harness 核心 |
| `packages/coding-agent` | 编码 Agent 实现，上层应用 |
| `packages/ai` | AI 模型提供者抽象层 |
| `packages/tui` | 终端 UI 组件库 |
| `packages/web-ui` | Web UI 前端 |
| `go-ai` | Go 语言版本的 AI 适配层 |

本报告重点研究 **容错 (Fault Tolerance)**、**异常处理 (Exception Handling)** 以及 **Harness 相关** 的架构设计。

---

## 二、Harness 核心架构

### 2.1 agent-harness.ts — Agent 运行引擎

**路径**: `packages/agent/src/harness/agent-harness.ts`

这是整个 Agent 系统的核心入口，定义了 `runAgent()` 函数。其容错设计包括：

#### 运行控制参数
- **`maxTurns`** — 最大轮次限制，防止无限循环
- **`maxTokens`** — 最大 Token 限制，防止上下文无限膨胀
- **`signal?: AbortSignal`** — 支持外部取消信号，实现优雅终止

#### 核心流程
1. 初始化 Agent 上下文（Session、Hooks、环境等）
2. 进入主循环：
   - 调用 LLM 获取响应
   - 执行工具调用
   - 处理结果
   - 检查终止条件
3. 返回最终结果或错误

### 2.2 types.ts — 类型定义

**路径**: `packages/agent/src/harness/types.ts`

关键类型定义：

```typescript
interface RunAgentOptions {
  signal?: AbortSignal;
  maxTurns?: number;
  maxTokens?: number;
  hooks?: Partial<Hooks>;
  // ... 其他配置
}

interface Hooks {
  beforeTurn?: (context) => Promise<void>;
  afterTurn?: (context) => Promise<void>;
  onError?: (error, context) => Promise<void>;
  // ... 生命周期 Hook
}
```

**容错设计**：
- 所有关键参数都有默认值
- Hooks 系统允许外部注入错误处理逻辑
- `AbortSignal` 支持超时和外部取消

### 2.3 Hooks 系统

**路径**: `packages/agent/docs/hooks.md`

Hook 生命周期：

| Hook | 触发时机 | 用途 |
|------|----------|------|
| `beforeTurn` | 每轮开始前 | 校验、预处理 |
| `afterTurn` | 每轮结束后 | 清理、统计 |
| `onError` | 发生错误时 | 错误处理、恢复 |
| `onMessage` | 收到消息时 | 消息监控 |
| `onToolCall` | 调用工具时 | 工具拦截 |

Hooks 是整个系统的 **容错枢纽**，通过它可以在不修改核心代码的情况下：
- 添加自定义重试逻辑
- 注入错误恢复策略
- 实现监控和日志

### 2.4 Session 会话管理

**路径**: `packages/agent/src/harness/session/session.ts`

会话管理的容错特性：

| 特性 | 说明 |
|------|------|
| **持久化** | 支持将 Session 保存到磁盘 |
| **恢复** | 支持从中断处恢复运行 |
| **JSONL 存储** | 增量写入，防数据丢失 |
| **内存/磁盘双模式** | `memory-repo.ts` / `jsonl-repo.ts` |

关键机制：
- `jsonl-storage.ts` — 每行一个 JSON 对象，追加写入，损坏一行不影响其他行
- `memory-storage.ts` — 内存模式，适合测试和临时运行
- `repo-utils.ts` — 存储工具函数

### 2.5 Compaction 压缩机制

**路径**: `packages/agent/src/harness/compaction/compaction.ts`

当上下文过长时自动触发的压缩机制：

1. **触发条件**：Token 数量超过阈值
2. **压缩策略**：
   - 将早期消息摘要为简短描述
   - 保留最近的完整消息
   - 保留系统提示和工具定义
3. **分支摘要** (`branch-summarization.ts`)：
   - 在分支切换时生成分支摘要
   - 保证跨分支上下文不丢失

### 2.6 环境抽象

**路径**: `packages/agent/src/harness/env/nodejs.ts`

提供 Node.js 运行时环境抽象：

- 文件读写操作
- 进程管理
- 环境变量访问
- 所有操作都有 try-catch 包裹

---

## 三、容错与异常处理机制

### 3.1 重试机制

**相关文件**：
- `packages/coding-agent/test/agent-session-retry.test.ts`
- `packages/ai/test/openai-completions-retry.test.ts`

特性：
- API 调用失败自动重试
- 可配置重试次数（默认 3 次）
- 指数退避策略（Exponential Backoff）
- 区分可重试错误（网络超时）和不可重试错误（认证失败）

### 3.2 网络容错

**相关文件**：
- `packages/coding-agent/test/suite/regressions/3317-network-connection-lost-retry.test.ts`
- `packages/ai/src/utils/node-http-proxy.ts`

场景：
- 网络断连检测与重连
- HTTP 代理故障切换
- WebSocket 断线重连（Codex 场景）

### 3.3 上下文溢出处理

**相关文件**：
- `packages/ai/src/utils/overflow.ts`
- `packages/ai/test/overflow.test.ts`
- `packages/ai/test/context-overflow.test.ts`

处理流程：
1. 检测 Token 使用量接近限制
2. 触发 compaction 压缩
3. 如还不够，删除最早的非关键消息
4. 最后手段：向用户报告上下文已满

### 3.4 文件操作容错

**相关文件**：
- `packages/coding-agent/src/core/tools/file-mutation-queue.ts`
- `packages/coding-agent/src/core/tools/edit.ts`
- `packages/coding-agent/src/core/tools/write.ts`
- `packages/coding-agent/test/file-mutation-queue.test.ts`

设计要点：
- **文件变更队列**：串行化文件操作，防止并发冲突
- **原子写入**：先写临时文件，再 rename 到目标文件
- **备份机制**：修改前自动备份原文件
- **冲突检测**：检测文件是否被外部修改

### 3.5 会话管理容错

**相关文件**：
- `packages/coding-agent/src/core/session-manager.ts`
- `packages/coding-agent/test/session-manager/`

特性：
- 多会话并发读写时加锁
- 自动保存进度
- 会话数据校验
- 损坏会话自动修复或重建

### 3.6 Provider 容错

**相关文件**：
- `packages/ai/src/providers/` 下的所有 Provider 实现

每个 Provider 都有独立的容错处理：
- **Anthropic**: SSE 流式解析容错
- **OpenAI**: 响应格式验证
- **Google**: 工具调用格式转换容错
- **Bedrock**: 端点解析和重试

### 3.7 测试层面的容错覆盖

**相关测试文件**：

| 测试文件 | 覆盖内容 |
|----------|----------|
| `agent-session-retry.test.ts` | 会话重试 |
| `agent-session-concurrent.test.ts` | 并发容错 |
| `agent-session-branching.test.ts` | 分支切换容错 |
| `agent-session-compaction.test.ts` | 压缩容错 |
| `bash-close-hang-windows.test.ts` | Bash 挂起处理 |
| `rpc-client-process-exit.test.ts` | RPC 进程退出容错 |
| `3317-network-connection-lost-retry.test.ts` | 网络断连容错 |
| `2791-fswatch-error-crash.test.ts` | 文件监听崩溃容错 |
| `2860-replaced-session-context.test.ts` | 会话上下文替换容错 |

---

## 四、整体架构图

```
┌─────────────────────────────────────────────────────────┐
│                  agent-harness.ts                        │
│                (Agent 运行引擎)                           │
├─────────────────────────────────────────────────────────┤
│                                                         │
│   ┌──────────────┐   ┌──────────────┐   ┌───────────┐  │
│   │  Session 管理  │   │   Hooks 系统  │   │ Compact   │  │
│   │  (持久化/恢复) │   │  (生命周期)   │   │ (压缩容错)  │  │
│   └──────────────┘   └──────────────┘   └───────────┘  │
│   ┌──────────────┐   ┌──────────────┐   ┌───────────┐  │
│   │   重试机制    │   │   上下文      │   │  环境抽象   │  │
│   │  (退避策略)   │   │  溢出处理     │   │ (Node.js)  │  │
│   └──────────────┘   └──────────────┘   └───────────┘  │
│   ┌──────────────┐   ┌──────────────┐   ┌───────────┐  │
│   │  Provider    │   │   文件操作    │   │ 网络容错   │  │
│   │  容错层      │   │  原子写入     │   │ 断线重连   │  │
│   └──────────────┘   └──────────────┘   └───────────┘  │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

---

## 五、总结

### 容错设计亮点

1. **分层容错**：从底层 Provider 到上层 Agent 引擎，每个层级都有独立的容错策略
2. **Hook 驱动**：通过 Hooks 系统将容错逻辑与核心业务解耦
3. **持久化与恢复**：Session 的 JSONL 存储保证数据完整性和可恢复性
4. **上下文管理**：Compaction 机制优雅处理上下文溢出问题
5. **全面的测试覆盖**：大量 test 和 suite 测试覆盖各种异常场景

### 异常处理模式

| 模式 | 应用场景 |
|------|----------|
| 重试 (Retry) | 网络超时、API 限流 |
| 降级 (Degrade) | Provider 不可用时切换到其他 Provider |
| 熔断 (Circuit Breaker) | 连续失败时暂停请求 |
| 回退 (Fallback) | 文件写入失败时使用备用路径 |
| 压缩 (Compaction) | 上下文过长时压缩历史 |
| 优雅终止 (Graceful Shutdown) | 通过 AbortSignal 实现 |

---

*文档生成日期: 2026-06-03*
*研究范围: pi-mono monorepo 的 Harness、容错与异常处理机制*
