# crux-turn

A standalone, reusable Turn FSM library — drives a single agent turn (user
input → complete response) through 9 explicit states with persistence and
reactive triggers. No external dependencies on any specific LLM SDK.

## Why

Existing turn packages in `crux-harness/turn/` couple the FSM core to
`crux-ai.Message` and `crux-harness/approval.Service`. This library extracts
the FSM into a standalone, type-agnostic module:

- `type TurnMsg any` — consumer passes their own message type
- `MessageAdapter[Msg, Call, Result]` — adapter lets the FSM operate on
  any message format
- Built-in `Store` interface + `MemoryStore` + `sqlite` subpackage
- Built-in `approval.SQLiteStore` (schema-compatible with old
  `crux-harness/approval`)

## Installation

```bash
go get github.com/hycjack/crux-turn
```

## Quick Start

```go
package main

import (
    "context"
    "encoding/json"
    "github.com/hycjack/crux-turn"
    "github.com/hycjack/crux-turn/sqlite"
)

type MyMsg struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type MyCall struct {
    ID   string          `json:"id"`
    Name string          `json:"name"`
    Args json.RawMessage `json:"args"`
}

type MyResult struct {
    ToolCallID string `json:"tool_call_id"`
    Content    string `json:"content"`
}

type MyAdapter struct{}

func (MyAdapter) MessageRole(m MyMsg) string             { return m.Role }
func (MyAdapter) MessageContent(m MyMsg) string          { return m.Content }
func (MyAdapter) MessageToolCalls(m MyMsg) []MyCall     { return nil }
func (MyAdapter) NewAssistantMessage(c string, t []MyCall) MyMsg {
    return MyMsg{Role: "assistant", Content: c}
}
func (MyAdapter) NewToolMessage(callID, name, content string) MyMsg {
    return MyMsg{Role: "tool", Content: content}
}
func (MyAdapter) CallID(c MyCall) string                { return c.ID }
func (MyAdapter) CallName(c MyCall) string              { return c.Name }
func (MyAdapter) CallArgs(c MyCall) json.RawMessage     { return c.Args }
func (MyAdapter) ResultID(r MyResult) string            { return r.ToolCallID }
func (MyAdapter) ResultContent(r MyResult) string       { return r.Content }

func main() {
    store, _ := sqlite.NewStore("/tmp/turns.db")
    defer store.Close()

    m := turn.New[MyMsg, MyCall, MyResult](store, MyAdapter{},
        turn.WithMaxRounds(10),
        turn.WithStreamFn(func(ctx context.Context, msgs []MyMsg, tools []turn.ToolSchema[MyCall]) (MyMsg, error) {
            // call your LLM here
            return MyMsg{Role: "assistant", Content: "hi"}, nil
        }),
    )

    result, _ := m.Prompt(context.Background(), "session-1", MyMsg{Role: "user", Content: "hello"})
    _ = result
}
```

## Design

See `/root/crux/docs/modules/50-turn-extraction.md` for the full design doc.

## License

Same as the parent Crux project.