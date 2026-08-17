# Crux — 模块化 AI Agent 框架

> 🌏 **语言 / Languages**: [English](./README.md) · [中文](./README.zh-CN.md)

Crux 是一个基于 Go 的多层 AI Agent 框架。仓库包含多个独立的 Go module：

| Module | 路径 | 职责 |
|---|---|---|
| **`crux-ai`** | `crux-ai/` | 与具体厂商无关的 AI 客户端——统一定义类型、流式接口，并提供 OpenAI / Anthropic / Google / Bedrock / Mistral / Azure 等适配器。 |
| **`crux-kernel`** | `crux-kernel/` | 核心运行时容器——插件生命周期、事件总线、服务注册表（Cordis 风格）。 |
| **`agent-engine`** | `agent-engine/` | 轻量级、可嵌入的 **Agent 循环引擎**，提供事件流、工具执行和管道抽象。 |
| **`crux-plugin`** | `crux-plugin/` | 子进程 JSON-RPC 2.0 插件框架（stdio）。 |
| **`crux-agent-chat`** | `crux-agent-chat/` | 一个开箱即用、跨平台的 REPL **编程助手**，建立在所有层之上。 |
| **`crux-agent-tui`** | `crux-agent-tui/` | TUI 终端界面，用于 Agent 交互。 |
| **`chat-app`** | `chat-app/` | Wails v2 + React 桌面聊天应用。 |
| **`crux-mcp`** | `crux-mcp/` | 模型上下文协议（MCP）客户端库。 |
| **`crux-memory`** | `crux-memory/` | 4 层长期记忆系统（热/温/冷/归档）。 |
| **`crux-turn`** | `crux-turn/` | 独立的 Turn FSM 库，用于单次 Agent 轮次。 |

依赖方向是**严格单向**的：

```
crux-agent-chat / chat-app / crux-agent-tui  →  agent-engine  →  crux-ai
                              crux-kernel  →  crux-ai
```

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

### 2. `crux-kernel` — 运行时容器

核心运行时容器，灵感来自 VS Code 的 ServiceCollection 和
Cordis 的 Container/Scope 架构。

- **`container`** — 服务集合，带生命周期钩子（`Start` / `Stop`）
- **`bus`** — 插件事件总线（`Register`、`Fire`、`Listen`、`Once`）
- **`service`** — 服务注册表（`Service`、`ServiceFactory` 接口）
- **`plugin`** — 插件生命周期（`PluginDef`、`PluginContext`、`PluginManager`）
- **`scope`** — 隔离的依赖注入作用域
- **`disposable`** — 资源清理模式

### 3. `agent-engine` — Agent 循环引擎

轻量级、可嵌入的 Agent 循环引擎（编译后仅 164KB）。

- **`core`** — 核心抽象（`AgentLoop`、`Pipeline`、`TurnContext`）
- **`engine`** — `DefaultEngine` 实现，带插件生命周期管理
- **`pipeline`** — 三层管道：SystemPrompt → History → Response
- **`events`** — 强类型事件系统（`EventTextDelta`、`EventToolCallEnd` 等）
- **`harness`** — 可选的 harness 服务（压缩、会话、可观测性等）
- **`memory`** — 4 层记忆集成（热/温/冷/归档）

### 4. `crux-plugin` — 子进程插件框架

基于 VS Code 插件架构的子进程 JSON-RPC 2.0 插件框架。

- **`transport`** — `stdio` 传输（`ReadPacket` / `WritePacket` / `PacketType`）
- **`protocol`** — JSON-RPC 2.0（`Request`、`Response`、`Notification`）
- **`lifecycle`** — 插件激活、停用和状态管理

### 5. `crux-agent-chat` — 可工作的编程 Agent

一个真正端到端的 REPL，复用了所有层：

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

### 6. 其他模块

- **`crux-agent-tui`** — TUI 终端界面，用于 Agent 交互
- **`chat-app`** — Wails v2 + React 桌面聊天应用
- **`crux-mcp`** — 模型上下文协议（MCP）客户端库
- **`crux-memory`** — 4 层长期记忆系统（热/温/冷/归档）
- **`crux-turn`** — 独立的 Turn FSM 库，用于单次 Agent 轮次

---

## 🚀 快速开始

### 环境要求

- **Go 1.25+**（go.work 指定 `go 1.25.0`）
- 一个 `.env` 文件（参见 [配置](#-配置)）
- 至少一个支持厂商的 API 密钥

### 一次性构建整个仓库

在仓库根目录：

```bash
go build ./...
```

每个 module 都用 `replace` 指令指向同级目录，**无需发布到模块
代理**就能直接构建。

### 跑 REPL

```bash
cd crux-agent-chat
cp .env.example .env
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

### 跑 agent-engine 演示

```bash
cd agent-engine
go run ./cmd/demo-agent
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
crux-agent/
├── crux-ai/                       # AI 客户端核心
│   ├── core/                      # 类型、环境变量、注册表
│   ├── ai/                        # 流式入口
│   ├── providers/<vendor>/        # 各厂商适配器
│   ├── testenv/                   # 沙箱测试辅助
│   └── cmd/                       # CLI 演示
│
├── crux-kernel/                   # 运行时容器
│   ├── container/                 # 服务集合
│   ├── bus/                       # 插件事件总线
│   ├── service/                   # 服务注册表
│   ├── plugin/                    # 插件生命周期
│   ├── scope/                     # 隔离的 DI 作用域
│   └── disposable/                # 资源清理
│
├── agent-engine/                  # Agent 循环引擎
│   ├── core/                      # 核心抽象
│   ├── engine/                    # DefaultEngine 实现
│   ├── pipeline/                  # 三层管道
│   ├── events/                    # 强类型事件系统
│   ├── harness/                   # 可选的 harness 服务
│   └── memory/                    # 4 层记忆集成
│
├── crux-plugin/                   # 子进程插件框架
│   ├── transport/                 # stdio 传输
│   ├── protocol/                  # JSON-RPC 2.0
│   └── lifecycle/                 # 插件生命周期
│
├── crux-agent-chat/               # 端到端 REPL 编程助手
│   ├── main.go                    # REPL 主循环
│   ├── agent/                     # Agent 工厂
│   ├── config/                    # .env 加载器
│   ├── tools/                     # bash、files、read_image
│   └── ui/                        # ANSI 渲染（含 Windows VT）
│
├── crux-agent-tui/                # TUI 终端界面
├── chat-app/                      # Wails v2 + React 桌面应用
├── crux-mcp/                      # MCP 客户端库
├── crux-memory/                   # 4 层长期记忆系统
└── crux-turn/                     # 独立的 Turn FSM 库
```

---

## 🧪 测试

| Module | 命令 |
|---|---|
| `crux-ai` | `go test ./...`（部分包有以 `//go:build integration` 守门的集成测试） |
| `crux-kernel` | `go test ./...` |
| `agent-engine` | `go test ./...` |
| `crux-plugin` | `go test ./...` |
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

**创建插件。** 使用 `crux-plugin` 构建子进程插件，
通过 stdio 的 JSON-RPC 2.0 进行通信。

**使用 agent-engine。** 该引擎设计为可嵌入。
导入 `agent-engine` 并使用 `engine.NewDefaultEngine()` 创建
带插件生命周期管理的完整 Agent。

---

## 📄 许可证

当前许可证见
[`crux-agent-chat/LICENSE`](./crux-agent-chat/LICENSE)。
