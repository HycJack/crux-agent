# crux-ai

[![Go Version](https://img.shields.io/badge/Go-1.23+-blue.svg)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-v0.0.1-orange.svg)](core/types.go)

`crux-ai` is a thin Go abstraction layer over multiple LLM providers. One interface, one event stream, dozens of models — and the freedom to plug in your own providers.

The library originated as a migration of [pi-ai-go](https://github.com/) with selective adoption of ideas from [oh-my-pi](https://github.com/can1357/oh-my-pi) and `crux/crux/crux-ai`.

---

## ✨ Features

| | |
|---|---|
| 🔄 **Unified interface** | One `core.APIProvider` interface for every text provider. |
| 🧩 **OpenAI-protocol router** | One shared engine for OpenAI / DeepSeek / Kimi / Xiaomi / GLM — dispatched by `model.Provider`. |
| ⚡ **Streaming + events** | `EventStream[T,R]` with `Start` / `TextDelta` / `ThinkingDelta` / `ToolCallDelta` / `Done` / `Error` events. |
| 🛡️ **Typed errors** | Sentinel + typed pair (`ErrAuth` + `*AuthError`) for `errors.Is` / `errors.As`. |
| 🔁 **Auto retry** | Budgeted exponential backoff for transient 5xx / 429 / network errors. |
| 🧠 **Reasoning models** | First-class `ThinkingLevel` / `ThinkingBudgets` for Claude Thinking, DeepSeek-R1, o-series. |
| 🛠️ **9 internal tool packages** | `sse`, `jsonparse`, `validation`, `oauth`, `overflow`, `sanitize`, `conv`, `hash`, `diagnostics`. |
| 🪝 **Facadeless** | Use `ai/`, `core/`, or the unified `cruxai/` facade — your choice. |
| 🧪 **Mock provider** | `providers/faux` for deterministic tests. |

---

## 📦 Supported providers

| Provider | Implementation | API |
|----------|----------------|-----|
| **OpenAI** | `providers/openai` (native) | Chat Completions, Responses, Azure, Codex |
| **Anthropic** | `providers/anthropic` (native) | Messages |
| **Google** | `providers/google` (native) | Generative AI + Vertex |
| **AWS Bedrock** | `providers/bedrock` (native) | Converse streaming |
| **Mistral** | `providers/mistral` (native) | Conversations |
| **DeepSeek** | `providers/deepseek` (`compat`) | OpenAI protocol |
| **Kimi** | `providers/kimi` (`compat`) | OpenAI protocol |
| **GLM / Z.AI** | `providers/glm` (`compat`) | OpenAI protocol |
| **Xiaomi MiMo** | `providers/xiaomi` (`compat`) | OpenAI protocol |
| **OpenRouter** | `providers/images/openrouter.go` | Image generation |
| **Faux** | `providers/faux` | Mock, testing only |

---

## 🚀 Quick start

### Install

```bash
go get crux-ai
```

### Hello world

```go
package main

import (
    "context"
    "fmt"

    "crux-ai/ai"
    "crux-ai/core"

    // Triggers init() that registers every builtin provider.
    _ "crux-ai/providers"
)

func main() {
    ctx := context.Background()

    // 1. Look up a model.
    model, err := ai.GetModel(core.ProviderOpenAI, "gpt-4o")
    if err != nil {
        panic(err)
    }

    // 2. Build messages.
    msgs := []core.Message{
        core.UserMessage{
            Role:      core.MessageRoleUser,
            Content:   "Hello!",
            Timestamp: time.Now(),
        },
    }

    // 3. Call.
    stream, err := ai.Stream(ctx, model, msgs, core.StreamOptions{
        APIKey: "sk-...",
    })
    if err != nil {
        panic(err)
    }

    // 4. Wait for the final result.
    result, err := stream.Result()
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Content)
}
```

### Stream events incrementally

```go
stream, _ := ai.Stream(ctx, model, msgs, opts)
_, err := stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
    switch e := evt.(type) {
    case core.EventTextDelta:
        fmt.Print(e.Delta)
    case core.EventThinkingDelta:
        fmt.Printf("[thinking] %s", e.Delta)
    case core.EventToolCallStart:
        fmt.Printf("\n[tool: %s]\n", e.Name)
    }
    return nil
})
```

### Reasoning models

Use `StreamSimple` to control thinking depth across providers:

```go
opts := core.SimpleStreamOptions{
    StreamOptions: core.StreamOptions{APIKey: "..."},
    Reasoning:     core.ThinkingHigh,
    // Optional per-provider budgets:
    ThinkingBudgets: map[string]int{
        "anthropic-claude-3-7-sonnet": 8192,
    },
}
stream, _ := ai.StreamSimple(ctx, model, msgs, opts)
```

### Image generation

```go
img, _ := ai.GenerateImages(ctx, imageModel, []core.Message{
    core.UserMessage{Role: core.MessageRoleUser, Content: "a cat astronaut"},
}, core.ImageOptions{APIKey: "..."})
```

---

## 🏗️ Architecture

```
┌──────────────────────────────────────────────────┐
│  Application code                                │
└─────────────────────┬────────────────────────────┘
                      │
┌─────────────────────▼────────────────────────────┐
│  ai/  (Stream, Complete, GetModel)               │  ← public API
└─────────────────────┬────────────────────────────┘
                      │
┌─────────────────────▼────────────────────────────┐
│  core/  (types, errors, events, registry)        │  ← zero-dep types
└──────────┬─────────────────────┬─────────────────┘
           │                     │
┌──────────▼────────┐  ┌─────────▼────────────┐
│ providers/        │  │ internal/            │
│ - native impls    │  │ - sse, jsonparse,    │
│ - compat router   │  │   validation, oauth, │
│   (OpenAI proto)  │  │   overflow, ...      │
└───────────────────┘  └──────────────────────┘
```

See **[AGENTS.md](AGENTS.md)** for the full module-by-module breakdown, package conventions, and dead-code audit.

---

## 🧪 Test

```bash
go build ./...
go vet ./...
go test ./... -count=1
```

A CLI is provided for ad-hoc testing:

```bash
go run ./cmd/crux-ai providers          # list registered providers
go run ./cmd/crux-ai models             # list loaded models
go run ./cmd/crux-ai complete openai gpt-4o "Hello"
```

---

## ➕ Add a new provider

| Protocol | Steps |
|----------|-------|
| **OpenAI-compatible** | 1) write `providers/<name>/<name>.go` returning `compat.Config` (≈30 lines); 2) add `WithConfig(<name>.New())` to the router in `providers/register.go`. |
| **Custom protocol** | 1) implement `core.APIProvider` (`Stream` + `StreamSimple`); 2) add a new `core.KnownAPI` constant in `core/types.go`; 3) `RegisterProvider(...)` in `providers/register.go`. |

Then add model entries to `ai/models_generated.go` (or the source data generator).

---

## 🔧 Configuration

### API key resolution

Every provider resolves API keys in the same precedence:

1. `core.StreamOptions.APIKey` (per-call override).
2. Provider-specific env vars (see `core/env.go:providerEnvVars`).
3. Empty → request fails fast.

```go
// Programmatic
opts := core.StreamOptions{APIKey: "sk-..."}

// Or via env (auto-resolved)
export OPENAI_API_KEY=sk-...
```

### Timeout

```go
opts := core.StreamOptions{
    APIKey:    "sk-...",
    TimeoutMs: 60_000,  // HTTP client timeout
}
```

### Retry

Handled automatically inside the router for transient errors (`ErrRateLimit`, `ErrServer`, `ErrNetwork`). Aborts / context overflows / auth failures never retry.

---

## 📂 Project structure

```
crux-ai/
├── core/              # zero-dep types, errors, events, registry, retry
├── ai/                # public API (Stream, Complete, GetModel)
├── providers/         # 12 provider implementations + router registration
│   ├── compat/        # OpenAI-protocol shared engine
│   ├── openai/        # OpenAI native (4 APIs)
│   ├── anthropic/     # Claude Messages
│   ├── google/        # Gemini + Vertex
│   ├── bedrock/       # AWS Bedrock Converse
│   ├── mistral/       # Mistral Conversations
│   ├── deepseek/      # compat
│   ├── kimi/          # compat
│   ├── glm/           # compat
│   ├── xiaomi/        # compat
│   ├── faux/          # mock
│   ├── images/        # image-only
│   └── register.go    # init() registers every builtin
├── internal/          # non-exported helpers (sse, jsonparse, oauth, ...)
├── testenv/           # .env loading for tests
├── cmd/crux-ai.go     # CLI
└── cruxai.go          # facade (re-exports core + ai)
```

---

## 📝 Version

Current: `v0.0.1` — access via `cruxai.Version` (re-export of `core.Version`).

---

## 📄 License

MIT — see [LICENSE](LICENSE).

---

## 🙏 Credits

- [pi-ai-go](https://github.com/) — original architecture
- [oh-my-pi](https://github.com/can1357/oh-my-pi) — retry patterns, race iterator, idle timeout
- `crux/crux/crux-ai` — selected design ideas