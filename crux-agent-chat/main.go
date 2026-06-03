package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "crux-ai/providers"

	"crux-agent-chat/agent"
	"crux-agent-chat/command"
	"crux-agent-chat/config"
	"crux-agent-chat/harness"
	"crux-agent-chat/tools"
	"crux-agent-chat/ui"

	agentruntime "crux-agent-runtime/agent"
	"crux-ai/core"

	"golang.org/x/term"
)

// chatAgent is a thin wrapper around the runtime Agent that lets us
// swap in a compacted message list between queries (the runtime's
// public API does not expose a SetMessages method).
type chatAgent struct {
	inner       *agentruntime.Agent
	mu          sync.Mutex
	override    []core.Message
	subscribers []func(agentruntime.AgentEvent)
}

// sessionWriter persists messages to the harness session.
// It tracks which messages have already been persisted so we only
// write new ones on each REPL iteration.
type sessionWriter struct {
	hr        *harness.Harness
	persisted int // number of messages already persisted from the agent's list
}

func (c *chatAgent) Run(ctx context.Context, prompt core.UserMessage) ([]core.Message, error) {
	c.mu.Lock()
	override := c.override
	c.override = nil
	inner := c.inner
	if len(override) > 0 {
		// Apply compaction: rebuild the agent's state with compacted history.
		state := inner.State()
		state.Messages = override
		newInner := agentruntime.New(agentruntime.AgentOptions{InitialState: &state})
		c.inner = newInner
		inner = newInner
		for _, fn := range c.subscribers {
			c.inner.Subscribe(fn)
		}
	}
	c.mu.Unlock()
	return inner.Run(ctx, prompt)
}

func (c *chatAgent) Subscribe(fn func(agentruntime.AgentEvent)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subscribers = append(c.subscribers, fn)
	if c.inner != nil {
		c.inner.Subscribe(fn)
	}
}
func (c *chatAgent) Abort() {
	c.mu.Lock()
	c.inner.Abort()
	c.mu.Unlock()
}
func (c *chatAgent) State() agentruntime.AgentState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inner.State()
}
func (c *chatAgent) Messages() []core.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inner.Messages()
}
func (c *chatAgent) SetOverride(msgs []core.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]core.Message, len(msgs))
	copy(cp, msgs)
	c.override = cp
}

// ResetSubscribers clears all event subscribers. Used by /clear to
// prevent subscriber list from growing indefinitely.
func (c *chatAgent) ResetSubscribers() {
	c.mu.Lock()
	c.subscribers = nil
	c.mu.Unlock()
}

// newSessionWriter creates a session writer that knows how many messages
// have already been persisted from the given agent's message list.
func newSessionWriter(hr *harness.Harness, msgs []core.Message) *sessionWriter {
	return &sessionWriter{hr: hr, persisted: len(msgs)}
}

// flush persists all messages in msgs that haven't been persisted yet.
func (sw *sessionWriter) flush(msgs []core.Message) {
	// After compaction, the message list may be shorter than persisted count.
	// In that case, reset persisted to the current length (all messages are "new"
	// from the session's perspective after compaction replaces the history).
	if sw.persisted > len(msgs) {
		sw.persisted = 0
	}
	for _, m := range msgs[sw.persisted:] {
		_ = sw.hr.RecordMessage(m)
	}
	sw.persisted = len(msgs)
}

// reset updates the tracked count after compaction or clear.
func (sw *sessionWriter) reset(count int) {
	sw.persisted = count
}

func main() {
	// Initialize command registry
	cmdRegistry := command.NewRegistry()

	cfg, err := config.Load()
	if err != nil {
		ui.PrintError("Configuration error: %v", err)
		fmt.Println("\nCreate a .env file with your API key, for example:")
		fmt.Println("  ANTHROPIC_API_KEY=sk-ant-...")
		fmt.Println("  OPENAI_API_KEY=sk-...")
		fmt.Println("  DEEPSEEK_API_KEY=sk-...")
		os.Exit(1)
	}

	// Build the harness (approval / skills / context / session / tokens).
	wd, _ := os.Getwd()
	hr, err := harness.New(harness.Config{
		Model:   cfg.GetModel(),
		APIKey:  cfg.GetAPIKey(),
		WorkDir: wd,
		// Default policy: prompt for write/edit/bash, auto-allow read-only.
		AutoApprove: []string{"read_file", "list_files", "read_image"},
		AlwaysDeny:  nil,
		Threshold:   0.9,
		MinKeep:     10,
	})
	if err != nil {
		ui.PrintError("Harness init failed: %v", err)
		os.Exit(1)
	}
	defer hr.Close()

	ui.PrintBanner(string(cfg.Provider), cfg.ModelID)
	ui.PrintMultimodalHint()
	ui.PrintHarnessSummary(hr)

	// Coding agent, integrated with the harness.
	ca := &chatAgent{inner: agent.NewCodingAgentWithHarness(agent.Options{Config: cfg, Harness: hr})}
	ca.Subscribe(func(evt agentruntime.AgentEvent) {
		ui.Subscriber()(evt)
		chatSubscriber(evt, hr)
	})

	// Track Ctrl+C presses: first aborts current query, second exits the REPL.
	var ctrlCCount int32
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range sigCh {
			count := atomic.AddInt32(&ctrlCCount, 1)
			if count == 1 {
				ui.PrintInfo("\n⏸  Aborting current query... (press Ctrl+C again to quit)")
				ca.Abort()
				// Reset counter after a short delay to allow user to continue
				go func() {
					time.Sleep(2 * time.Second)
					atomic.StoreInt32(&ctrlCCount, 0)
				}()
			} else {
				ui.PrintInfo("\n👋 Exiting...")
				os.Exit(0)
			}
		}
	}()
	defer signal.Stop(sigCh)

	// sessionWriter tracks which messages have already been persisted to the
	// session file. On first iteration, all existing messages in the agent
	// are already in the session (loaded from file during harness init),
	// so persisted starts at len(currentMessages). Only new messages from
	// each Run() call will be flushed.
	sw := newSessionWriter(hr, ca.Messages())

	var pendingImages []core.ContentBlock

	for {
		atomic.StoreInt32(&ctrlCCount, 0)

		input := readInputWithESC(ui.FormatUserPrompt(len(pendingImages)))
		if input == "" {
			continue
		}

		// Handle slash commands
		if strings.HasPrefix(input, "/") {
			var result command.HandlerResult

			// Handle special commands that need custom processing
			if strings.HasPrefix(input, "/paste") {
				result = command.HandlePaste(input)
			} else if input == "/clearimg" {
				result = command.HandleClearImg()
			} else {
				// Use the command registry for other commands
				cmdCtx := &command.Context{
					Agent:     ca,
					Harness:   hr,
					Config:    cfg,
					SessionID: hr.SessionID(),
				}
				result = cmdRegistry.Handle(cmdCtx, input)

				// Handle /clear command that needs agent recreation
				if input == "/clear" && result.ResetSession {
					ca.ResetSubscribers()
					ca.SetOverride(nil)
					ca.inner = agent.NewCodingAgentWithHarness(agent.Options{Config: cfg, Harness: hr})
					ca.Subscribe(func(evt agentruntime.AgentEvent) {
						ui.Subscriber()(evt)
						chatSubscriber(evt, hr)
					})
				}

				// Handle /compact command
				if input == "/compact" && result.ResetSession {
					forceCompact(hr, ca)
				}

				// Handle /new command
				if input == "/new" && result.ResetSession {
					wd, _ := os.Getwd()
					newHr, err := harness.New(harness.Config{
						Model:       cfg.GetModel(),
						APIKey:      cfg.GetAPIKey(),
						WorkDir:     wd,
						AutoApprove: []string{"read_file", "list_files", "read_image"},
						Threshold:   0.9,
						MinKeep:     10,
					})
					if err != nil {
						ui.PrintError("Failed to create new session: %v", err)
					} else {
						newCa := &chatAgent{inner: agent.NewCodingAgentWithHarness(agent.Options{Config: cfg, Harness: newHr})}
						newCa.Subscribe(func(evt agentruntime.AgentEvent) {
							ui.Subscriber()(evt)
							chatSubscriber(evt, newHr)
						})
						ui.PrintInfo("✅ New session created: %s", newHr.SessionID())
						hr.Close()
						hr = newHr
						ca = newCa
						result.NewHarness = newHr
						result.NewAgent = newCa
					}
				}

				// Handle /restore command
				if strings.HasPrefix(input, "/restore") && result.ResetSession && result.RestoreSessionID != "" {
					wd, _ := os.Getwd()
					newHr, err := harness.RestoreSession(harness.Config{
						Model:       cfg.GetModel(),
						APIKey:      cfg.GetAPIKey(),
						WorkDir:     wd,
						AutoApprove: []string{"read_file", "list_files", "read_image"},
						Threshold:   0.9,
						MinKeep:     10,
					}, result.RestoreSessionID)
					if err != nil {
						ui.PrintError("Failed to restore session: %v", err)
					} else {
						newCa := &chatAgent{inner: agent.NewCodingAgentWithHarness(agent.Options{Config: cfg, Harness: newHr})}
						newCa.Subscribe(func(evt agentruntime.AgentEvent) {
							ui.Subscriber()(evt)
							chatSubscriber(evt, newHr)
						})
						// Load messages from restored session
						msgs := newHr.BuildContext()
						newCa.SetOverride(msgs)
						ui.PrintInfo("✅ Session restored: %s", newHr.SessionID())
						hr.Close()
						hr = newHr
						ca = newCa
						result.NewHarness = newHr
						result.NewAgent = newCa
					}
				}
			}

			if result.ClearPending {
				pendingImages = nil
			}
			if result.ResetSession {
				sw = newSessionWriter(hr, ca.Messages())
			}
			if len(result.StagedBlocks) > 0 {
				pendingImages = append(pendingImages, result.StagedBlocks...)
			}
			if result.NewHarness != nil && result.NewAgent != nil {
				sw = newSessionWriter(result.NewHarness, result.NewAgent.Messages())
			}
			if result.Done {
				return
			}
			if result.Handled {
				continue
			}
		}

		ui.PrintSeparator()

		// Auto-detect image paths in the input.
		autoImages, rest := extractImagePaths(input)
		if len(autoImages) > 0 {
			added, skipped := command.StageImages(autoImages, &pendingImages)
			ui.PrintInfo("🖼  Detected %d image path(s)%s", added, command.SkippedMsg(skipped))
			input = rest
			if strings.TrimSpace(input) == "" {
				ui.PrintInfo("Add a question and press Enter (or send blank to skip):")
				continue
			}
		}

		// Build the prompt content: text + any pending images.
		var content any = input
		if len(pendingImages) > 0 {
			blocks := make([]core.ContentBlock, 0, len(pendingImages)+1)
			blocks = append(blocks, core.TextContent{Type: "text", Text: input})
			blocks = append(blocks, pendingImages...)
			content = blocks
			pendingImages = nil
		}

		// Persist the session before each query so that the full conversation
		// history is durably recorded, even if the process crashes during Run.
		// We flush all messages from the agent's internal list that haven't
		// been persisted yet (e.g. after compaction in a prior iteration).
		sw.flush(ca.Messages())

		// Maybe compact before the next LLM call to stay within context limits.
		maybeCompactBeforeQuery(hr, ca)

		// After compaction, the agent's message list may have been replaced
		// with a compressed shorter list. Reset the session writer's count
		// to reflect the new list, so we don't re-persist the compressed
		// messages that are already in the session file.
		sw.reset(len(ca.Messages()))

		// Use a context with the configured timeout.  Signal-based cancellation
		// (Ctrl+C) is handled separately by ca.Abort(), which sets an atomic flag
		// checked within the agent's Run loop, so the Go context here is only
		// for enforcing the query timeout.
		queryTimeout := cfg.QueryTimeout
		if queryTimeout <= 0 {
			queryTimeout = 10 * time.Minute
		}
		queryCtx, queryCancel := context.WithTimeout(context.Background(), queryTimeout)
		_, err := ca.Run(queryCtx, core.UserMessage{
			Role:      "user",
			Content:   content,
			Timestamp: time.Now(),
		})
		queryCancel()
		if err != nil {
			ui.PrintError("Agent error: %v", err)
		}

		// Persist all new messages the agent appended during Run.
		sw.flush(ca.Messages())

		fmt.Println()
	}
}

func extractImagePaths(input string) (paths []string, rest string) {
	tokens := tokenize(input)
	var kept []string
	for _, tok := range tokens {
		clean := strings.Trim(tok, "\"'")
		if tools.IsImagePath(clean) && fileExists(clean) {
			paths = append(paths, tok)
		} else {
			kept = append(kept, tok)
		}
	}
	return paths, strings.Join(kept, " ")
}

// tokenize splits s into tokens, respecting quoted substrings.
// Backslash-escaped quotes inside a quoted string are treated as
// literal quote characters (e.g. "hello \"world\"" includes the inner quotes).
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote != 0:
			// Handle backslash-escaped quote inside the quoted string.
			if c == '\\' && i+1 < len(s) && s[i+1] == inQuote {
				cur.WriteByte(inQuote)
				i++ // skip the escaped quote
				continue
			}
			cur.WriteByte(c)
			if c == inQuote {
				out = append(out, cur.String())
				cur.Reset()
				inQuote = 0
			}
		case c == '"' || c == '\'':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			cur.WriteByte(c)
			inQuote = c
		case c == ' ' || c == '\t':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// readInputWithESC reads a line from stdin and returns the input string.
// Returns an empty string if ESC is pressed.
func readInputWithESC(prompt string) string {
	fmt.Print(prompt)

	// Save the original terminal state
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		// Fallback to regular input if terminal mode can't be changed
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			return strings.TrimSpace(scanner.Text())
		}
		return ""
	}
	defer term.Restore(fd, oldState)

	var input bytes.Buffer
	buf := make([]byte, 4) // UTF-8 max is 4 bytes

	for {
		n, err := os.Stdin.Read(buf[:1])
		if err != nil || n == 0 {
			return strings.TrimSpace(input.String())
		}

		firstByte := buf[0]

		// Handle ESC key or escape sequences (arrow keys, etc.)
		if firstByte == 27 {
			// Read next byte to check if it's an escape sequence
			n, _ = os.Stdin.Read(buf[1:2])
			if n >= 1 && buf[1] == '[' {
				// This is an escape sequence (arrow keys, etc.), ignore it
				continue
			}
			// Single ESC key pressed - cancel input
			fmt.Println()
			ui.PrintInfo("⏸  Query cancelled with ESC.")
			return ""
		}

		// Handle Ctrl+C
		if firstByte == 3 {
			fmt.Println()
			ui.PrintInfo("⏸  Input cancelled with Ctrl+C.")
			return ""
		}

		// Handle Enter
		if firstByte == 13 {
			fmt.Println()
			return strings.TrimSpace(input.String())
		}

		// Handle Backspace
		if firstByte == 127 || firstByte == 8 {
			if input.Len() > 0 {
				// Get the last rune to calculate its byte length
				lastRune := []rune(input.String())
				if len(lastRune) > 0 {
					lastCharBytes := len(string(lastRune[len(lastRune)-1]))
					input.Truncate(input.Len() - lastCharBytes)
					// Erase the character on screen
					fmt.Print("\b \b")
				}
			}
			continue
		}

		// Handle regular characters - UTF-8 multi-byte support
		// Determine how many bytes this UTF-8 character has
		var charLen int
		if firstByte&0x80 == 0 {
			// ASCII character (1 byte)
			charLen = 1
		} else if firstByte&0xE0 == 0xC0 {
			// 2-byte UTF-8 character
			charLen = 2
		} else if firstByte&0xF0 == 0xE0 {
			// 3-byte UTF-8 character (most Chinese characters)
			charLen = 3
		} else if firstByte&0xF8 == 0xF0 {
			// 4-byte UTF-8 character
			charLen = 4
		} else {
			// Invalid UTF-8, treat as single byte
			charLen = 1
		}

		// Read remaining bytes for multi-byte characters
		if charLen > 1 {
			n, _ = os.Stdin.Read(buf[1:charLen])
			if n != charLen-1 {
				// Failed to read full character, just use what we have
			}
		}

		// Write the character to input
		input.Write(buf[:charLen])
		fmt.Print(string(buf[:charLen]))
	}
}

// chatToolsCore returns the tool list as core.Tool for the harness.
func chatToolsCore() []core.Tool {
	all := tools.AllTools()
	return harness.AgentToolsToCore(all)
}

// maybeCompactBeforeQuery checks the live agent state and runs
// compaction if the context has crossed the configured threshold.
func maybeCompactBeforeQuery(hr *harness.Harness, ca *chatAgent) {
	state := ca.State()
	est := hr.EstimateUsage(state.SystemPrompt, state.Messages, chatToolsCore())

	ui.PrintInfo("🗜  Context check: %d tokens used, %d messages", est.Used, len(state.Messages))

	if !hr.ShouldCompact(state.SystemPrompt, state.Messages, chatToolsCore()) {
		return
	}
	ui.PrintInfo("🗜  Context near limit — compacting before query...")
	newMsgs, res, err := hr.Compact(context.Background(), state.SystemPrompt, state.Messages, chatToolsCore())
	if err != nil {
		ui.PrintWarn("Compaction failed: %v", err)
		return
	}
	if res == nil {
		return
	}
	// Apply compaction immediately: rebuild the agent's state with the
	// compacted message list, so ca.Messages() reflects the new list
	// right away (not just on the next ca.Run()).
	ca.mu.Lock()
	ca.override = nil
	state.Messages = newMsgs
	newInner := agentruntime.New(agentruntime.AgentOptions{InitialState: &state})
	subs := ca.subscribers
	ca.inner = newInner
	for _, fn := range subs {
		ca.inner.Subscribe(fn)
	}
	ca.mu.Unlock()
	// Record compaction and persist the compacted messages
	if err := hr.CompactAndPersist(res.Summary, res.TokensBefore, newMsgs); err != nil {
		ui.PrintWarn("Failed to persist compaction: %v", err)
	}
	ui.PrintInfo("   saved %d tokens (kept %d recent messages)", res.TokensSaved, res.KeptCount)
}

func forceCompact(hr *harness.Harness, ca *chatAgent) {
	state := ca.State()
	newMsgs, res, err := hr.Compact(context.Background(), state.SystemPrompt, state.Messages, chatToolsCore())
	if err != nil {
		ui.PrintError("Compaction failed: %v", err)
		return
	}
	if res == nil {
		ui.PrintInfo("No compaction needed.")
		return
	}
	// Apply compaction immediately: rebuild the agent's state with the
	// compacted message list, so ca.Messages() reflects the new list
	// right away (not just on the next ca.Run()).
	ca.mu.Lock()
	ca.override = nil // discard any pending override
	state.Messages = newMsgs
	newInner := agentruntime.New(agentruntime.AgentOptions{InitialState: &state})
	subs := ca.subscribers
	ca.inner = newInner
	for _, fn := range subs {
		ca.inner.Subscribe(fn)
	}
	ca.mu.Unlock()
	// Record compaction and persist the compacted messages
	if err := hr.CompactAndPersist(res.Summary, res.TokensBefore, newMsgs); err != nil {
		ui.PrintWarn("Failed to persist compaction: %v", err)
	}
	ui.PrintInfo("✅ Compacted: %d → %d tokens (saved %d)", res.TokensBefore, res.TokensAfter, res.TokensSaved)
}

// chatSubscriber is the agent event listener that accumulates token
// usage from every assistant message.
//
// Only EventMessageEnd is used to record usage. EventMessageUpdate+EventDone
// is NOT used because it would cause double-counting (EventMessageEnd already
// contains the final message with complete Usage data).
func chatSubscriber(evt agentruntime.AgentEvent, hr *harness.Harness) {
	switch e := evt.(type) {
	case agentruntime.EventMessageEnd:
		hr.AccumulateUsage(e.Message.Usage)
		// Print usage information after each message
		usage := e.Message.Usage
		if usage.TotalTokens > 0 {
			ui.PrintInfo("💰 Usage: Input=%d, Output=%d, Total=%d",
				usage.Input, usage.Output, usage.TotalTokens)
		}
	}
}
