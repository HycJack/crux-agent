# Crux — A Modular AI Agent Framework

> 🌏 **语言 / Languages**: [English](./README.md) · [中文](./README.zh-CN.md)

Crux is a Go-based, multi-layered framework for building AI agents and
agent-powered applications. The repository is organized into several
independent Go modules:

| Module | Path | Purpose |
|---|---|---|
| **`crux-ai`** | `crux-ai/` | Provider-agnostic AI client — types, streaming, and adapters for OpenAI / Anthropic / Google / Bedrock / Mistral / Azure / etc. |
| **`crux-kernel`** | `crux-kernel/` | Core runtime container — plugin lifecycle, event bus, service registry (Cordis-inspired). |
| **`agent-engine`** | `agent-engine/` | Lightweight, embeddable **agent loop** with event streams, tool execution, and pipeline abstraction. |
| **`crux-plugin`** | `crux-plugin/` | Subprocess JSON-RPC 2.0 plugin framework (stdio). |
| **`crux-agent-chat`** | `crux-agent-chat/` | A working cross-platform REPL **coding agent** built on top of everything else. |
| **`crux-agent-tui`** | `crux-agent-tui/` | TUI terminal interface for agent interaction. |
| **`chat-app`** | `chat-app/` | Wails v2 + React desktop chat application. |
| **`crux-mcp`** | `crux-mcp/` | Model Context Protocol (MCP) client library. |
| **`crux-memory`** | `crux-memory/` | 4-layer long-term memory for AI agents. |
| **`crux-turn`** | `crux-turn/` | Standalone Turn FSM library for single agent turns. |

The dependency direction is strictly one-way:

```
crux-agent-chat / chat-app / crux-agent-tui  →  agent-engine  →  crux-ai
                              crux-kernel  →  crux-ai
```

---

## ✨ Features

- **Provider-agnostic** — One unified streaming API for OpenAI,
  Anthropic, Google (Gemini / Vertex), Amazon Bedrock, Mistral, Azure
  OpenAI, OpenAI Codex, Groq, xAI, DeepSeek, Cerebras, Cloudflare,
  Hugging Face, Moonshot, OpenRouter, Fireworks, Together, and more.
- **Reasoning-aware** — Native handling of `ThinkingContent` blocks
  and per-model reasoning-effort mapping.
- **Multimodal** — Text, images, audio, and tool calls share a single
  `core.ContentBlock` union.
- **Tool-use loop** — Streaming `AgentLoop` with abort support,
  concurrent tool execution, and structured events.
- **Harness plumbing** — Token-aware context compaction (LLM /
  sliding-window / hybrid), rule-based approval gates, undo-redo
  checkpoints, JSONL session persistence, structured logging, skills
  (SKILL.md), and a prompt builder.
- **REPL coding agent** — A ready-to-run terminal assistant that
  works on Windows, macOS, and Linux, including image attachments
  and PowerShell-native shell execution.

---

## 📦 Module Tour

### 1. `crux-ai` — the AI client

The bottom layer. Defines the cross-provider vocabulary and ships
the adapters that turn it into real HTTP / SSE traffic.

- `crux-ai/core` — Provider-agnostic types:
  `Model`, `Context`, `Message`, `ContentBlock` (text / thinking /
  image / tool call), `UserMessage` / `AssistantMessage` /
  `ToolResultMessage`, `Usage`, `Cost`, `StreamOptions`, plus an
  env-key resolver and a provider registry.
- `crux-ai/ai` — The high-level streaming entry points
  (`Complete`, `Stream`, `CompleteSimple`).
- `crux-ai/providers/<vendor>` — One package per vendor. Each
  registers itself via `providers.RegisterBuiltInProviders()`
  (called from `init()`) and lives behind a `KnownAPI` constant
  (`openai-completions`, `anthropic-messages`, `bedrock-converse-stream`,
  `openai-responses`, `azure-openai-responses`, `openai-codex-responses`,
  `google-generative`, `google-vertex`, `mistral-conversations`).

### 2. `crux-kernel` — runtime container

Core runtime container inspired by VS Code's ServiceCollection and
Cordis's Container/Scope architecture.

- **`container`** — Service collection with lifecycle hooks (`Start` / `Stop`)
- **`bus`** — Plugin event bus (`Register`, `Fire`, `Listen`, `Once`)
- **`service`** — Service registry (`Service`, `ServiceFactory` interfaces)
- **`plugin`** — Plugin lifecycle (`PluginDef`, `PluginContext`, `PluginManager`)
- **`scope`** — Isolated dependency injection scopes
- **`disposable`** — Resource cleanup patterns

### 3. `agent-engine` — agent loop engine

Lightweight, embeddable agent loop engine (164KB compiled binary).

- **`core`** — Core abstractions (`AgentLoop`, `Pipeline`, `TurnContext`)
- **`engine`** — `DefaultEngine` implementation with plugin lifecycle
- **`pipeline`** — Three-layer pipeline: SystemPrompt → History → Response
- **`events`** — Strongly-typed event system (`EventTextDelta`, `EventToolCallEnd`, etc.)
- **`harness`** — Optional harness services (compaction, session, observability, etc.)
- **`memory`** — 4-layer memory integration (Hot/Warm/Cold/Archive)

### 4. `crux-plugin` — subprocess plugin framework

Subprocess JSON-RPC 2.0 plugin framework based on VS Code's plugin architecture.

- **`transport`** — `stdio` transport (`ReadPacket` / `WritePacket` / `PacketType`)
- **`protocol`** — JSON-RPC 2.0 (`Request`, `Response`, `Notification`)
- **`lifecycle`** — Plugin activation, deactivation, and status management

### 5. `crux-agent-chat` — a working coding agent

A real, end-to-end REPL that uses all layers:

- `main.go` — REPL loop, Ctrl+C abort, command parser
  (`/help`, `/clear`, `/tools`, `/paste`, `/clearimg`, `/quit`).
- `agent/coding_agent.go` — Builds the system prompt (working
  directory, current time, OS/arch), wires tools, and runs the
  agent.
- `config/config.go` — `.env` loader with sane defaults and
  validation (`AI_MAX_TOKENS`, `AI_TEMPERATURE`).
- `tools/` — The actual toolset:
  - `bash` — cross-platform shell (PowerShell on Windows,
    `bash` elsewhere) with streaming output.
  - `read_file`, `write_file`, `edit_file`, `list_files` —
    file-system primitives.
  - `read_image` — load an image as a multimodal
    `core.ImageContent` (jpg/jpeg/png/gif/webp, ≤ 8 MiB).
- `ui/terminal*.go` — ANSI rendering with a `kernel32.dll` call
  to enable Virtual Terminal Processing on Windows.

### 6. Other modules

- **`crux-agent-tui`** — TUI terminal interface for agent interaction
- **`chat-app`** — Wails v2 + React desktop chat application
- **`crux-mcp`** — Model Context Protocol (MCP) client library
- **`crux-memory`** — 4-layer long-term memory for AI agents (Hot/Warm/Cold/Archive)
- **`crux-turn`** — Standalone Turn FSM library for single agent turns

---

## 🚀 Quick Start

### Prerequisites

- **Go 1.25+** (go.work targets `go 1.25.0`)
- A `.env` file (see [Configuration](#-configuration))
- An API key for at least one supported provider

### Build everything

From the repo root:

```bash
go build ./...
```

Each module uses a `replace` directive that points at its sibling,
so the build is self-contained — no module publishing required.

### Run the chat REPL

```bash
cd crux-agent-chat
cp .env.example .env
# edit .env and set your API key
go run .
```

Inside the REPL:

```
👤 You: what files are in this directory?
👤 You: /paste screenshot.png
📎 Staged 1 image(s)
👤 You: 📎 1 image(s) attached  what's wrong with this error dialog?
👤 You: /help            # show all commands
👤 You: /quit            # exit (Ctrl+C twice also exits)
```

### Run the agent-engine demo

```bash
cd agent-engine
go run ./cmd/demo-agent
```

---

## ⚙️ Configuration

`crux-agent-chat` reads a `.env` file at startup. The most important
keys (see `.env.example` for the full list):

| Variable | Purpose |
|---|---|
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `DEEPSEEK_API_KEY` / … | Provider API key. Crux auto-detects which provider to use based on which key is set. |
| `AI_PROVIDER` | Force a specific provider. |
| `AI_MODEL` | Override the model id. |
| `AI_BASE_URL` | Override the API base URL (for OpenAI-compatible endpoints). |
| `AI_MAX_TOKENS` | Max output tokens (must be `> 0`). |
| `AI_TEMPERATURE` | Sampling temperature. |
| `CRUX_SHELL` | Force a specific shell (`pwsh`, `powershell`, `cmd`, `bash`, …). |

For other providers (Google, Mistral, Azure, Bedrock, …) consult
`crux-ai/core/env.go` for the exact variable name.

---

## 🧱 Project Layout

```
crux-agent/
├── crux-ai/                       # Core AI client
│   ├── core/                      # Types, env, registry
│   ├── ai/                        # Streaming entry points
│   ├── providers/<vendor>/        # Provider adapters
│   ├── testenv/                   # Hermetic test helpers
│   └── cmd/                       # CLI demo
│
├── crux-kernel/                   # Runtime container
│   ├── container/                 # Service collection
│   ├── bus/                       # Plugin event bus
│   ├── service/                   # Service registry
│   ├── plugin/                    # Plugin lifecycle
│   ├── scope/                     # Isolated DI scopes
│   └── disposable/                # Resource cleanup
│
├── agent-engine/                  # Agent loop engine
│   ├── core/                      # Core abstractions
│   ├── engine/                    # DefaultEngine implementation
│   ├── pipeline/                  # Three-layer pipeline
│   ├── events/                    # Strongly-typed event system
│   ├── harness/                   # Optional harness services
│   └── memory/                    # 4-layer memory integration
│
├── crux-plugin/                   # Subprocess plugin framework
│   ├── transport/                 # stdio transport
│   ├── protocol/                  # JSON-RPC 2.0
│   └── lifecycle/                 # Plugin lifecycle
│
├── crux-agent-chat/               # End-to-end REPL coding agent
│   ├── main.go                    # REPL loop
│   ├── agent/                     # Agent factory
│   ├── config/                    # .env loader
│   ├── tools/                     # Bash, files, read_image
│   └── ui/                        # ANSI rendering (+ Windows VT)
│
├── crux-agent-tui/                # TUI terminal interface
├── chat-app/                      # Wails v2 + React desktop app
├── crux-mcp/                      # MCP client library
├── crux-memory/                   # 4-layer long-term memory
└── crux-turn/                     # Standalone Turn FSM library
```

---

## 🧪 Testing

| Module | Command |
|---|---|
| `crux-ai` | `go test ./...` (some packages have an integration test guarded by `//go:build integration`) |
| `crux-kernel` | `go test ./...` |
| `agent-engine` | `go test ./...` |
| `crux-plugin` | `go test ./...` |
| `crux-agent-chat` | `go build ./...` (no tests by design) |

The `faux` provider in `crux-ai/providers/faux` is a stub useful for
running integration tests without burning real tokens.

---

## 🛠 Extending Crux

**Add a new provider.** Implement the
`core.Provider` interface, register it via
`core.RegisterProvider(core.KnownAPI("myapi"), myProvider, "...")`,
and add a `KnownProvider` constant + env-var mapping in `core/env.go`.

**Add a tool to the chat agent.** Append a `ToolDef` in
`crux-agent-chat/tools/tools.go`'s `AllTools()` and it will be
exposed to the LLM automatically.

**Create a plugin.** Use `crux-plugin` to build a subprocess plugin
that communicates via JSON-RPC 2.0 over stdio.

**Use the agent-engine.** The engine is designed to be embedded.
Import `agent-engine` and use `engine.NewDefaultEngine()` to create
a fully functional agent with plugin lifecycle management.

---

## 📄 License

See [`crux-agent-chat/LICENSE`](./crux-agent-chat/LICENSE) for the
current license terms.
