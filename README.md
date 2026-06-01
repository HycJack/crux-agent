# Crux — A Modular AI Agent Framework

> 🌏 **语言 / Languages**: [English](./README.md) · [中文](./README.zh-CN.md)

Crux is a Go-based, multi-layered framework for building AI agents and
agent-powered applications. The repository is intentionally split into
four small Go modules so that each layer can be adopted (or skipped)
independently:

| Module | Path | Purpose |
|---|---|---|
| **`crux-ai`** | `crux-ai/` | Provider-agnostic AI client — types, streaming, and adapters for OpenAI / Anthropic / Google / Bedrock / Mistral / Azure / etc. |
| **`crux-agent-runtime`** | `crux-agent-runtime/` | A reusable **agent loop** with event streams and a tool-execution framework. |
| **`crux-agent-harness`** | `crux-agent-harness/` | Pluggable harness concerns: context compaction, approval gates, checkpoints, session persistence, skills, observability, prompts. |
| **`crux-agent-chat`** | `crux-agent-chat/` | A working cross-platform REPL **coding agent** built on top of everything else. |

The dependency direction is strictly one-way:

```
crux-agent-chat  →  crux-agent-runtime  →  crux-ai
crux-agent-harness  ───────────────────→  crux-ai
```

`crux-agent-harness` knows nothing about the runtime; the runtime knows
nothing about the harness. Each layer can be replaced or extended
without touching the others.

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

Minimal usage:

```go
import (
    "context"
    "crux-ai/ai"
    "crux-ai/core"
    _ "crux-ai/providers" // register all built-in providers
)

model := core.Model{
    ID:   "claude-sonnet-4-5",
    API:  core.APIAnthropicMessages,
    Provider: core.ProviderAnthropic,
    Input: []core.Modality{core.ModalityText, core.ModalityImage},
}

msg, err := ai.CompleteSimple(context.Background(), model,
    []core.Message{core.UserMessage{Content: "Hello!"}},
    core.SimpleStreamOptions{StreamOptions: core.StreamOptions{
        APIKey:  "<your key>",
        MaxTokens: ptr(1024),
    }},
)
```

The `crux-ai/cmd` directory contains a small CLI demo
(`crux-ai.go`) and `crux-ai/testenv` provides hermetic test
harnesses with `faux` (mock) providers.

### 2. `crux-agent-runtime` — the agent loop

A small, self-contained `Agent` type that wraps `crux-ai` in an
event-driven loop:

- `agent.New(config, toolSpecs)` — construct an agent with tools.
- `agent.Run(ctx, userMessage)` — run one or more turns until
  the model stops or signals `StopToolUse`.
- `agent.Subscribe(fn)` / `agent.SubscribeChan(ch)` — listen to
  typed events (`EventText`, `EventThinkingStart`, `EventToolCallStart`,
  `EventToolResult`, `EventDone`, `EventError`, …).
- `agent.Abort()` — cooperative cancellation.

The runtime ships its own tool-spec system, so you can plug in any
custom tool without touching the core loop.

### 3. `crux-agent-harness` — cross-cutting concerns

Eleven small sub-packages that solve the "stuff every serious agent
eventually needs" problem. All packages are **independent of the
runtime** — they consume only `crux-ai` types, so you can mix-and-match:

| Package | What it does |
|---|---|
| `token` | Tiktoken-backed token counting with a process-wide counter pool. |
| `token/messages` | Estimates request size from `core.Message` slices (incl. images, tool calls). |
| `context` | Token budget, status checks, and binary-search split-point planning. |
| `context/compactor` | `Compactor` interface + LLM / sliding-window / hybrid implementations. |
| `context/pipeline` | `Pipeline.Check` → `ShouldCompact` → `Compact` orchestration. |
| `approval` | Rule-based gate (`DecisionAllow` / `Block` / `Ask`) with custom matchers. |
| `checkpoint` | Snapshot stack with undo/redo. |
| `session` + `session/jsonl` | JSONL-persisted session tree (`Message`, `CustomMessage`, `BranchSummary`, `Compaction`, `ModelChange`, `ThinkingChange`, `SessionInfo`, `Label`). |
| `observe` | Structured JSON-line logger + turn timer + token usage recorder. |
| `prompt` | System-prompt builder with skills/templates XML sections. |
| `skills` | `SKILL.md` loader (YAML frontmatter, `disable-model-invocation` support). |

> The harness is **optional**. The runtime and chat work fine
> without it; you adopt the parts you need.

### 4. `crux-agent-chat` — a working coding agent

A real, end-to-end REPL that uses all three lower layers:

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

---

## 🚀 Quick Start

### Prerequisites

- **Go 1.23+** (every module targets `go 1.23.0`)
- A `.env` file (see [Configuration](#-configuration))
- An API key for at least one supported provider

### Build everything

From the repo root:

```powershell
go build ./...
```

Each module uses a `replace` directive that points at its sibling,
so the build is self-contained — no module publishing required.

### Run the chat REPL

```powershell
cd crux-agent-chat
copy .env.example .env
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

### Run the harness tests

```powershell
cd crux-agent-harness
go test ./...
```

### Try the demo binary

```powershell
cd crux-agent-runtime
go run ./cmd
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
crux/
├── crux-ai/                       # Core AI client
│   ├── core/                      # Types, env, registry
│   ├── ai/                        # Streaming entry points
│   ├── providers/<vendor>/        # Provider adapters
│   ├── testenv/                   # Hermetic test helpers
│   └── cmd/                       # CLI demo
│
├── crux-agent-runtime/            # Agent loop
│   ├── agent/                     # Agent, AgentLoop, event types
│   └── cmd/                       # Demo binary
│
├── crux-agent-harness/            # Optional harness concerns
│   ├── token/                     # Tiktoken counting (+ pool)
│   ├── context/                   # Budget + pipeline + compactor
│   ├── approval/                  # Rule-based gates
│   ├── checkpoint/                # Undo/redo snapshots
│   ├── session/                   # JSONL session tree
│   ├── observe/                   # JSON-line logger
│   ├── prompt/                    # System-prompt builder
│   └── skills/                    # SKILL.md loader
│
└── crux-agent-chat/               # End-to-end REPL coding agent
    ├── main.go                    # REPL loop
    ├── agent/                     # Agent factory
    ├── config/                    # .env loader
    ├── tools/                     # Bash, files, read_image
    ├── ui/                        # ANSI rendering (+ Windows VT)
    └── react-go-tutorial/         # Bundled example web app
```

---

## 🧪 Testing

| Module | Command |
|---|---|
| `crux-ai` | `go test ./...` (some packages have an integration test guarded by `//go:build integration`) |
| `crux-agent-runtime` | `go test ./...` |
| `crux-agent-harness` | `go test ./...` |
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

**Adopt the harness.** The harness is intentionally
loose-coupled — pick the packages you need (e.g. only
`context.Pipeline` for compaction) and ignore the rest.

---

## 📄 License

See [`crux-agent-chat/LICENSE`](./crux-agent-chat/LICENSE) for the
current license terms. The bundled `react-go-tutorial` retains its
own license and is included only as a demo project.
