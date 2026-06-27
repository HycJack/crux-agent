# crux-ai vs pi-mono 代码库对比分析

> **分析范围**: crux-ai (Go) vs pi-mono (TypeScript)  
> **分析日期**: 2026-06-26  
> **分析目标**: 识别 crux-ai 中已实现但未被使用的模块，对照 pi-mono 的使用模式，制定激活计划

---

## 目录

1. [整体架构对比](#1-整体架构对比)
2. [模块级详细对比](#2-模块级详细对比)
3. [pi-mono 使用模式详解](#3-pi-mono-使用模式详解)
4. [crux-ai 激活优先级](#4-crux-ai-激活优先级)
5. [冗余清理清单](#5-冗余清理清单)
6. [附录：关键文件映射](#6-附录关键文件映射)

---

## 1. 整体架构对比

### pi-mono (TypeScript) — 三层架构

```
packages/ai/src/utils/             ← 纯函数库（底层）
    overflow.ts                   溢出检测
    retry.ts                      错误重试
    transform-messages.ts         消息转换
    sanitize.ts                   内容消毒
    validateTool.ts               工具参数校验
    json-parse.ts                 宽松 JSON 解析
    diagnostics.ts                诊断工具

packages/agent/src/              ← Agent 循环（中间层）
    agent-loop.ts                通用 Agent 循环

packages/coding-agent/src/       ← 业务逻辑集成层（上层）
    core/agent-session.ts         核心 Agent Session
    core/compaction/              上下文压缩
```

### crux-ai (Go) — 两层架构

```
core/                            ← 纯函数库 + 核心类型（底层）
    retry.go                      错误重试
    overflow.go                   溢出检测（精简版）
    compaction.go                 上下文压缩
    transform.go                  消息转换
    errors.go                     核心错误类型
    (类型定义)                     Message, ToolCall, AssistantMessage ...

internal/                        ← 内部工具包（底层）
    overflow/                     溢出检测（完整版，与 core/overflow 重复）
    validation/                   工具参数校验
    sanitize/                     内容消毒
    jsonparse/                    宽松 JSON 解析
    diagnostics/                  诊断工具
    sse/                          SSE 事件流
    credstore/                    凭据存储
    hash/                         哈希工具
    oauth/                        OAuth 支持
    conv/                         格式转换

providers/                       ← 各 AI 提供商实现（中间层）
    anthropic/claude.go
    google/gemini.go
    compat/compat.go             OpenAI 兼容 API 路由
    openai/...
    mistral/...
    bedrock/...
    ...

crux-agent-harness/              ← 业务集成层（上层）
    session/                      Session 管理器
    context/                      上下文管理
    ...

crux-agent-runtime/              ← Agent 运行时（上层）
    agent/agent-loop.go           Agent 循环
    session/                      会话持久化
    ...
```

## 2. 模块级详细对比

### 状态图例

| 标记 | 含义 |
|:---:|:------|
| ✅ | 已实现 + 已在对应层使用 |
| ⚠️ | 已实现 + 外部可调用但未被实际使用 |
| 🔴 | 已实现但未被任何调用者使用 |
| 🟡 | 重复实现 |
| 🔵 | 已实现但替代方案不同 |
| ❌ | 未实现 |

### 2.1 溢出检测 (Overflow)

| 方面 | pi-mono | crux-ai |
|:-----|:--------|:--------|
| 文件 | \packages/ai/src/utils/overflow.ts\ | \core/overflow.go\ + \internal/overflow/overflow.go\ |
| 特点 | 导出 \isContextOverflow()\，使用正则匹配 | 两个版本，core 版精简，internal 版完整 |
| 使用方 | \gent-session.ts\ → 触发 compaction | \crux-agent-runtime/agent/agent-loop.go\ — 已使用 \core.IsContextOverflow()\ |
| 状态 | — | ⚠️ **\internal/overflow\ 存在冗余**，core 版已被 agent-loop 使用 |

**结论**: core/overflow 功能已激活。\internal/overflow\ 与 \core/overflow.go\ 重复，需合并。

---

### 2.2 错误重试 (Retry)

| 方面 | pi-mono | crux-ai |
|:-----|:--------|:--------|
| 文件 | \packages/ai/src/utils/retry.ts\ | \core/retry.go\ |
| 特点 | 导出 \isRetryableAssistantError()\ 分类函数 + 指数退避 | 导出 \Retry()\ + \IsRetryableError()\ + \DefaultRetryConfig()\ + \isRetryableErrorHTTP()\ 完整重试工具 |
| 使用方 | \gent-session.ts\ 判断重试 + 底层 HTTP fetch 循环实现指数退避 | 🔴 **未被任何 provider 或 agent-loop 使用** |
| 状态 | — | 🔴 **完全未使用** |

---

### 2.3 上下文压缩 (Compaction)

| 方面 | pi-mono | crux-ai |
|:-----|:--------|:--------|
| 文件 | \packages/coding-agent/src/core/compaction/\ (多个文件) | \core/compaction.go\ |
| 特点 | LLM 驱动的上下文压缩，完整流程 | 提供 \PlanCompaction()\ + \BuildCompactedMessages()\ |
| 使用方 | \gent-session.ts\ 在 overflow 后调用 | \crux-agent-runtime/agent/agent-loop.go\ — 使用自定义 \Compactor interface\，**不是直接使用 \core/compaction.go\** |
| 状态 | — | 🔵 **agent-loop 自己实现 compaction**，不依赖 \core/compaction.go\ |

---

### 2.4 消息转换 (Transform)

| 方面 | pi-mono | crux-ai |
|:-----|:--------|:--------|
| 文件 | \packages/ai/src/utils/transform-messages.ts\ | \core/transform.go\ |
| 特点 | 图片降级、thinking 块处理、ToolCall ID 归一化、thoughtSignature 清除 | \TransformMessages()\ 处理 ToolCall ID 长度 + thinking 块 |
| 使用方 | \openai-responses.ts\ / \openai-completions.ts\ 在发请求前调用 | 🔴 **未被任何 provider 调用** |
| 状态 | — | 🔴 **完全未使用** |

---

### 2.5 内容消毒 (Sanitize)

| 方面 | pi-mono | crux-ai |
|:-----|:--------|:--------|
| 文件 | \packages/ai/src/utils/sanitize.ts\ | \internal/sanitize/sanitize.go\ |
| 特点 | 清理非法 unicode 代理对 | 清理非法 unicode 字符串 |
| 使用方 | \	ransform-messages.ts\ 中调用 | 🔴 **未被任何代码调用** |
| 状态 | — | 🔴 **完全未使用** |

---

### 2.6 工具参数校验 (Validation)

| 方面 | pi-mono | crux-ai |
|:-----|:--------|:--------|
| 文件 | \packages/ai/src/utils/validateTool.ts\ | \internal/validation/validation.go\ |
| 特点 | 校验工具参数类型、必须字段、值范围 | 校验工具参数 |
| 使用方 | \gent-loop.ts\ 的 \prepareToolCall()\ | 🔴 **未被任何代码调用** |
| 状态 | — | 🔴 **完全未使用** |

---

### 2.7 宽松 JSON 解析

| 方面 | pi-mono | crux-ai |
|:-----|:--------|:--------|
| 文件 | \packages/ai/src/utils/json-parse.ts\ | \internal/jsonparse/jsonparse.go\ |
| 特点 | 宽松 JSON 解析 | 宽松 JSON 解析 |
| 使用方 | 多处 | 🔴 **未被任何代码调用** |
| 状态 | — | 🔴 **完全未使用** |

---

### 2.8 诊断工具

| 方面 | pi-mono | crux-ai |
|:-----|:--------|:--------|
| 文件 | \packages/ai/src/utils/diagnostics.ts\ | \internal/diagnostics/diagnostics.go\ |
| 特点 | 记录请求诊断信息 | 错误诊断辅助 |
| 使用方 | \gent-session.ts\ 的 \createAssistantMessageDiagnostic()\ | 🔴 **未被任何代码调用** |
| 状态 | — | 🔴 **完全未使用** |

---

### 2.9 SSE (Server-Sent Events)

| 方面 | pi-mono | crux-ai |
|:-----|:--------|:--------|
| 文件 | 分散在各 provider 中 | \internal/sse/sse.go\ |
| 特点 | 每个 provider 自己解析 SSE | 统一 SSE 解析引擎 |
| 使用方 | — | ✅ **被 \compat/compat.go\ 使用** (provider 级别) |
| 状态 | — | ✅ **唯一被实际调用的 internal 工具** |

---

## 3. pi-mono 使用模式详解

### 3.1 Agent 执行流程 (pi-mono)

下面是 pi-mono 中 \gent-session.ts\ 的核心执行流程，标注了每个阶段用到的模块：

`
sendChat() 开始
  |
  ├─ isContextOverflow(previousError)  ← overflow.ts
  |     └─ 是 → 执行 compaction → 重试
  |
  ├─ isRetryableAssistantError(previousError)  ← retry.ts
  |     └─ 是 → 回退重试（指数退避）
  |
  ├─ transformMessages(messages)  ← transform-messages.ts
  |     |  图片降级、thinking 块处理、ToolCall ID 归一化
  |     └─ sanitizeUnicode()  ← sanitize.ts
  |
  ├─ validateToolArguments(toolCall)  ← validateTool.ts
  |
  ├─ 发送请求到 LLM (通过 openai-responses.ts 或 openai-completions.ts)
  |     └─ 底层 HTTP 请求有 retryWithExponentialBackoff 保护
  |
  ├─ 收到 EventDone(msg)
  |     └─ isContextOverflow(stopReason)  ← overflow.ts
  |           └─ 是 → 标记需要 compaction 后重试
  |
  └─ 返回 AssistantMessage（附带 diagnostics 信息）  ← diagnostics.ts
`

### 3.2 调用关系总结

| 函数 | 放入消息队列前 | 执行工具调用时 | 完成消息后 | HTTP 请求层 |
|:-----|:-------------:|:--------------:|:----------:|:----------:|
| \isContextOverflow\ | 检查上一轮错误 | — | 检查 stopReason | — |
| \isRetryableAssistantError\ | 检查上一轮错误 | — | — | — |
| \etryWithExponentialBackoff\ | — | — | — | 保护所有请求 |
| \	ransformMessages\ | ✅ 转换消息 | — | — | — |
| \sanitizeUnicode\ | 在 transform 中调用 | — | — | — |
| \alidateToolArguments\ | — | ✅ 执行前校验 | — | — |
| \diagnostics\ | — | — | ✅ 附加诊断 | — |

---

## 4. crux-ai 激活优先级

基于以上分析，以下是按优先级排序的实施计划。

### 🔴 P0 — 立即激活（影响面最大）

#### 4.1 在 provider HTTP 请求层接入 \core.Retry()\

**目标文件**:
| 文件 | 改动 |
|:-----|:-----|
| \crux-ai/providers/compat/compat.go\ — \doRequest()\ | 用 \core.Retry()\ 包装 HTTP 请求 |
| \crux-ai/providers/anthropic/claude.go\ — \unStream()\ | 对初始请求加重试 |
| \crux-ai/providers/google/gemini.go\ — \doRequest()\ | 对 Google API 请求加重试 |
| \crux-ai/providers/mistral/mistral.go\ | 对 Mistral 请求加重试 |
| \crux-ai/providers/bedrock/bedrock.go\ | 对 Bedrock 请求加重试 |

**改动模式** (以 compat.go 为例):

`go
// 当前：
req, err := http.NewRequestWithContext(ctx, "POST", url, body)
if err != nil { return nil, err }
resp, err := http.DefaultClient.Do(req)
if err != nil { return nil, err }

// 改为：
var resp *http.Response
err := core.Retry(ctx, core.DefaultRetryConfig(), func() error {
    req, err := http.NewRequestWithContext(ctx, "POST", url, body)
    if err != nil { return err }
    resp, err = http.DefaultClient.Do(req)
    if err != nil { return err }
    if resp.StatusCode >= 400 {
        bodyBytes, _ := io.ReadAll(resp.Body)
        resp.Body.Close()
        return &HTTPError{StatusCode: resp.StatusCode, Body: string(bodyBytes)}
    }
    return nil
})
`

**为什么要加在 provider HTTP 层**:
- 保护所有底层 API 调用，自动重试瞬态错误
- 与 pi-mono 的 \openai-codex-responses.ts\ 中的 fetch 循环重试保持一致
- 不改变任何业务逻辑，只增加健壮性

---

### 🟡 P1 — 消除冗余

#### 4.2 合并 \internal/overflow\ → \core/overflow.go\

\internal/overflow\ 有 \ClassifyHTTPError()\ 函数和额外的溢出模式。应该：
1. 将 \ClassifyHTTPError\ 合并到 \core/overflow.go\
2. 将额外的溢出正则模式合并到 \core/overflow/overflowPatterns\
3. 删除 \internal/overflow\ 包

---

### 🟠 P2 — 激活更佳体验

#### 4.3 在 provider 层接入 \core.TransformMessages()\

**目标文件**: \crux-ai/providers/compat/compat.go\ — 在 \uildBody()\ 或 \unStream()\ 入口处调用

**效果**: 自动处理 ToolCall 超长 ID 截断、thinking 块转换，无需每个 provider 单独实现

#### 4.4 在 agent-loop 层面接入 \core.Retry()\ 处理非溢出重试

**目标文件**: \crux-agent-runtime/agent/agent-loop.go\

当前 agent-loop 只重试 overflow 错误（通过 \etryWithCompaction\），但不处理瞬态 API 错误（429 Too Many Requests, 500 Server Error 等）。可以用 \core.Retry()\ 包装 LLM 调用。

---

### 🔵 P3 — 激活可选增强

| 模块 | 激活方式 | 复杂度 |
|:-----|:--------|:------:|
| \internal/validation\ | 在 \crux-agent-runtime/agent/\ 的 tool 执行前调用 | 低 |
| \internal/sanitize\ | 在 \core/transform.go\ 的 \TransformMessages()\ 中调用 | 低 |
| \internal/jsonparse\ | 在 provider 解析流式 JSON 时使用 | 中 |
| \internal/diagnostics\ | 可选集成到 agent-loop 的事件系统 | 中 |

---

## 5. 冗余清理清单

以下是当前项目中存在的冗余和重复代码。

### 5.1 \internal/overflow\ 与 \core/overflow.go\

| 对比项 | \core/overflow.go\ | \internal/overflow/overflow.go\ |
|:-------|:------------------:|:-------------------------------:|
| 正则模式数 | 约 22 个 | 更多（含额外模式） |
| 导出函数 | \IsContextOverflow()\ | \IsContextOverflow()\ + \ClassifyHTTPError()\ |
| 使用方 | \gent-loop.go\ | 无 |
| 退出策略 | 保留 | 合并后删除 |

**建议**: 将 \ClassifyHTTPError\ 和新模式从 \internal/overflow\ 提取到 \core/overflow.go\，删除 \internal/overflow\。

### 5.2 Provider SSE 解析重复

\providers/compat/compat.go\ 使用 \internal/sse\，\providers/anthropic/claude.go\ 有自己的 SSE 解析逻辑。如果可能应该统一。

---

## 6. 附录：关键文件映射

### 文件映射表 pi-mono → crux-ai

| pi-mono (TypeScript) | crux-ai (Go) | 激活状态 |
|:---------------------|:-------------|:--------:|
| \overflow.ts\ | \core/overflow.go\ | ✅ 已由 agent-loop 使用 |
| \overflow.ts\ (完整版) | \internal/overflow/overflow.go\ | 🔴 冗余，未使用 |
| \etry.ts\ | \core/retry.go\ | 🔴 完全未使用 |
| \	ransform-messages.ts\ | \core/transform.go\ | 🔴 完全未使用 |
| \sanitize.ts\ | \internal/sanitize/sanitize.go\ | 🔴 完全未使用 |
| \alidateTool.ts\ | \internal/validation/validation.go\ | 🔴 完全未使用 |
| \json-parse.ts\ | \internal/jsonparse/jsonparse.go\ | 🔴 完全未使用 |
| \diagnostics.ts\ | \internal/diagnostics/diagnostics.go\ | 🔴 完全未使用 |
| (无) | \internal/sse/sse.go\ | ✅ 已被 compat 使用 |
| \gent-session.ts\ | \crux-agent-runtime/agent/agent-loop.go\ | ✅ 核心 Agent 循环 |
| \compaction/*.ts\ | \core/compaction.go\ | 🔵 agent-loop 使用自定义 Compactor |

### 使用情况汇总

`
模块                   状态         使用方
───────────────────────────────────────────────
core/overflow.go       ✅ 活跃      agent-loop.go
core/retry.go          🔴 未使用    无
core/transform.go      🔴 未使用    无
core/compaction.go     🔵 替代实现  agent-loop 用自己 Compactor
internal/sse           ✅ 活跃      compat.go
internal/overflow      🔴 冗余      core/overflow 的重复版
internal/validation    🔴 未使用    无
internal/sanitize      🔴 未使用    无
internal/jsonparse     🔴 未使用    无
internal/diagnostics   🔴 未使用    无
`

---

## 总结

本分析揭示了 crux-ai 代码库中的一个关键模式：**core 层和 internal 层的工具函数库已经相当完善，但大部分没有被实际接入**。

这与 pi-mono 形成了鲜明对比——在 pi-mono 中，\gent-session.ts\ 清晰地串联了所有功能模块（overflow、retry、transform、validation、diagnostics），形成了一个完整的 Agent 执行管道。

**最优先的行动**:
1. 在 provider HTTP 层加 \core.Retry()\ — 保护所有外部 API 调用
2. 合并 \internal/overflow\ 到 \core/overflow.go\ — 消除冗余
3. 在各 provider 请求前调用 \core.TransformMessages()\ — 自动消息处理

这三个改动不需要修改任何业务逻辑，也不需要改变 Agent 循环的行为，就能让现有的工具函数库全部运转起来。
