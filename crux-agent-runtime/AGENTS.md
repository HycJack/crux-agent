# crux-agent-runtime

The agent execution layer. The thing that runs the autonomous LLM loop, manages conversations, and remembers.

## What "agent runtime" means

An **agent runtime** is the engine that drives an LLM through a multi-turn conversation with tool calls. It:
- runs the LLM → tool → LLM loop (the agent loop)
- manages conversation history (session)
- tracks token usage and compacts when needed (context)
- remembers facts across sessions (memory)
- learns from conversations automatically (autolearn)

## Layering

This module is the **bottom** of Crux's agent stack:

```
chat  →  harness  →  agent-runtime  →  ai
                     ^^^^^^^^^^^^^^^
                      you are here
```

`agent-runtime` depends on `crux-ai` (for LLM types and providers).
`agent-runtime` does NOT depend on `harness` or `chat`.
`harness` depends on `agent-runtime` (for the agent loop).

## Packages

### Core agent loop
| package | role |
|---------|------|
| `agent` | The autonomous LLM loop: streaming, tool dispatch, event emission |
| `agent/types.go` | AgentTool, AgentEvent, AgentLoopConfig |
| `agent/agent-loop.go` | Dual-level loop (outer + inner), tool execution |

### Conversation management
| package | role |
|---------|------|
| `session` | Persistent session storage (JSONL, SQLite, Memory) |
| `session/types.go` | SessionTreeEntry, SessionStorage interface |
| `session/storage.go` | JSONLStorage, MemoryStorage backends |
| `session/sqlite.go` | SQLiteStorage backend |
| `session/session.go` | Session, AgentSession with event subscription |

### Context window management
| package | role |
|---------|------|
| `context` | Token counting, compaction strategies, context manager |
| `context/token.go` | DefaultTokenCounter, ContextWindowConfig |
| `context/compaction.go` | SlideWindow, LLMSummarize, ChainedCompactor |
| `context/manager.go` | Manager: auto-compact, stats, session integration |

### Long-term memory
| package | role |
|---------|------|
| `memory` | KV store for cross-session facts |
| `memory/memory.go` | Get/Set/Delete, JSON persistence, FormatForPrompt |

### Auto-learning
| package | role |
|---------|------|
| `autolearn` | Automatic memory extraction from conversations |
| `autolearn/autolearn.go` | 4 trigger sources: explicit, tool, natural language, LLM |

### Planned (not yet implemented)
| package | role |
|---------|------|
| `turn` | Turn FSM: persistent state machine for agent turns |
| `turn/agent_runner.go` | Bridge between AgentSession and Turn FSM |

## What's NOT in here

- LLM types and providers → `crux-ai`
- Policy enforcement, approval gates → `crux-harness`
- HTTP server / chat UI → `crux-chat`
- Turn FSM (planned) → will be in `turn/`

## Principles

- **Event-driven architecture.** The agent loop emits events (AgentEvent) at every step. Callers subscribe to observe, audit, or react. Events are non-blocking: slow subscribers get dropped.
- **Composable by design.** Each package (session, context, memory, autolearn) is independent. Use one, some, or all. The agent loop has hooks for each.
- **Persistent by default.** Sessions, memory, and turn state are designed for persistence. JSON files for dev, SQLite for production.
- **Async-friendly.** Auto-learning, compaction, and event fanout all run in goroutines. The main loop never blocks on side effects.

## The agent loop (excerpt)

```
AgentLoop(ctx, messages, config)
  │
  ├─ outer loop: check follow-up messages
  │
  └─ inner loop:
       ├─ inject steering messages
       ├─ streamAssistantResponse()
       │    ├─ transformContext() ← context.Manager
       │    ├─ convertToLlm()
       │    ├─ invokeStreamFn()   ← llm.StreamSimpleWithContext
       │    └─ consumeStreamEvents() → EventMessageUpdate
       │
       ├─ extractToolCalls()
       │
       ├─ executeToolCalls()
       │    ├─ beforeToolCall hook  ← autolearn.ProcessToolResult
       │    ├─ tool.Execute()
       │    └─ afterToolCall hook
       │
       ├─ check termination (StopReason)
       ├─ PrepareNextTurn hook  ← context.CompactIfNeeded
       └─ ShouldStopAfterTurn hook
```

## Package dependency graph

```
agent
  ├── session
  │     └── core (crux-ai)
  ├── context
  │     ├── core (crux-ai)
  │     └── session
  ├── memory
  │     └── (standalone, JSON only)
  └── autolearn
        ├── core (crux-ai)
        └── memory
```

No circular dependencies. Each package can be used independently.

## Integration patterns

### Pattern 1: Basic agent loop (no extras)

```go
config := agent.AgentLoopConfig{
    Model:    model,
    StreamFn: streamFn,
    Tools:    tools,
}
stream := agent.AgentLoop(ctx, messages, config)
result, _ := stream.Result()
```

### Pattern 2: With session persistence

```go
storage, _ := session.NewSQLiteStorage("./session.db")
sess, _ := session.NewSession(storage)
sess.Append(session.NewUserMessageEntry("Hello"))

config := agent.AgentLoopConfig{...}
config.OnEvent = func(e agent.AgentEvent) {
    if em, ok := e.(agent.EventMessageEnd); ok {
        sess.Append(session.NewAssistantMessageEntry(em.Message))
    }
}
```

### Pattern 3: With context management

```go
ctxMgr := context.NewManager(context.DefaultContextWindowConfig())
ctxMgr.SetCompactor(context.NewSlideWindow(50))
ctxMgr.LoadFromSession(sess)

config.TransformContext = func(msgs []core.Message) []core.Message {
    for _, m := range msgs {
        ctxMgr.AddMessage(m)
    }
    return ctxMgr.GetMessages()
}
```

### Pattern 4: With memory + auto-learn

```go
mem, _ := memory.New("./memory.json")
learner := autolearn.New(mem, autolearn.DefaultSettings())

// Inject memory into system prompt
config.SystemPrompt = basePrompt + "\n\n" + mem.FormatForPrompt()

// Auto-learn from user input
config.ConvertToLlm = func(msgs []core.Message) []core.Message {
    for _, m := range msgs {
        if um, ok := m.(core.UserMessage); ok {
            learner.ProcessUserInput(fmt.Sprintf("%v", um.Content))
        }
    }
    return msgs
}
```

### Pattern 5: Full integration (all packages)

```go
// 1. Create components
mem, _ := memory.New("./memory.json")
storage, _ := session.NewSQLiteStorage("./session.db")
sess, _ := session.NewSession(storage)
ctxMgr := context.NewManager(context.DefaultContextWindowConfig())
learner := autolearn.New(mem, autolearn.DefaultSettings())

// 2. Configure context manager
ctxMgr.SetCompactor(&context.ChainedCompactor{
    Compactors: []context.Compactor{
        context.NewLLMSummarize(),
        context.NewSlideWindow(50),
    },
})
ctxMgr.LoadFromSession(sess)

// 3. Build agent config
config := agent.AgentLoopConfig{
    Model:        model,
    SystemPrompt: basePrompt + "\n\n" + mem.FormatForPrompt(),
    Tools:        tools,
    StreamFn:     streamFn,
    TransformContext: func(msgs []core.Message) []core.Message {
        for _, m := range msgs {
            ctxMgr.AddMessage(m)
        }
        return ctxMgr.GetMessages()
    },
    ConvertToLlm: func(msgs []core.Message) []core.Message {
        for _, m := range msgs {
            if um, ok := m.(core.UserMessage); ok {
                learner.ProcessUserInput(fmt.Sprintf("%v", um.Content))
            }
        }
        return msgs
    },
    OnEvent: func(e agent.AgentEvent) {
        switch ev := e.(type) {
        case agent.EventMessageEnd:
            sess.Append(session.NewAssistantMessageEntry(ev.Message))
        case agent.EventToolExecEnd:
            learner.ProcessToolResult(string(ev.Result))
        }
    },
}

// 4. Run
stream := agent.AgentLoop(ctx, messages, config)
```

## Testing

```bash
# Run all tests
go test ./...

# Run specific package
go test ./session/ -v
go test ./context/ -v
go test ./memory/ -v
go test ./autolearn/ -v

# Coverage
go test ./... -cover
```

### Test coverage

| package | tests | coverage |
|---------|-------|----------|
| agent | 0 | 0% (pending) |
| session | 21 | 82% |
| context | 15 | 76% |
| memory | 13 | 77% |
| autolearn | 20 | 88% |

## Roadmap

See [TODO.md](TODO.md) for the full list. Key items:

1. **Turn FSM** — Persistent state machine for agent turns (see [docs/turn-fsm-design.md](docs/turn-fsm-design.md))
2. **Agent tests** — Unit tests for the core loop
3. **Package integration** — Wire session/memory/context/autolearn into agent
4. **Metrics** — Prometheus counters for tokens, turns, compactions
