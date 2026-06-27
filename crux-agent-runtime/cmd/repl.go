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
	"golang.org/x/term"

	"crux-agent-runtime/agent"
	"crux-agent-runtime/session"
)

// inputEvents carries keystroke events from the raw-mode terminal reader
// to the REPL select loop.
type inputEvents struct {
	line  chan string // completed lines (terminated by Enter)
	esc   chan struct{}
	ctrlC chan struct{}
	done  chan struct{} // closed when the reader exits (EOF / error)
}

// newInputEvents returns a wired-up inputEvents struct.
func newInputEvents() *inputEvents {
	return &inputEvents{
		line:  make(chan string, 1),
		esc:   make(chan struct{}, 1),
		ctrlC: make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
}

// enableRawInput puts os.Stdin into raw mode and starts a goroutine that
// forwards keystrokes into ev. Returns a restore function the caller
// MUST defer to put the terminal back into cooked mode.
//
// On non-TTY stdin (e.g. piped input), raw mode is skipped and a simple
// line scanner is used instead — only ev.line and ev.done carry signals;
// ev.esc / ev.ctrlC never fire.
func enableRawInput(ev *inputEvents) (restore func()) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		go readLinesCooked(ev)
		return func() {}
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[repl] raw mode failed: %v\n", err)
		return func() {}
	}
	go readKeys(fd, ev)
	return func() { _ = term.Restore(fd, oldState) }
}

// readLinesCooked is the fallback reader used when stdin is not a TTY
// (piped input, CI, scripted runs). It scans whole lines and forwards
// them on ev.line; ev.esc / ev.ctrlC stay silent.
func readLinesCooked(ev *inputEvents) {
	defer close(ev.done)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		select {
		case ev.line <- scanner.Text():
		default:
		}
	}
}

// readKeys is the raw-mode terminal reader. It runs in its own goroutine
// until stdin closes, building a line buffer and dispatching:
//
//   - Enter → emits a line via ev.line
//   - ESC → emits via ev.esc (clears current buffer)
//   - Ctrl+C → emits via ev.ctrlC (clears current buffer)
//   - Backspace → deletes last character from buffer + erases on screen
//   - Printable ASCII → appends to buffer + echoes
//
// Anything else (arrow keys, etc.) is ignored.
func readKeys(fd int, ev *inputEvents) {
	defer close(ev.done)
	var buf []byte
	b := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(b)
		if err != nil || n == 0 {
			return
		}
		buf = processKey(b[0], buf, ev)
	}
}

// processKey handles a single raw keystroke. It mutates the line buffer,
// emits events on ev, and echoes visible feedback to stdout. Returns the
// new buffer (which may be the same slice with the last byte dropped or
// a fresh slice if Enter/ESC/Ctrl+C cleared it).
//
// Extracted from readKeys so it can be unit-tested without a real TTY.
func processKey(key byte, buf []byte, ev *inputEvents) []byte {
	switch key {
	case 0x1b: // ESC
		clearLine(buf)
		send(ev.esc)
		return nil
	case 0x03: // Ctrl+C
		clearLine(buf)
		send(ev.ctrlC)
		return nil
	case '\r', '\n': // Enter
		os.Stdout.Write([]byte("\r\n"))
		select {
		case ev.line <- string(buf):
		default:
		}
		return nil
	case 0x7f, 0x08: // Backspace / DEL
		if len(buf) > 0 {
			buf = buf[:len(buf)-1]
			os.Stdout.Write([]byte("\b \b"))
		}
		return buf
	default:
		if key >= 32 && key < 127 {
			buf = append(buf, key)
			os.Stdout.Write([]byte{key})
		}
		return buf
	}
}

// send is a non-blocking channel send. We use buffered channels of size 1
// so a slow REPL never deadlocks the reader; if a slot is full the event
// is dropped (the REPL will pick up the next one).
func send(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// clearLine erases the current input buffer from the terminal by writing
// backspaces + spaces for each character then moving the cursor back.
func clearLine(buf []byte) {
	if len(buf) == 0 {
		return
	}
	erase := make([]byte, 0, len(buf)*3)
	for range buf {
		erase = append(erase, '\b', ' ', '\b')
	}
	os.Stdout.Write(erase)
}

// runREPL is the interactive prompt loop.
//
// State machine:
//
//	idle:    read lines; Ctrl+C exits, ESC clears current buffer
//	running: agent is generating; Ctrl+C and ESC both abort the turn
func runREPL(ctx context.Context, state *demoState) {
	ev := newInputEvents()
	restore := enableRawInput(ev)
	defer restore()

	running := false
	for {
		if !running {
			fmt.Print("\nYou: ")
		}
		select {
		case <-ctx.Done():
			return
		case <-ev.done:
			// Stdin closed (EOF / piped input exhausted). If the user
			// piped a script, just exit when it runs out.
			return

		case <-ev.ctrlC:
			if running {
				abortTurn(state, &running)
			} else {
				fmt.Println("\nGoodbye!")
				return
			}

		case <-ev.esc:
			if running {
				abortTurn(state, &running)
			}
			// At idle prompt: nothing to cancel; current buffer is
			// already cleared by the raw reader.

		case line := <-ev.line:
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if line == "/quit" || line == "/exit" {
				fmt.Println("Goodbye!")
				return
			}
			if cmd, ok := slashCommands[line]; ok {
				cmd(state)
				continue
			}
			running = true
			runTurn(ctx, state, line)
			running = false
		}
	}
}

// abortTurn cancels the in-flight agent run and resets REPL state.
func abortTurn(state *demoState, running *bool) {
	state.a.Abort()
	*running = false
	fmt.Fprintln(os.Stderr, "\n[cancelled]")
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
//   - thinking deltas → buffered in memory
//   - first text delta → flushes the thinking buffer to stderr BEFORE
//     printing the text, so the reasoning always appears above the reply
//   - subsequent text deltas → stdout (live, interleaved with user input)
//   - tool calls / agent lifecycle → stderr
//   - EventMessageEnd → trailing newline; flushes any leftover thinking
//     (only matters for messages that had reasoning but no text)
func (p *eventPrinter) handle(evt agent.AgentEvent) {
	switch e := evt.(type) {
	case agent.EventAgentStart:
		fmt.Fprintf(os.Stderr, "[agent] started\n")
	case agent.EventTurnStart:
		fmt.Fprintf(os.Stderr, "[agent] turn start\n")
	case agent.EventMessageUpdate:
		if ae, ok := e.AssistantEvent.(core.EventTextDelta); ok {
			p.flushThinking() // reasoning goes before the reply
			fmt.Print(ae.Delta)
			return
		}
		if ae, ok := e.AssistantEvent.(core.EventThinkingDelta); ok {
			p.mu.Lock()
			p.thinking.WriteString(ae.Delta)
			p.mu.Unlock()
			return
		}
		printAssistantEvent(e.AssistantEvent)
	case agent.EventMessageEnd:
		p.flushThinking() // catches reasoning-only messages with no text
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
