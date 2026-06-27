package main

import (
	"fmt"
	"os"
	"time"

	"github.com/hycjack/crux-ai/core"

	"crux-agent-runtime/agent"
	"crux-agent-runtime/session"
)

// newSession creates a fresh session and resets the agent state.
// sessPath and sess are passed by pointer so the caller in the REPL
// sees the updated values for subsequent persistence.
func newSession(sess **session.Session, sessPath *string, a *agent.Agent, systemPrompt string) {
	if old := *sess; old != nil {
		_ = old.Close()
	}

	newPath := fmt.Sprintf("./sessions/demo-%d.jsonl", time.Now().UnixNano())
	newStore, err := session.NewJSONLStorage(newPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[session] failed to create new session: %v\n", err)
		return
	}

	newSess, err := session.NewSession(newStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[session] failed to init new session: %v\n", err)
		_ = newStore.Close()
		return
	}

	if err := newSess.SetID(fmt.Sprintf("demo-%d", time.Now().UnixNano())); err != nil {
		fmt.Fprintf(os.Stderr, "[session] failed to set ID: %v\n", err)
	}

	// Update caller's references so subsequent turns persist to the new file.
	*sess = newSess
	*sessPath = newPath

	// Reset agent state: clear message history and refresh system prompt.
	a.Reset()
	a.SetSystemPrompt(systemPrompt)

	fmt.Printf("[session] started new session: %s\n", newSess.ID())
	fmt.Printf("[session] file: %s\n", newPath)
}

// messageToEntry converts a core.Message into a SessionTreeEntry.
// Errors are returned so the caller can skip unsupported message types
// without aborting the whole turn.
func messageToEntry(msg core.Message) (session.SessionTreeEntry, error) {
	switch m := msg.(type) {
	case core.UserMessage:
		text, ok := m.Content.(string)
		if !ok {
			return session.SessionTreeEntry{}, fmt.Errorf("user message content is not a string: %T", m.Content)
		}
		return session.NewUserMessageEntry(text), nil
	case core.AssistantMessage:
		return session.NewAssistantMessageEntry(m), nil
	case core.ToolResultMessage:
		return session.NewToolResultEntry(m.ToolCallID, m.ToolName, m.Content, m.IsError), nil
	default:
		return session.SessionTreeEntry{}, fmt.Errorf("unsupported message type: %T", msg)
	}
}