package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hycjack/crux-ai/core"

	"crux-agent-runtime/agent"
	"crux-agent-runtime/session"
)

// runREPL is the interactive prompt loop. Commands are dispatched through
// slashCommands for readability and easy extension.
func runREPL(ctx context.Context, state *demoState) {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nYou: ")
		if !scanner.Scan() {
			return
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// /quit has its own non-table path so the map stays free of exit semantics.
		if input == "/quit" || input == "/exit" {
			fmt.Println("Goodbye!")
			return
		}
		if cmd, ok := slashCommands[input]; ok {
			cmd(state)
			continue
		}

		runTurn(ctx, state, input)
	}
}

// runTurn processes a single user input: extract facts, call the agent,
// persist new messages, kick off async workflow extraction, and refresh
// the system prompt for the next turn.
func runTurn(ctx context.Context, state *demoState, input string) {
	if n := state.learner.ProcessUserInput(input); n > 0 {
		fmt.Fprintf(os.Stderr, "[autolearn] extracted %d memories\n", n)
	}

	fmt.Print("\nAssistant: ")
	prevCount := len(state.a.Messages())
	_, err := state.a.Run(ctx,
		core.UserMessage{Role: core.MessageRoleUser, Content: input, Timestamp: time.Now()},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		return
	}

	persistNewMessages(state, prevCount)
	_ = state.mem.Save()

	// Async workflow extraction: never blocks the conversation.
	if state.learner.Settings().AutoLearn {
		msgsCopy := append([]core.Message(nil), state.a.Messages()...)
		go func(msgs []core.Message) {
			bgCtx, bgCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer bgCancel()
			if n := state.learner.MaybeExtractWorkflow(bgCtx, msgs, state.wfExtractor); n > 0 {
				fmt.Fprintf(os.Stderr, "[workflow] extracted %d new skill(s) to %s\n", n, state.wfDir)
			}
		}(msgsCopy)
	}

	state.a.SetSystemPrompt(buildSystemPrompt(state.mem))
}

// persistNewMessages writes the agent's new messages (those appended
// during this turn) to the session storage. Errors are logged but not
// fatal — losing one entry should not crash the REPL.
func persistNewMessages(state *demoState, prevCount int) {
	current := state.a.Messages()
	if len(current) <= prevCount {
		return
	}
	newMsgs := current[prevCount:]
	entries := make([]session.SessionTreeEntry, 0, len(newMsgs))
	for _, msg := range newMsgs {
		entry, err := messageToEntry(msg)
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	if len(entries) > 0 {
		if err := state.sess.Append(entries...); err != nil {
			fmt.Fprintf(os.Stderr, "[session] failed to persist: %v\n", err)
		}
	}
}

// slashCommands is the dispatch table for /-prefixed REPL commands.
// Adding a new command = adding one entry here and one closure.
var slashCommands = map[string]func(*demoState){
	"/memory": func(s *demoState) {
		fmt.Println("\n--- Long-term Memory ---")
		fmt.Println(s.mem.FormatForPrompt())
		fmt.Println("--- End ---")
	},
	"/clear": func(s *demoState) {
		for _, k := range []string{"user.name", "user.location", "user.preferred_language"} {
			s.mem.Delete(k)
		}
		_ = s.mem.Save()
		fmt.Println("Memory cleared.")
	},
	"/new": func(s *demoState) {
		newSession(&s.sess, &s.sessPath, s.a, s.systemPrompt)
	},
	"/session": func(s *demoState) {
		fmt.Printf("\n--- Session ---\nID: %s\nEntries: %d\nFile: %s\n--- End ---\n",
			s.sess.ID(), len(s.sess.Entries()), s.sessPath)
	},
	"/learn": func(s *demoState) {
		fmt.Printf("\n--- AutoLearn ---\nEnabled: %v\nExtracted memories:\n", s.learner != nil)
		for _, k := range s.mem.Keys() {
			v, _ := s.mem.Get(k)
			fmt.Printf("  %s = %s\n", k, v)
		}
		fmt.Println("--- End ---")
	},
}

// eventPrinter formats AgentEvents for the REPL. It buffers thinking
// deltas and emits a single "[thinking] ...\n" line per assistant message
// instead of one line per delta (which spams the terminal).
type eventPrinter struct {
	mu       sync.Mutex
	thinking strings.Builder
}

func newEventPrinter() func(agent.AgentEvent) {
	p := &eventPrinter{}
	return p.handle
}

// handle dispatches one AgentEvent to the right sink.
//
//   - text deltas → stdout (so they interleave live with user input)
//   - thinking deltas → in-memory buffer (flushed on EventMessageEnd)
//   - tool calls / agent lifecycle → stderr
func (p *eventPrinter) handle(evt agent.AgentEvent) {
	switch e := evt.(type) {
	case agent.EventAgentStart:
		fmt.Fprintf(os.Stderr, "[agent] started\n")
	case agent.EventTurnStart:
		fmt.Fprintf(os.Stderr, "[agent] turn start\n")
	case agent.EventMessageUpdate:
		if ae, ok := e.AssistantEvent.(core.EventThinkingDelta); ok {
			p.mu.Lock()
			p.thinking.WriteString(ae.Delta)
			p.mu.Unlock()
			return
		}
		printAssistantEvent(e.AssistantEvent)
	case agent.EventMessageEnd:
		p.flushThinking()
		fmt.Println()
	case agent.EventToolExecStart:
		fmt.Fprintf(os.Stderr, "\n[tool:start] %s(%s)\n", e.ToolName, string(e.Args))
	case agent.EventToolExecEnd:
		fmt.Fprintf(os.Stderr, "[tool:end]   %s error=%v result=%s\n",
			e.ToolName, e.IsError, string(e.Result))
	case agent.EventTurnEnd:
		fmt.Fprintf(os.Stderr, "[agent] turn end\n")
	case agent.EventAgentEnd:
		fmt.Fprintf(os.Stderr, "[agent] ended\n")
	}
}

// flushThinking emits a single "[thinking] ..." line for the buffered
// thinking content (if any) and clears the buffer.
func (p *eventPrinter) flushThinking() {
	p.mu.Lock()
	defer p.mu.Unlock()
	content := strings.TrimSpace(p.thinking.String())
	if content != "" {
		fmt.Fprintf(os.Stderr, "[thinking] %s\n", content)
	}
	p.thinking.Reset()
}

// printAssistantEvent forwards an assistant event to the right sink:
// text → stdout, tool calls → stderr.
func printAssistantEvent(evt core.AssistantMessageEvent) {
	switch ae := evt.(type) {
	case core.EventTextDelta:
		fmt.Print(ae.Delta)
	case core.EventToolCallStart:
		fmt.Fprintf(os.Stderr, "\n[tool:start] %s\n", ae.Name)
	}
}
