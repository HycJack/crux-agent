package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "crux-ai/providers"

	"crux-agent-chat/agent"
	"crux-agent-chat/config"
	"crux-agent-chat/harness"
	"crux-agent-chat/tools"
	"crux-agent-chat/ui"

	agentruntime "crux-agent-runtime/agent"
	"crux-ai/core"
)

// chatAgent is a thin wrapper around the runtime Agent that lets us
// swap in a compacted message list between queries (the runtime's
// public API does not expose a SetMessages method).
type chatAgent struct {
	inner       *agentruntime.Agent
	mu          sync.Mutex
	override    []core.Message
	subscribers []func(agentruntime.AgentEvent)
	// msgCount tracks how many messages have been persisted to session,
	// so we only persist newly-added messages on each REPL iteration.
	msgCount int
}

func (c *chatAgent) Run(ctx context.Context, prompt core.UserMessage) ([]core.Message, error) {
	c.mu.Lock()
	override := c.override
	c.override = nil
	c.mu.Unlock()
	if len(override) > 0 {
		// Apply compaction: rebuild the agent's state with compacted history.
		state := c.inner.State()
		state.Messages = override
		newInner := agentruntime.New(agentruntime.AgentOptions{InitialState: &state})
		c.mu.Lock()
		c.inner = newInner
		for _, fn := range c.subscribers {
			c.inner.Subscribe(fn)
		}
		c.mu.Unlock()
	}
	return c.inner.Run(ctx, prompt)
}

func (c *chatAgent) Subscribe(fn func(agentruntime.AgentEvent)) {
	c.mu.Lock()
	c.subscribers = append(c.subscribers, fn)
	c.mu.Unlock()
	c.inner.Subscribe(fn)
}
func (c *chatAgent) Abort()                         { c.inner.Abort() }
func (c *chatAgent) State() agentruntime.AgentState { return c.inner.State() }
func (c *chatAgent) Messages() []core.Message       { return c.inner.Messages() }
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

func main() {
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
		APIKey:  cfg.APIKey,
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
			} else {
				ui.PrintInfo("\n👋 Exiting...")
				os.Exit(0)
			}
		}
	}()
	defer signal.Stop(sigCh)

	// REPL loop
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024), 1024*1024)

	var pendingImages []core.ContentBlock

	for {
		atomic.StoreInt32(&ctrlCCount, 0)
		fmt.Print(ui.FormatUserPrompt(len(pendingImages)))

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				ui.PrintError("Read error: %v", err)
			}
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Handle slash commands
		if strings.HasPrefix(input, "/") {
			handled, done := handleCommand(input, ca, cfg, hr)
			if done {
				return
			}
			if handled {
				continue
			}
		}

		ui.PrintSeparator()

		// Auto-detect image paths in the input.
		autoImages, rest := extractImagePaths(input)
		if len(autoImages) > 0 {
			added, skipped := stageImages(autoImages, &pendingImages)
			ui.PrintInfo("🖼  Detected %d image path(s)%s", added, skippedMsg(skipped))
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

		// Persist the user message to the session before sending it.
		_ = hr.RecordMessage(core.UserMessage{
			Role: "user", Content: content, Timestamp: time.Now(),
		})

		// Persist any existing messages that haven't been persisted yet
		// (this happens after compaction in a prior iteration — the compressed
		// messages must be written before we compact again).
		for _, m := range ca.Messages()[ca.msgCount:] {
			_ = hr.RecordMessage(m)
		}
		ca.msgCount = len(ca.Messages())

		// Snapshot messages before compaction so the session log preserves
		// the full history. Compaction may replace the in-memory message
		// list with a compressed version, but the session file already has
		// the full details.
		preCompactMsgCount := len(ca.Messages())

		// Maybe compact before the next LLM call.
		maybeCompactBeforeQuery(hr, ca)

		// If compaction happened, the agent's in-memory messages changed.
		// The compressed messages replace the full ones in memory, but the
		// session already has the full history persisted. Update msgCount
		// so we don't re-persist the compressed version.
		if len(ca.Messages()) != preCompactMsgCount {
			ca.msgCount = len(ca.Messages())
		}

		// Use a fresh context per query so Abort() can cancel just this one.
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

		// Persist only the new messages that the agent appended.
		for _, m := range ca.Messages()[ca.msgCount:] {
			_ = hr.RecordMessage(m)
		}
		ca.msgCount = len(ca.Messages())

		fmt.Println()
	}
}

// handleCommand dispatches a slash command.
//   - handled: the input was a command; the REPL should NOT also run it as a prompt
//   - done:    the command was /quit, exit the process
func handleCommand(input string, ca *chatAgent, cfg *config.Config, hr *harness.Harness) (handled, done bool) {
	cmd := strings.ToLower(input)

	// Image-related commands
	if strings.HasPrefix(cmd, "/paste") {
		args := strings.Fields(input)
		if len(args) < 2 {
			ui.PrintError("Usage: /paste <image-path> [more-image-paths...]")
			return true, false
		}
		var pending []core.ContentBlock
		added, skipped := stageImages(args[1:], &pending)
		ui.PrintInfo("📎 Staged %d image(s)%s (will attach to the next turn)", added, skippedMsg(skipped))
		return true, false
	}
	if strings.HasPrefix(cmd, "/clearimg") {
		ui.PrintInfo("Cleared staged images.")
		return true, false
	}

	switch cmd {
	case "/quit", "/exit":
		fmt.Println("Goodbye! 👋")
		return true, true
	case "/clear":
		ca.ResetSubscribers()
		ca.SetOverride(nil)
		ca.msgCount = 0
		ca.inner = agent.NewCodingAgentWithHarness(agent.Options{Config: cfg, Harness: hr})
		ca.Subscribe(func(evt agentruntime.AgentEvent) {
			ui.Subscriber()(evt)
			chatSubscriber(evt, hr)
		})
		ui.PrintInfo("Conversation cleared.")
		return true, false
	case "/help":
		ui.PrintHelp()
		return true, false
	case "/tools":
		ui.PrintTools()
		return true, false
	case "/tokens":
		ui.PrintTokenUsage(hr)
		return true, false
	case "/session":
		ui.PrintSessionInfo(hr)
		return true, false
	case "/skills":
		ui.PrintSkills(hr.LoadedSkills())
		return true, false
	case "/compact":
		ui.PrintInfo("Compacting context...")
		forceCompact(hr, ca)
		return true, false
	default:
		ui.PrintError("Unknown command: %s (type /help for help)", input)
		return true, false
	}
}

// --- Helpers ---

func stageImages(paths []string, pending *[]core.ContentBlock) (added int, skipped []string) {
	for _, p := range paths {
		p = strings.Trim(p, "\"'")
		if strings.HasPrefix(p, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				// On Windows, ~ expands to C:\Users\name.
				// TrimPrefix removes the leading "~" but leaves any following
				// path separator; filepath.Join handles it correctly.
				suffix := strings.TrimPrefix(p, "~")
				// Ensure we don't end up with a bare "/" prefix on Windows.
				suffix = strings.TrimLeft(suffix, "/\\")
				p = filepath.Join(home, suffix)
			}
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		mime, b64, err := tools.ReadImageFile(abs)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		*pending = append(*pending, core.ImageContent{
			Type:     "image",
			Data:     b64,
			MimeType: mime,
		})
		added++
	}
	return added, skipped
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

func skippedMsg(skipped []string) string {
	if len(skipped) == 0 {
		return ""
	}
	return " (skipped: " + strings.Join(skipped, "; ") + ")"
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
	// Apply compaction: set the override so the next Run uses the
	// compacted message list as its base history.
	ca.SetOverride(newMsgs)
	_ = hr.RecordCompaction(res.Summary, res.TokensBefore)
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
	ca.SetOverride(newMsgs)
	_ = hr.RecordCompaction(res.Summary, res.TokensBefore)
	ui.PrintInfo("✅ Compacted: %d → %d tokens (saved %d)", res.TokensBefore, res.TokensAfter, res.TokensSaved)
}

// chatSubscriber is the agent event listener that accumulates token
// usage from every assistant message.
//
// Only EventMessageEnd and EventMessageUpdate+EventDone are used to
// record usage. EventAgentEnd is skipped because its Messages list
// may overlap with earlier events, causing double-counting.
func chatSubscriber(evt agentruntime.AgentEvent, hr *harness.Harness) {
	switch e := evt.(type) {
	case agentruntime.EventMessageEnd:
		hr.AccumulateUsage(e.Message.Usage)
	case agentruntime.EventMessageUpdate:
		// The Done sub-event carries the final message with Usage.
		if done, ok := e.AssistantEvent.(core.EventDone); ok {
			hr.AccumulateUsage(done.Message.Usage)
		}
	}
}
