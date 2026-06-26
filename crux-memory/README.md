# crux-memory

4-layer long-term memory for AI agents, in Go. Go port of
[`TencentCloud/TencentDB-Agent-Memory`](https://github.com/TencentCloud/TencentDB-Agent-Memory).

```
L0  raw conversation       (JSONL, append-only)
L1  atomic memories        (LLM JSON-mode + dedup)
L2  scene aggregation      (Markdown + META blocks)
L3  persona.md             (4-layer deep scan)
```

See [`AGENTS.md`](./AGENTS.md) for module overview and design principles, and
[`../docs/modules/28-crux-memory.md`](../docs/modules/28-crux-memory.md) for
full design notes, verification results, and limitations.

## Quick start

### Offline demo (no API key required)

```bash
go run ./cmd/demo /tmp/crux-memory-demo

# Expected output:
# L0: 1946 B JSONL
# L1: 1838 B memories.jsonl (7 atomic memories, deduped)
# L2: pinyin-learning.md (425 B), user-profile.md (371 B)
# L3: persona.md (1502 B)
```

### Standalone HTTP daemon

```bash
go run ./cmd/memoryd --bind 127.0.0.1:8420 --data /tmp/memoryd-data

# In another shell:
curl -X POST localhost:8420/v1/capture \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"s1","type":"user","role":"user","content":"..."}'

curl -X POST localhost:8420/v1/tick \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"s1"}'

curl localhost:8420/v1/state?session=s1
```

Set `MODEL_API_KEY`, `MODEL_BASE_URL`, `MODEL_NAME` env vars to switch from
mock mode to a real LLM.

### Embed in your own code

```go
import (
    "github.com/crux-memory/crux-memory/pipeline"
    "github.com/crux-memory/crux-memory/llm"
)

p, _ := pipeline.New("/var/lib/crux-memory",
    llm.NewClient("https://api.openai.com/v1", os.Getenv("OPENAI_API_KEY"), "gpt-4o-mini"),
    pipeline.DefaultConfig())

p.Capture(ctx, "session-1", l0.RoleUser, "I prefer dark mode")
p.MaybeTick(ctx)
```

## Status

| Component | Status |
|---|---|
| L0 raw recorder | ✅ verified |
| L1 atomic extraction + dedup | ✅ verified (mock LLM) |
| L2 scene aggregation | ✅ verified (mock LLM) |
| L3 persona generation | ✅ verified (mock LLM) |
| Pipeline orchestration | ✅ verified |
| `harness.Consumer` adapter | ✅ compiles, integration not wired |
| `memoryd` daemon | ✅ verified end-to-end |
| Real LLM end-to-end | ⏳ needs API key (any OpenAI-compat works) |

## License

MIT (matching upstream `TencentDB-Agent-Memory`).