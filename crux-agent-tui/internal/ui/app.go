package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"crux-agent-tui/internal/agent"
	"crux-agent-tui/internal/openai"
	"crux-agent-tui/internal/provider"

	tea "github.com/charmbracelet/bubbletea"
)

// ── Mode constants ────────────────────────────────────────────────────────────

type tuiMode int

const (
	modeAuto tuiMode = iota
	modePlan
	modeYOLO
)

// ── Messages ──────────────────────────────────────────────────────────────────

type agentResponseMsg struct {
	err error
}

type agentStreamMsg struct {
	text string
}

type agentToolStartMsg struct {
	name    string
	args    string
	rawArgs string
	toolID  string
}

type agentToolEndMsg struct {
	name   string
	result string
	isErr  bool
	toolID string
}

type agentReasoningMsg struct {
	delta string
}

type elapsedTickMsg struct{}
type clipboardPasteMsg struct {
	text string
	err  error
}

// ── App Model ─────────────────────────────────────────────────────────────────

// App is the main Bubble Tea model for the TUI agent chat.
type App struct {
	chat   *ChatView
	input  *InputView
	dialog *ApprovalDialog
	agent  *agent.Agent
	cfg    *tuiConfig

	width  int
	height int
	ready  bool

	// Agent state
	querying bool
	mode     tuiMode

	// Bubble unsend: the user bubble is echoed immediately on Enter but stays
	// "pending" until the first response packet arrives. Esc before then pops
	// it back off the transcript and restores the input text.
	bubblePending bool
	bubbleRestore string // text to restore on unsend

	// Reasoning tracking
	reasoningAccum strings.Builder
	reasoningOpen  bool // true while we have a "▎ thinking…" marker in chat

	// Tool streaming
	activeToolID string

	// Todo panel state: the latest todo_write tool's args, parsed as JSON.
	todoArgs string

	// Paste support: folded long text pastes
	pastedBlocks []pastedBlock
	nextPasteID  int

	// Esc state machine
	lastEscAt   time.Time
	lastCtrlCAt time.Time

	// Tick for elapsed time during run
	runStart   time.Time
	runElapsed int

	program *tea.Program
	mu      sync.Mutex
}

// SetProgram stores the tea.Program reference.
func (a *App) SetProgram(p *tea.Program) {
	a.program = p
}

// NewApp creates a new TUI agent application.
func NewApp() (*App, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	var llmProvider provider.LLMProvider
	if !cfg.mustUseOpenAIProtocol() {
		return nil, fmt.Errorf("provider %q not supported. Use AI_BASE_URL + OPENAI_API_KEY instead.\n", cfg.ProviderName)
	}
	llmProvider = openai.New()

	agentInst := newAgent(cfg, llmProvider)

	app := &App{
		chat:   NewChatView(),
		input:  NewInputView(),
		dialog: NewApprovalDialog(),
		agent:  agentInst,
		cfg:    cfg,
		mode:   modeAuto,
	}

	agentInst.Subscribe(func(evt agent.AgentEvent) {
		app.handleAgentEvent(evt)
	})

	app.chat.AddMessage(ChatMessage{
		Role:    "system",
		Content: fmt.Sprintf("Crux Agent TUI — %s / %s\nType a message to chat. Commands: /help", cfg.ProviderName, cfg.ModelID),
	})

	return app, nil
}

func newAgent(cfg *tuiConfig, llmProvider provider.LLMProvider) *agent.Agent {
	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = defaultSystemPrompt
	}
	wd, _ := os.Getwd()
	systemPrompt += fmt.Sprintf("\n\nCurrent working directory: %s", wd)
	systemPrompt += fmt.Sprintf("\nCurrent time: %s", time.Now().Format(time.RFC3339))
	systemPrompt += fmt.Sprintf("\nOperating system: %s/%s", runtime.GOOS, runtime.GOARCH)

	state := agent.AgentState{
		Model:        cfg.ModelID,
		BaseURL:      cfg.BaseURL,
		APIKey:       cfg.APIKey,
		SystemPrompt: systemPrompt,
		Tools:        allTools(),
		MaxTokens:    cfg.MaxTokens,
		Headers:      make(map[string]string),
	}
	return agent.New(state, llmProvider, agent.CompactionConfig{MaxTokens: 100000, TokenBudget: 50})
}

// handleAgentEvent routes agent events to the event loop.
func (a *App) handleAgentEvent(evt agent.AgentEvent) {
	switch e := evt.(type) {
	case agent.EventMessageUpdate:
		if e.Type == "reasoning" || e.Type == "thinking" {
			// First response packet → confirm the bubble as sent
			a.confirmBubbleSent()
			a.publish(agentReasoningMsg{delta: e.Delta})
		} else {
			a.confirmBubbleSent()
			a.publish(agentStreamMsg{text: e.Delta})
		}
	case agent.EventToolExecStart:
		a.confirmBubbleSent()
		args := extractPrimaryArg(e.ToolName, e.Args)
		a.publish(agentToolStartMsg{name: e.ToolName, args: args, rawArgs: e.Args, toolID: e.ToolID})
	case agent.EventToolExecEnd:
		a.publish(agentToolEndMsg{name: e.ToolName, result: e.Result, isErr: e.IsError, toolID: e.ToolID})
	case agent.EventTurnEnd:
		if e.ErrorMessage != "" {
			a.publish(agentStreamMsg{text: "\n\nError: " + e.ErrorMessage})
		}
		a.publish(agentResponseMsg{err: nil})
	}
}

func (a *App) publish(msg tea.Msg) {
	if a.program != nil {
		a.program.Send(msg)
	}
}

// confirmBubbleSent marks the pending user bubble as confirmed once the first
// response packet arrives, so Esc no longer un-sends it.
func (a *App) confirmBubbleSent() {
	if a.bubblePending {
		a.bubblePending = false
	}
}

// ── Bubble Tea Interface ──────────────────────────────────────────────────────

func (a *App) Init() tea.Cmd {
	return tea.Batch(
		enableMouse(),
	)
}

func enableMouse() tea.Cmd {
	return tea.EnableMouseCellMotion
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return a.handleWindowSize(msg)
	case tea.KeyMsg:
		return a.handleKeyMsg(msg)
	case tea.MouseMsg:
		return a.handleMouseMsg(msg)
	case agentStreamMsg:
		return a.handleStreamMsg(msg)
	case agentReasoningMsg:
		return a.handleReasoningMsg(msg)
	case agentToolStartMsg:
		return a.handleToolStartMsg(msg)
	case agentToolEndMsg:
		return a.handleToolEndMsg(msg)
	case agentResponseMsg:
		return a.handleResponseMsg(msg)
	case clipboardPasteMsg:
		return a.handlePasteMsg(msg)
	case elapsedTickMsg:
		if a.querying {
			a.runElapsed = int(time.Since(a.runStart).Seconds())
			return a, elapsedTick()
		}
		return a, nil
	}
	return a, nil
}

// ── Window Size ───────────────────────────────────────────────────────────────

func (a *App) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	a.width = msg.Width
	a.height = msg.Height
	a.ready = true

	a.input.SetSize(msg.Width)
	a.dialog.SetSize(msg.Width, msg.Height)
	a.chat.SetSize(msg.Width, msg.Height) // will be recalculated in View()
	return a, nil
}

// ── Message Handlers ──────────────────────────────────────────────────────────

// handleMouseMsg forwards mouse events to the ChatView for scrolling and selection.
func (a *App) handleMouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if a.querying {
		// During query, only allow scrolling, not selection
		switch msg.Type {
		case tea.MouseWheelUp:
			a.chat.ScrollUp(3)
		case tea.MouseWheelDown:
			a.chat.ScrollDown(3)
		}
		return a, nil
	}

	// Handle right-click: copy selected text
	if msg.Type == tea.MouseRight && msg.Action == tea.MouseActionPress {
		if a.chat.HasSelection() {
			text := a.chat.SelectedText()
			if text != "" {
				a.chat.clearSelection()
				return a, copyToClipboard(text)
			}
		}
		return a, nil
	}

	// Forward to ChatView for selection handling
	if a.chat.Update(msg) {
		return a, nil
	}
	return a, nil
}

func (a *App) handleStreamMsg(msg agentStreamMsg) (tea.Model, tea.Cmd) {
	a.closeReasoning()

	if len(a.chat.messages) == 0 || a.chat.messages[len(a.chat.messages)-1].Role != "assistant" {
		a.chat.AddMessage(ChatMessage{Role: "assistant", Content: msg.text})
	} else {
		last := a.chat.messages[len(a.chat.messages)-1].Content
		a.chat.UpdateLastMessage(last + msg.text)
	}
	return a, nil
}

func (a *App) handleReasoningMsg(msg agentReasoningMsg) (tea.Model, tea.Cmd) {
	a.reasoningAccum.WriteString(msg.delta)
	text := a.reasoningAccum.String()
	if len(text) > 300 {
		text = "…" + text[len(text)-300:]
	}

	if !a.reasoningOpen {
		a.chat.AddMessage(ChatMessage{Role: "reasoning", Content: "▎ thinking…"})
		a.reasoningOpen = true
	}

	// Update the reasoning text inline (last message's Content)
	a.chat.UpdateLastMessage(dimLine(clampLine(text, a.width-10)))
	return a, nil
}

func (a *App) closeReasoning() {
	if a.reasoningOpen {
		a.reasoningAccum.Reset()
		a.reasoningOpen = false
	}
}

func (a *App) handleToolStartMsg(msg agentToolStartMsg) (tea.Model, tea.Cmd) {
	a.activeToolID = msg.toolID
	a.chat.AddMessage(ChatMessage{
		Role: "tool_call",
		ToolCall: &ToolCallInfo{
			Name:      msg.name,
			Args:      msg.args,
			RawArgs:   msg.rawArgs,
			Streaming: true,
			StartTime: "0",
		},
	})
	return a, nil
}

func (a *App) handleToolEndMsg(msg agentToolEndMsg) (tea.Model, tea.Cmd) {
	a.activeToolID = ""

	// Check if this is a todo_write tool — capture its args for the todo panel
	if msg.name == "todo_write" {
		a.todoArgs = msg.result
		// The args are in result for the TUI; parse to validate
		if todos := a.parseTodos(); len(todos) == 0 || allTodosDone(todos) {
			a.todoArgs = ""
		}
		// Recalculate layout (todo panel height changed)
		// View() recalculates chat size on each render, no need to do it here.
		return a, nil
	}

	if len(a.chat.messages) > 0 {
		last := a.chat.messages[len(a.chat.messages)-1]
		if last.ToolCall != nil && last.ToolCall.Name == msg.name {
			last.ToolCall.Streaming = false
			last.ToolCall.IsError = msg.isErr
			last.ToolCall.Result = msg.result
			last.ToolCall.Lines = strings.Count(msg.result, "\n")
			a.chat.ReplaceLastMessage(last)
			return a, nil
		}
	}
	label := "[ok]"
	if msg.isErr {
		label = "[err]"
	}
	a.chat.AddMessage(ChatMessage{Role: "system", Content: fmt.Sprintf("%s %s", label, msg.name)})
	return a, nil
}

func (a *App) handleResponseMsg(msg agentResponseMsg) (tea.Model, tea.Cmd) {
	a.querying = false
	a.bubblePending = false
	a.runElapsed = 0
	a.input.Enable()
	a.activeToolID = ""
	if msg.err != nil {
		a.chat.AddMessage(ChatMessage{Role: "error", Content: msg.err.Error()})
	}
	return a, nil
}

// ── Paste Support ─────────────────────────────────────────────────────────────

type pastedBlock struct {
	label string
	text  string
}

const foldedPasteMinChars = 1000
const foldedPasteMinLines = 5

func shouldFoldPastedText(s string) bool {
	return len([]rune(s)) >= foldedPasteMinChars || strings.Count(s, "\n")+1 >= foldedPasteMinLines
}

func pastedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"), "\n") + 1
}

func foldedPasteLabel(id, lines int) string {
	return fmt.Sprintf("[Pasted text #%d · %d lines]", id, lines)
}

func renderFoldedPasteBlock(block pastedBlock) string {
	return fmt.Sprintf("%s\n\n--- Begin %s ---\n%s\n--- End %s ---", block.label, block.label, block.text, block.label)
}

func (a *App) handlePasteMsg(msg clipboardPasteMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil || msg.text == "" {
		return a, nil
	}
	if shouldFoldPastedText(msg.text) {
		label := foldedPasteLabel(a.nextPasteID, pastedLineCount(msg.text))
		a.nextPasteID++
		a.pastedBlocks = append(a.pastedBlocks, pastedBlock{label: label, text: msg.text})
		a.input.InsertAtCursor(label + " ")
	} else {
		a.input.InsertAtCursor(msg.text)
	}
	return a, nil
}

func pasteFromClipboard() tea.Cmd {
	return func() tea.Msg {
		// Try reading clipboard via the platform tool
		text, err := readClipboard()
		if err != nil {
			return clipboardPasteMsg{err: err}
		}
		return clipboardPasteMsg{text: text}
	}
}

// readClipboard attempts to read text from the clipboard using platform tools.
// This is a best-effort implementation that tries PowerShell on Windows.
func readClipboard() (string, error) {
	// Use PowerShell Get-Clipboard on Windows
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Get-Clipboard")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("clipboard: %w", err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// copyToClipboard writes text to the clipboard using platform tools.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		// Use PowerShell Set-Clipboard on Windows
		cmd := exec.Command("powershell", "-NoProfile", "-Command", "Set-Clipboard")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			// Fallback: try clip.exe
			cmd2 := exec.Command("clip")
			cmd2.Stdin = strings.NewReader(text)
			_ = cmd2.Run()
		}
		return nil // no message needed; copy is fire-and-forget
	}
}

// ── Keyboard ──────────────────────────────────────────────────────────────────

func (a *App) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ks := msg.String()

	if a.dialog.Visible() {
		return a.handleApprovalKey(ks)
	}

	switch ks {
	case "pgup", "pgdown", "home", "end":
		a.chat.Update(msg)
		return a, nil
	}

	switch ks {
	case "ctrl+c":
		// If there's a text selection, copy it to clipboard instead of abort/quit
		if a.chat.HasSelection() {
			text := a.chat.SelectedText()
			if text != "" {
				a.chat.clearSelection()
				return a, copyToClipboard(text)
			}
		}
		if a.querying {
			a.agent.Abort()
			a.chat.AddMessage(ChatMessage{Role: "system", Content: "Aborted"})
			a.querying = false
			a.bubblePending = false
			a.input.Enable()
			a.reasoningAccum.Reset()
			a.reasoningOpen = false
			return a, nil
		}
		// Idle: double Ctrl+C quits
		if strings.TrimSpace(a.input.Value()) != "" {
			a.input.ClearInput()
			a.lastCtrlCAt = time.Time{}
			return a, nil
		}
		if !a.lastCtrlCAt.IsZero() && time.Since(a.lastCtrlCAt) < 1500*time.Millisecond {
			return a, tea.Quit
		}
		a.lastCtrlCAt = time.Now()
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Press Ctrl+C again to quit"})
		return a, nil

	case "ctrl+d":
		return a, tea.Quit

	case "ctrl+l":
		a.chat.Clear()
		a.reasoningAccum.Reset()
		a.reasoningOpen = false
		return a, nil

	case "ctrl+v", "ctrl+shift+v":
		if a.querying {
			return a, nil
		}
		return a, pasteFromClipboard()

	case "shift+tab":
		return a.cycleMode()

	case "esc":
		return a.handleEsc()
	}

	if a.querying {
		return a, nil
	}

	switch ks {
	case "enter":
		return a.handleEnter()

	case "alt+enter", "ctrl+j", "shift+enter":
		a.input.SetAltEnter()
		a.input.AppendRune('\n')
		return a, nil

	case "up":
		if a.input.CompletionActive() {
			a.input.MoveCompletion(-1)
			return a, nil
		}
		a.input.RecallPrevious()
		return a, nil

	case "down":
		if a.input.CompletionActive() {
			a.input.MoveCompletion(1)
			return a, nil
		}
		a.input.RecallNext()
		return a, nil

	case "left":
		a.input.CursorLeft()
		a.input.ResetHistoryRecall()
		return a, nil

	case "right":
		a.input.CursorRight()
		a.input.ResetHistoryRecall()
		return a, nil

	case "ctrl+left":
		a.input.CursorWordLeft()
		a.input.ResetHistoryRecall()
		return a, nil

	case "ctrl+right":
		a.input.CursorWordRight()
		a.input.ResetHistoryRecall()
		return a, nil

	case "home":
		if a.querying {
			return a, nil
		}
		a.input.CursorHome()
		return a, nil

	case "end":
		if a.querying {
			return a, nil
		}
		a.input.CursorEnd()
		return a, nil

	case "backspace":
		a.input.DeleteLast()
		a.input.ResetHistoryRecall()
		return a, nil

	case "delete":
		a.input.DeleteForward()
		a.input.ResetHistoryRecall()
		return a, nil

	case "ctrl+w":
		a.input.DeleteWordBackward()
		a.input.ResetHistoryRecall()
		return a, nil

	case "ctrl+u":
		a.input.ClearInput()
		a.input.ResetHistoryRecall()
		return a, nil

	case "tab":
		if a.input.CompletionActive() {
			accepted := a.input.AcceptCompletion()
			// If this was an @ file directory, refresh completion with new directory content
			if accepted && a.input.CompletionType() == 2 {
				a.updateCompletion()
			}
			return a, nil
		}
		return a, nil
	}

	if len(msg.Runes) > 0 {
		a.input.ResetHistoryRecall()
		for _, r := range msg.Runes {
			a.input.AppendRune(r)
		}
		a.updateCompletion()
		return a, nil
	}

	return a, nil
}

// handleEsc: Bubble unsend → Cancel → Exit YOLO → Exit Plan → Clear input → Double-Esc
func (a *App) handleEsc() (tea.Model, tea.Cmd) {
	if a.querying && a.bubblePending {
		// Unsend: restore input, pop bubble off transcript, cancel request
		a.input.SetValue(a.bubbleRestore)
		a.chat.PopLastMessage()
		a.querying = false
		a.bubblePending = false
		a.input.Enable()
		a.reasoningAccum.Reset()
		a.reasoningOpen = false
		a.agent.Abort()
		return a, nil
	}

	if a.querying {
		a.agent.Abort()
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Canceled"})
		a.querying = false
		a.bubblePending = false
		a.input.Enable()
		a.reasoningAccum.Reset()
		a.reasoningOpen = false
		return a, nil
	}

	if a.mode == modeYOLO {
		a.mode = modeAuto
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Mode: Auto"})
		return a, nil
	}
	if a.mode == modePlan {
		a.mode = modeAuto
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Mode: Auto"})
		return a, nil
	}

	if strings.TrimSpace(a.input.Value()) != "" {
		a.input.ClearInput()
		return a, nil
	}

	if !a.lastEscAt.IsZero() && time.Since(a.lastEscAt) < 600*time.Millisecond {
		a.lastEscAt = time.Time{}
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Double Esc — rewind (placeholder)"})
		return a, nil
	}
	a.lastEscAt = time.Now()
	return a, nil
}

func (a *App) cycleMode() (tea.Model, tea.Cmd) {
	switch a.mode {
	case modeAuto:
		a.mode = modePlan
	case modePlan:
		a.mode = modeYOLO
	case modeYOLO:
		a.mode = modeAuto
	}
	return a, nil
}

func (a *App) handleApprovalKey(ks string) (tea.Model, tea.Cmd) {
	switch ks {
	case "y", "Y":
		a.dialog.Hide()
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Approved"})
		a.input.Enable()
		return a, nil
	case "a", "A":
		a.dialog.Hide()
		a.chat.AddMessage(ChatMessage{Role: "system", Content: fmt.Sprintf("'%s' always allowed", a.dialog.ToolName())})
		a.input.Enable()
		return a, nil
	case "n", "N", "esc":
		a.dialog.Hide()
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Denied"})
		a.input.Enable()
		return a, nil
	}
	return a, nil
}

func (a *App) handleEnter() (tea.Model, tea.Cmd) {
	if a.querying {
		return a, nil
	}

	raw := a.input.Value()
	val := strings.TrimSpace(raw)
	if val == "" {
		return a, nil
	}

	a.input.SaveToHistory(val)
	a.input.ClearInput()

	if strings.HasPrefix(val, "/") {
		return a.handleCommand(val)
	}

	a.querying = true
	a.input.Disable()
	a.runStart = time.Now()
	a.runElapsed = 0

	// Echo the user bubble immediately (bubble unsend pattern)
	a.chat.AddMessage(ChatMessage{Role: "user", Content: val})
	a.bubblePending = true
	a.bubbleRestore = val

	return a, tea.Batch(a.runAgent(val), elapsedTick())
}

// ── Commands ──────────────────────────────────────────────────────────────────

func (a *App) handleCommand(cmd string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(cmd)
	base := parts[0]

	switch base {
	case "/quit", "/exit":
		return a, tea.Quit

	case "/clear":
		a.chat.Clear()
		a.reasoningAccum.Reset()
		a.reasoningOpen = false
		a.todoArgs = ""
		a.agent.Reset()
		llmProvider := openai.New()
		a.agent = newAgent(a.cfg, llmProvider)
		a.agent.Subscribe(func(evt agent.AgentEvent) { a.handleAgentEvent(evt) })
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Conversation cleared"})
		return a, nil

	case "/help":
		helpText := `Commands:
  /help            Show help
  /clear           Clear history
  /model           Show model info
  /compact         Compact context
  /new             New session
  /todo            Dismiss todo list
  /quit, /exit     Exit

Keys:
  Enter            Send
  Alt+Enter        New line
  ↑/↓              History
  Ctrl+C           Abort (running) / Clear (idle) / Quit (double)
  Ctrl+L           Clear screen
  Shift+Tab        Cycle mode (Auto→Plan→YOLO)
  Esc              Unsend → Cancel → Exit mode → Clear input
  PgUp/PgDn        Scroll`
		a.chat.AddMessage(ChatMessage{Role: "system", Content: helpText})
		return a, nil

	case "/model":
		a.chat.AddMessage(ChatMessage{Role: "system",
			Content: fmt.Sprintf("Model: %s\nProvider: %s\nMax Tokens: %d", a.cfg.ModelID, a.cfg.ProviderName, a.cfg.MaxTokens)})
		return a, nil

	case "/compact":
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Compacting context..."})
		a.agent.Compact()
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Context compacted"})
		return a, nil

	case "/todo":
		a.todoArgs = ""
		return a, nil

	case "/new":
		a.chat.Clear()
		a.reasoningAccum.Reset()
		a.reasoningOpen = false
		a.todoArgs = ""
		a.agent.Reset()
		llmProvider := openai.New()
		a.agent = newAgent(a.cfg, llmProvider)
		a.agent.Subscribe(func(evt agent.AgentEvent) { a.handleAgentEvent(evt) })
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "New session started"})
		return a, nil

	default:
		a.chat.AddMessage(ChatMessage{Role: "error", Content: fmt.Sprintf("Unknown: %s. Try /help", cmd)})
		return a, nil
	}
}

// ── Agent Runner ──────────────────────────────────────────────────────────────

func (a *App) runAgent(prompt string) tea.Cmd {
	return func() tea.Msg {
		timeout := a.cfg.QueryTimeout
		if timeout <= 0 {
			timeout = 10 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_, err := a.agent.Run(ctx, prompt)
		if err != nil {
			return agentResponseMsg{err: err}
		}
		return agentResponseMsg{err: nil}
	}
}

func elapsedTick() tea.Cmd {
	return tea.Tick(1*time.Second, func(time.Time) tea.Msg { return elapsedTickMsg{} })
}

// ── Completion ────────────────────────────────────────────────────────────────

func (a *App) updateCompletion() {
	val := a.input.Value()

	// Detect @ file path completion
	if ci := a.shouldCompleteFilePath(); ci != "" {
		a.updateFileCompletion(ci)
		return
	}

	// Detect / command completion
	if strings.HasPrefix(val, "/") {
		knownCommands := []CompletionItem{
			{Label: "/help", Insert: "/help", Hint: "Show help"},
			{Label: "/clear", Insert: "/clear", Hint: "Clear history"},
			{Label: "/model", Insert: "/model", Hint: "Show model info"},
			{Label: "/compact", Insert: "/compact", Hint: "Compact context"},
			{Label: "/new", Insert: "/new", Hint: "New session"},
			{Label: "/todo", Insert: "/todo", Hint: "Dismiss todo list"},
			{Label: "/quit", Insert: "/quit", Hint: "Exit"},
		}

		prefix := strings.ToLower(val)
		var matches []CompletionItem
		for _, item := range knownCommands {
			if strings.HasPrefix(strings.ToLower(item.Label), prefix) {
				matches = append(matches, item)
			}
		}
		a.input.SetCompletionItems(matches, 1)
		return
	}

	// No completion pattern detected
	a.input.DismissCompletion()
}

// shouldCompleteFilePath checks if the cursor is at an @ word boundary.
// Returns the partial path after @ (e.g. "src/" for "@src/"), or empty.
func (a *App) shouldCompleteFilePath() string {
	val := a.input.Value()
	if a.input.Cursor() == 0 {
		return ""
	}
	// Find the last @ before the cursor
	runes := []rune(val)
	cursor := a.input.Cursor()
	if cursor > len(runes) {
		cursor = len(runes)
	}
	// Scan backwards from cursor to find @ at word boundary
	for i := cursor - 1; i >= 0; i-- {
		if runes[i] == '@' {
			// Check word boundary: start of string or preceded by space
			if i == 0 || runes[i-1] == ' ' {
				pathPart := strings.TrimSpace(string(runes[i+1 : cursor]))
				return pathPart
			}
			return ""
		}
		if runes[i] == ' ' {
			// Hit a space before @ — not a valid @ trigger
			return ""
		}
	}
	return ""
}

// updateFileCompletion scans the filesystem for path completion.
func (a *App) updateFileCompletion(partial string) {
	// Get the working directory for the base path
	wd, err := os.Getwd()
	if err != nil {
		a.input.DismissCompletion()
		return
	}

	// Determine the directory to scan and the prefix filter
	searchDir := wd
	filterPrefix := partial

	if partial != "" {
		// Separate directory part and file prefix
		dirPart := filepath.Dir(partial)
		filePrefix := filepath.Base(partial)

		if filepath.IsAbs(partial) {
			searchDir = dirPart
		} else if dirPart != "." && dirPart != "" {
			searchDir = filepath.Join(wd, dirPart)
		}
		// Treat "." and ".." as empty prefix (show everything)
		if filePrefix == "." || filePrefix == ".." {
			filterPrefix = ""
		} else {
			filterPrefix = filePrefix
		}
	}

	entries, err := os.ReadDir(searchDir)
	if err != nil {
		a.input.DismissCompletion()
		return
	}

	var items []CompletionItem
	for _, entry := range entries {
		name := entry.Name()
		// Skip hidden files and directories
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Apply prefix filter
		if filterPrefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(filterPrefix)) {
			continue
		}

		isDir := entry.IsDir()

		// Build the full relative path for insertion
		var insertPath string
		if partial == "" {
			insertPath = "@" + name
		} else {
			// Replace the last path component
			dirPart := filepath.Dir(partial)
			if dirPart == "." || dirPart == "" {
				insertPath = "@" + name
			} else {
				insertPath = "@" + filepath.ToSlash(filepath.Join(dirPart, name))
			}
		}

		hint := ""
		if isDir {
			hint = "dir"
		} else {
			// Add file extension as hint
			if ext := filepath.Ext(name); ext != "" {
				hint = ext
			}
		}

		items = append(items, CompletionItem{
			Label:       name,
			Insert:      insertPath,
			Hint:        hint,
			IsDirectory: isDir,
		})
	}

	// Sort: directories first, then files, alphabetically within each group
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDirectory != items[j].IsDirectory {
			return items[i].IsDirectory // directories first
		}
		return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label)
	})

	// Limit to 50 items
	if len(items) > 50 {
		items = items[:50]
	}

	a.input.SetCompletionItems(items, 2)
}

// ── Todo Panel ────────────────────────────────────────────────────────────────

type todoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm"`
	Level      int    `json:"level"`
}

type todoPayload struct {
	Todos []todoItem `json:"todos"`
}

func (a *App) parseTodos() []todoItem {
	var p todoPayload
	if err := json.Unmarshal([]byte(a.todoArgs), &p); err != nil {
		return nil
	}
	return p.Todos
}

func allTodosDone(todos []todoItem) bool {
	for _, t := range todos {
		if t.Status != "completed" {
			return false
		}
	}
	return true
}

func (a *App) renderTodoPanel() string {
	todos := a.parseTodos()
	if len(todos) == 0 {
		return ""
	}
	done := 0
	for _, t := range todos {
		if t.Status == "completed" {
			done++
		}
	}
	if done == len(todos) {
		a.todoArgs = ""
		return ""
	}

	var b strings.Builder
	header := todoHeaderStyle.Render(fmt.Sprintf("To-dos %d/%d", done, len(todos)))
	b.WriteString(header)
	b.WriteString("\n")

	shown := 0
	maxShow := 8

	for _, t := range todos {
		if shown >= maxShow {
			b.WriteString(todoDimStyle.Render(fmt.Sprintf("  +%d more", len(todos)-shown)) + "\n")
			break
		}
		shown++
		indent := "  "
		if t.Level >= 1 {
			indent = "      "
		}
		switch t.Status {
		case "completed":
			b.WriteString(indent + todoGreenStyle.Render("✔") + " " + todoDimStyle.Render(t.Content) + "\n")
		case "in_progress":
			label := t.Content
			if t.ActiveForm != "" {
				label = t.ActiveForm
			}
			b.WriteString(indent + todoYellowStyle.Render("▶") + " " + todoYellowStyle.Render(label) + "\n")
		default:
			b.WriteString(indent + todoDimStyle.Render("○ "+t.Content) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ── View ──────────────────────────────────────────────────────────────────────

func (a *App) View() string {
	if !a.ready {
		return "Initializing..."
	}

	boxW := a.width
	if boxW < 10 {
		boxW = 10
	}

	// Title + separator (2 rows)
	title := TitleStyle.Render("Crux Agent TUI")
	sep := separatorStyle.Render(strings.Repeat("─", boxW))

	// Mode pill
	var modeTag string
	switch a.mode {
	case modeAuto:
		modeTag = PillAutoStyle.Render("Auto")
	case modePlan:
		modeTag = PillPlanStyle.Render("Plan")
	case modeYOLO:
		modeTag = PillYOLOStyle.Render("YOLO")
	}

	// Status line
	var statusText string
	switch {
	case a.querying && a.bubblePending:
		statusText = fmt.Sprintf("%s · sending…", modeTag)
	case a.querying:
		sp := spinnerFrame(a.runElapsed)
		statusText = fmt.Sprintf("%s · %s thinking · %ds", modeTag, sp, a.runElapsed)
	default:
		statusText = fmt.Sprintf("%s · ready (%s)", modeTag, dimLine("Shift+Tab to cycle, double Ctrl+C to quit"))
	}

	// Data line (model, context usage, messages, etc.)
	modelInfo := fmt.Sprintf("%s · %s", dimLine(a.cfg.ProviderName), dimLine(a.cfg.ModelID))

	// Context usage
	ctxInfo := a.agent.ContextInfo()
	msgCount := a.agent.MessageCount()
	if ctxInfo != "" {
		modelInfo += " · " + dimLine(ctxInfo)
	}
	if msgCount > 0 {
		modelInfo += " · " + dimLine(fmt.Sprintf("%d msgs", msgCount))
	}

	modeInfo := ""
	switch a.mode {
	case modePlan:
		modeInfo = dimLine(" · plan: read-only")
	case modeYOLO:
		modeInfo = modeYOLOInfoStyle.Render(" · approvals: skipped")
	}

	// Working spinner above input
	var workingLine string
	if a.querying {
		workingLine = workingStyle.Render(fmt.Sprintf(" %s agent working…", spinnerFrame(a.runElapsed)))
	}

	// Todo panel (pinned above input)
	todoPanel := a.renderTodoPanel()

	// Completion menu (above input box)
	completionMenu := a.input.RenderCompletionMenu()

	// Input
	inputBox := a.input.View()

	// Layout: compute row positions so ChatView knows its Y offset for mouse
	// Row 0: title
	// Row 1: separator
	// Row 2...: ChatView (variable height)
	// After chat: workingLine (1), todoPanel (variable), completionMenu (N), inputBox (N rows), status (1), data (1)
	bottomRows := 0
	if workingLine != "" {
		bottomRows++ // working line
	}
	if todoPanel != "" {
		bottomRows += strings.Count(todoPanel, "\n") + 1
	}
	if completionMenu != "" {
		bottomRows += strings.Count(completionMenu, "\n") + 1
	}
	bottomRows += a.input.VisibleRows() // input box (dynamic height)
	bottomRows += 2                     // status + data lines

	chatHeight := a.height - 2 - bottomRows // 2 = title + separator
	if chatHeight < 5 {
		chatHeight = 5
	}
	a.chat.SetSize(boxW, chatHeight)

	// Set transcript Y offset for mouse coordinate mapping
	transcriptY := 2 // title (1) + separator (1)
	a.chat.SetTranscriptY(transcriptY)

	// Chat content (now includes scrollbar)
	chatContent := a.chat.View()

	// Build full layout
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(sep)
	b.WriteString("\n")
	b.WriteString(chatContent)
	b.WriteString("\n")

	if workingLine != "" {
		b.WriteString(workingLine)
		b.WriteString("\n")
	}
	if todoPanel != "" {
		b.WriteString(todoPanel)
		b.WriteString("\n")
	}
	if completionMenu != "" {
		b.WriteString(completionMenu)
		b.WriteString("\n")
	}
	b.WriteString(inputBox)
	b.WriteString("\n")
	b.WriteString(clampLine(statusText, boxW))
	b.WriteString("\n")
	b.WriteString(clampLine(modelInfo+modeInfo, boxW))

	return b.String()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func spinnerFrame(elapsed int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return spinnerStyle.Render(frames[elapsed%len(frames)])
}

func extractPrimaryArg(name, rawArgs string) string {
	arg := strings.TrimSpace(rawArgs)
	if len(arg) > 60 {
		arg = arg[:60] + "…"
	}
	return arg
}
