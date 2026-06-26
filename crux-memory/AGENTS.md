# crux-memory

The persistent memory layer for AI agents. Implements the 4-layer progressive
memory hierarchy (L0 raw → L1 atomic → L2 scene → L3 persona) as a Go port of
`TencentCloud/TencentDB-Agent-Memory`.

## What "memory" means here

A long-running agent must remember:
1. **What the user just said** (L0: raw stream, never lossy)
2. **Facts and preferences the user has expressed** (L1: deduplicated atoms)
3. **Topics the user keeps coming back to** (L2: aggregated scenes)
4. **Who the user is, distilled** (L3: persona narrative profile)

Without this layer, every conversation restarts cold. The agent re-asks the
user's name, re-explains its capabilities, forgets yesterday's code conventions.

Crux-memory solves this with four progressive layers where each layer adds
abstraction but never loses the raw data underneath.

## Layering

This module is the **side-pillar** of Crux's stack — it touches only the
boundary with the agent runtime, not the core agent loop:

```
                    ┌─────────────────────┐
   crux-chat/ui ───►│  crux-harness       │
                    │  (turn FSM, hooks,  │
                    │   policy, dispatch) │
                    └──────────┬──────────┘
                               │ hooks.Event
                               ▼
                    ┌─────────────────────┐
                    │  crux-memory        │ ◄─── you are here
                    │  L0→L1→L2→L3       │
                    └──────────┬──────────┘
                               │ file JSONL/MD
                               ▼
                          ~/crux-memory/
                          l0/  l1/  l2/  l3/
```

`crux-memory` does **not** depend on `crux-agent-runtime` or `crux-harness`
for its core pipeline. Only the integration adapters (`hooks/`, `harness/`)
import Crux types.

The pure core (`l0`, `l1`, `l2`, `l3`, `llm`, `pipeline`) has **zero** Crux
dependencies and can be vendored standalone into any Go project.

## What's in here

| package     | purpose                                                                |
|-------------|------------------------------------------------------------------------|
| `l0`        | Append-only JSONL recorder for raw conversation messages                |
| `l1`        | Atomic memory extractor (LLM JSON-mode) + writer + token-overlap dedup |
| `l2`        | Scene aggregator with `META-START/END` Markdown format                |
| `l3`        | Persona.md generator using 4-layer deep-scan prompt                    |
| `llm`       | OpenAI-compatible chat client + JSON-mode + offline mock              |
| `pipeline`  | L0→L1→L2→L3 orchestrator with debounced triggers                       |
| `hooks`     | Adapter for `crux-agent-runtime` AgentEvent → L0 capture              |
| `harness`   | Adapter implementing `crux-harness/hooks.Consumer` (in-process entry) |
| `cmd/demo`  | End-to-end offline demo (mock LLM, no API key required)                |
| `cmd/memoryd` | Standalone HTTP daemon on `:8420` (cross-process entry)             |

## What is NOT in here

- LLM types and providers → `crux-ai`
- The agent loop → `crux-agent-runtime`
- Hooks, policy, dispatch, FSM → `crux-harness`
- HTTP front door → `crux-deployment/gateway`
- Storage backends (S3, Postgres, etc.) → out of scope; we are file-only

## Principles

- **Lossy extraction, lossless raw.** LLM stages may fail or hallucinate, but
  L0 is always written first. A partial extraction is recoverable from disk.
- **File format mirrors the reference.** L0/L1 JSONL, L2 `META-START/END`
  Markdown, L3 `persona.md`. Future TS↔Go migration is mechanical.
- **Async by default.** L1/L2/L3 LLM calls are off the agent hot path. A 5s
  extraction stall must never block a turn.
- **Three trigger knobs, no single global timer.** `MessagesPerTick`,
  `MinInterval`, `MaxInterval`. This is the only sane way to handle both
  bursty and idle sessions.
- **Zero business logic.** The pipeline does not decide *what* is worth
  remembering — the LLM does. We just orchestrate and persist.
- **Pluggable LLM, swappable for tests.** `llm.Client.SetMockFn` lets the
  entire pipeline run without an API key, which is how the demo and CI work.

## Integration patterns

### A. In-process (recommended for `cruxd`)

```go
import (
    "github.com/hycjack/crux-harness/hooks"
    cruxmem "github.com/crux-memory/crux-memory/harness"
    "github.com/crux-memory/crux-memory/pipeline"
    "github.com/crux-memory/crux-memory/llm"
)

p, _ := pipeline.New("/var/lib/crux-memory",
    llm.NewClient(os.Getenv("MODEL_BASE_URL"), os.Getenv("MODEL_API_KEY"), "gpt-4o-mini"),
    pipeline.DefaultConfig())
fanout := hooks.NewFanout()
fanout.Add(cruxmem.NewConsumer(p))
```

No changes to `cruxd/main.go`. The consumer is registered with the same
pattern as `AuditConsumer` / `BudgetConsumer` / `RedactConsumer`.

### B. Cross-process (memoryd)

```bash
memoryd --bind 127.0.0.1:8420 \
        --data /var/lib/crux-memory \
        --base-url $MODEL_BASE_URL \
        --api-key $MODEL_API_KEY
```

```bash
curl -X POST localhost:8420/v1/capture \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"s1","type":"user","role":"user","content":"..."}'

curl -X POST localhost:8420/v1/tick \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"s1"}'

curl localhost:8420/v1/state?session=s1
```

Useful when the producer is a separate process or language (e.g. an OpenClaw
Node.js plugin POSTing events).

## File layout

```
/var/lib/crux-memory/
├── <session-id>.jsonl        # L0: one message per line, append-only
├── l1/
│   └── memories.jsonl        # L1: deduped atomic memories
├── l2/
│   └── scenes/
│       ├── <scene-name>.md   # L2: META-START/END + body
│       └── ...
└── l3/
    └── persona.md            # L3: distilled profile
```

See `docs/modules/28-crux-memory.md` for design rationale + verification results.