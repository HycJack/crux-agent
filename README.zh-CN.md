# Crux — 模块化 AI Agent 框架

> 🌏 **语言 / Languages**: [English](./README.md) · [中文](./README.zh-CN.md)

Crux 是一个基于 Go 的多层 AI Agent 框架。仓库被刻意拆分成四个
互相独立的小 Go module，让每一层都可以被单独采用（也可以被跳过）：

| Module | 路径 | 职责 |
|---|---|---|
| **`crux-ai`** | `crux-ai/` | 与具体厂商无关的 AI 客户端——统一定义类型、流式接口，并提供 OpenAI / Anthropic / Google / Bedrock / Mistral / Azure 等适配器。 |
| **`crux-agent-runtime`** | `crux-agent-runtime/` | 可复用的 **Agent 循环**，提供事件流和工具执行框架。 |
| **`crux-agent-harness`** | `crux-agent-harness/` | 可插拔的"代理管理"套件：上下文压缩、审批闸门、检查点、会话持久化、技能加载、可观测性、提示词构建。 |
| **`crux-agent-chat`** | `crux-agent-chat/` | 一个开箱即用、跨平台的 REPL **编程助手**，建立在前三层之上。 |

依赖方向是**严格单向**的：

```
crux-agent-chat  →  crux-agent-runtime  →  crux-ai
crux-agent-harness  ───────────────────→  crux-ai
```

`crux-agent-harness` 完全不知道 runtime 的存在，runtime 也完全不
知道 harness 的存在。每一层都可以独立替换或扩展，而不必动到其他
层。

---

## ✨ 特性

- **与厂商无关** — 一套统一的流式 API，同时支持 OpenAI、Anthropic、
  Google（Gemini / Vertex）、Amazon Bedrock、Mistral、Azure OpenAI、
  OpenAI Codex、Groq、xAI、DeepSeek、Cerebras、Cloudflare、
  Hugging Face、Moonshot、OpenRouter、Fireworks、Together 等。
- **原生支持推理（Reasoning）** — 内建 `ThinkingContent` 块、各
  模型推理强度映射。
- **多模态** — 文本、图片、音频、工具调用共享同一个
  `core.ContentBlock` 联合类型。
- **工具调用循环** — 流式 `AgentLoop`，支持中止、并发工具执行、
  结构化事件流。
- **Harness 基础设施** — 基于 token 的上下文压缩（LLM / 滑动窗口 /
  混合）、基于规则的审批闸门、撤销 / 重做检查点、JSONL 会话持久化、
  结构化日志、技能文件（`SKILL.md`）、提示词构造器。
- **REPL 编程 Agent** — 现成的终端助手，Windows / macOS / Linux
  都能跑，支持图片附件和 PowerShell 原生执行。

---

## 📦 Module 详解

### 1. `crux-ai` — AI 客户端

最底层。定义跨厂商的统一类型，并提供把它们变成真实 HTTP / SSE
请求的适配器。

- `crux-ai/core` — 与厂商无关的类型：
  `Model`、`Context`、`Message`、`ContentBlock`（text / thinking /
  image / tool call）、`UserMessage` / `AssistantMessage` /
  `ToolResultMessage`、`Usage`、`Cost`、`StreamOptions`，以及
  环境变量密钥解析器和 Provider 注册表。
- `crux-ai/ai` — 上层流式入口
  （`Complete`、`Stream`、`CompleteSimple`）。
- `crux-ai/providers/<vendor>` — 每个厂商一个 package。它们通过
  `providers.RegisterBuiltInProviders()`（在 `init()` 中调用）注册
  自己，对外由 `KnownAPI` 常量表示（`openai-completions`、
  `anthropic-messages`、`bedrock-converse-stream`、
  `openai-responses`、`azure-openai-responses`、
  `openai-codex-responses`、`google-generative`、`google-vertex`、
  `mistral-conversations`）。

最小用法：

```go
import (
    "context"
    "crux-ai/ai"
    "crux-ai/core"
    _ "crux-ai/providers" // 注册所有内置 provider
)

model := core.Model{
    ID:   "claude-sonnet-4-5",
    API:  core.APIAnthropicMessages,
    Provider: core.ProviderAnthropic,
    Input: []core.Modality{core.ModalityText, core.ModalityImage},
}

msg, err := ai.CompleteSimple(context.Background(), model,
    []core.Message{core.UserMessage{Content: "你好！"}},
    core.SimpleStreamOptions{StreamOptions: core.StreamOptions{
        APIKey:  "<你的密钥>",
        MaxTokens: ptr(1024),
    }},
)
```

`crux-ai/cmd` 目录下有一个小的 CLI 演示（`crux-ai.go`），
`crux-ai/testenv` 提供基于 `faux` 模拟 provider 的沙箱测试工具。

### 2. `crux-agent-runtime` — Agent 循环

一个独立、小巧的 `Agent` 类型，把 `crux-ai` 包装成事件驱动的循环：

- `agent.New(config, toolSpecs)` — 用一组工具构造一个 Agent。
- `agent.Run(ctx, userMessage)` — 跑一轮或多轮，直到模型
  自然停止或发出 `StopToolUse`。
- `agent.Subscribe(fn)` / `agent.SubscribeChan(ch)` — 监听
  类型化事件（`EventText`、`EventThinkingStart`、`EventToolCallStart`、
  `EventToolResult`、`EventDone`、`EventError` 等）。
- `agent.Abort()` — 协作式取消。

Runtime 自带一套工具规范（tool-spec）系统，因此可以接入任何自定义
工具而无需改动核心循环。

### 3. `crux-agent-harness` — 横切关注点

十一个小型子 package，解决"每个严肃的 Agent 迟早都会遇到的问题"。
所有 package 都**独立于 runtime**——只消费 `crux-ai` 的类型，
因此可以自由混搭：

| Package | 作用 |
|---|---|
| `token` | 基于 Tiktoken 的 token 计数，自带进程级 Counter 缓存池。 |
| `token/messages` | 根据 `core.Message` 序列估算请求大小（含图片、工具调用）。 |
| `context` | token 预算、状态检查、二分搜索找切分点。 |
| `context/compactor` | `Compactor` 接口 + LLM / 滑动窗口 / 混合三种实现。 |
| `context/pipeline` | `Pipeline.Check` → `ShouldCompact` → `Compact` 编排。 |
| `approval` | 基于规则的闸门（`DecisionAllow` / `Block` / `Ask`），支持自定义匹配器。 |
| `checkpoint` | 快照栈，支持撤销 / 重做。 |
| `session` + `session/jsonl` | JSONL 持久化的会话树（`Message`、`CustomMessage`、`BranchSummary`、`Compaction`、`ModelChange`、`ThinkingChange`、`SessionInfo`、`Label`）。 |
| `observe` | 结构化 JSON 行日志 + turn 计时器 + token 用量记录。 |
| `prompt` | 系统提示词构造器，自带 skills/templates 的 XML 区块。 |
| `skills` | `SKILL.md` 加载器（YAML frontmatter，支持 `disable-model-invocation`）。 |

> Harness 是**可选的**。Runtime 和 chat 不依赖它也能跑；按需
> 引入即可。

### 4. `crux-agent-chat` — 可工作的编程 Agent

一个真正端到端的 REPL，复用了下面三个 module：

- `main.go` — REPL 主循环，Ctrl+C 中止，命令解析
  （`/help`、`/clear`、`/tools`、`/paste`、`/clearimg`、`/quit`）。
- `agent/coding_agent.go` — 拼装系统提示词（工作目录、当前时间、
  操作系统 / 架构），挂载工具，运行 Agent。
- `config/config.go` — `.env` 加载器，带默认值与校验
  （`AI_MAX_TOKENS`、`AI_TEMPERATURE`）。
- `tools/` — 实际工具集：
  - `bash` — 跨平台 Shell（Windows 走 PowerShell，其他平台
    走 `bash`），支持流式输出。
  - `read_file`、`write_file`、`edit_file`、`list_files` —
    文件系统原语。
  - `read_image` — 把图片加载成多模态
    `core.ImageContent`（jpg / jpeg / png / gif / webp，
    最大 8 MiB）。
- `ui/terminal*.go` — ANSI 渲染，在 Windows 下通过
  `kernel32.dll` 启用 Virtual Terminal Processing。

---

## 🚀 快速开始

### 环境要求

- **Go 1.23+**（每个 module 都指定 `go 1.23.0`）
- 一个 `.env` 文件（参见 [配置](#-配置)）
- 至少一个支持厂商的 API 密钥

### 一次性构建整个仓库

在仓库根目录：

```powershell
go build ./...
```

每个 module 都用 `replace` 指令指向同级目录，**无需发布到模块
代理**就能直接构建。

### 跑 REPL

```powershell
cd crux-agent-chat
copy .env.example .env
# 编辑 .env，填入你的 API 密钥
go run .
```

REPL 中可以这样用：

```
👤 You: 当前目录下有哪些文件？
👤 You: /paste screenshot.png
📎 Staged 1 image(s)
👤 You: 📎 1 image(s) attached  这个报错窗口里是什么问题？
👤 You: /help            # 查看所有命令
👤 You: /quit            # 退出（按两次 Ctrl+C 也可以退出）
```

### 跑 Harness 测试

```powershell
cd crux-agent-harness
go test ./...
```

### 跑 Runtime 演示

```powershell
cd crux-agent-runtime
go run ./cmd
```

---

## ⚙️ 配置

`crux-agent-chat` 启动时会读 `.env` 文件。关键变量（完整列表见
`.env.example`）：

| 变量 | 作用 |
|---|---|
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `DEEPSEEK_API_KEY` / … | 厂商 API 密钥。Crux 会根据**设置了哪个变量**自动选择对应厂商。 |
| `AI_PROVIDER` | 强制使用某个 provider。 |
| `AI_MODEL` | 覆盖模型 id。 |
| `AI_BASE_URL` | 覆盖 API 基础地址（用于 OpenAI 兼容端点）。 |
| `AI_MAX_TOKENS` | 最大输出 token（必须 `> 0`）。 |
| `AI_TEMPERATURE` | 采样温度。 |
| `CRUX_SHELL` | 强制使用某个 shell（`pwsh`、`powershell`、`cmd`、`bash` 等）。 |

其他厂商（Google、Mistral、Azure、Bedrock…）准确的环境变量名请
参考 `crux-ai/core/env.go`。

---

## 🧱 项目结构

```
crux/
├── crux-ai/                       # AI 客户端核心
│   ├── core/                      # 类型、环境变量、注册表
│   ├── ai/                        # 流式入口
│   ├── providers/<vendor>/        # 各厂商适配器
│   ├── testenv/                   # 沙箱测试辅助
│   └── cmd/                       # CLI 演示
│
├── crux-agent-runtime/            # Agent 循环
│   ├── agent/                     # Agent、AgentLoop、事件类型
│   └── cmd/                       # 演示二进制
│
├── crux-agent-harness/            # 可选的 harness 套件
│   ├── token/                     # Tiktoken 计数（含缓存池）
│   ├── context/                   # 预算 + 流水线 + 压缩器
│   ├── approval/                  # 基于规则的审批闸门
│   ├── checkpoint/                # 撤销 / 重做快照
│   ├── session/                   # JSONL 会话树
│   ├── observe/                   # JSON 行日志
│   ├── prompt/                    # 系统提示词构造器
│   └── skills/                    # SKILL.md 加载器
│
└── crux-agent-chat/               # 端到端 REPL 编程助手
    ├── main.go                    # REPL 主循环
    ├── agent/                     # Agent 工厂
    ├── config/                    # .env 加载器
    ├── tools/                     # bash、files、read_image
    ├── ui/                        # ANSI 渲染（含 Windows VT）
    └── react-go-tutorial/         # 内置的示例 Web 应用
```

---

## 🧪 测试

| Module | 命令 |
|---|---|
| `crux-ai` | `go test ./...`（部分包有以 `//go:build integration` 守门的集成测试） |
| `crux-agent-runtime` | `go test ./...` |
| `crux-agent-harness` | `go test ./...` |
| `crux-agent-chat` | `go build ./...`（设计上不写测试） |

`crux-ai/providers/faux` 提供的 mock provider 可以用于跑集成测试，
不消耗真实 token。

---

## 🛠 扩展 Crux

**新增一个 Provider。** 实现 `core.Provider` 接口，通过
`core.RegisterProvider(core.KnownAPI("myapi"), myProvider, "...")`
注册，并在 `core/env.go` 中补一个 `KnownProvider` 常量和环境变量
映射。

**给 Chat Agent 加一个工具。** 在
`crux-agent-chat/tools/tools.go` 的 `AllTools()` 里追加一个
`ToolDef`，它会自动暴露给 LLM。

**采用 Harness。** Harness 故意做得松耦合——按需引入某些 package
（例如只要 `context.Pipeline` 做压缩），其余的可以忽略。

---

## 📄 许可证

当前许可证见
[`crux-agent-chat/LICENSE`](./crux-agent-chat/LICENSE)。内置的
`react-go-tutorial` 保留其各自许可证，仅作为示例项目引入。
