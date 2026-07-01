package ui

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"crux-agent-tui/internal/agent"
	"crux-agent-tui/internal/openai"
	"crux-agent-tui/internal/provider"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Mode constants ────────────────────────────────────────────────────────────

type tuiMode int

const (
	modeAuto tuiMode = iota
	modePlan
	modeYOLO
)

// ── Messages for the Bubble Tea event loop ────────────────────────────────────

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

	// Reasoning tracking
	reasoningAccum strings.Builder

	// Tool streaming
	activeToolID string

	// Esc state machine
	// States: idle -> pending (unsent turn running) -> canceled (turn done, input restores)
	// Esc on empty idle with double-press triggers rewind (future)
	lastEscAt time.Time

	// Tick for elapsed time during run
	runStart   time.Time
	runElapsed int

	program *tea.Program
	mu      sync.Mutex
}

// SetProgram stores the tea.Program reference for sending messages from goroutines.
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
		return nil, fmt.Errorf("provider %q currently not supported. Use an OpenAI-compatible base URL instead.\n"+
			"Set AI_BASE_URL and use OPENAI_API_KEY for the API key.", cfg.ProviderName)
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

	// Welcome message
	app.chat.AddMessage(ChatMessage{
		Role:    "system",
		Content: fmt.Sprintf("Crux Agent TUI — %s / %s\nType a message to chat. Commands: /help", cfg.ProviderName, cfg.ModelID),
	})

	return app, nil
}

// newAgent creates a fresh coding agent from config.
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
	compaction := agent.CompactionConfig{
		MaxTokens:   100000,
		TokenBudget: 50,
	}
	return agent.New(state, llmProvider, compaction)
}

// handleAgentEvent processes agent events and sends them as Bubble Tea messages.
func (a *App) handleAgentEvent(evt agent.AgentEvent) {
	switch e := evt.(type) {
	case agent.EventMessageUpdate:
		if e.Type == "reasoning" || e.Type == "thinking" {
			a.publish(agentReasoningMsg{delta: e.Delta})
		} else {
			a.publish(agentStreamMsg{text: e.Delta})
		}
	case agent.EventToolExecStart:
		// Extract a human-readable primary arg
		args := extractPrimaryArg(e.ToolName, e.Args)
		a.publish(agentToolStartMsg{
			name:    e.ToolName,
			args:    args,
			rawArgs: e.Args,
			toolID:  e.ToolID,
		})
	case agent.EventToolExecEnd:
		a.publish(agentToolEndMsg{
			name:   e.ToolName,
			result: e.Result,
			isErr:  e.IsError,
			toolID: e.ToolID,
		})
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

// ── Bubble Tea Model Interface ────────────────────────────────────────────────

func (a *App) Init() tea.Cmd { return nil }

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return a.handleWindowSize(msg)

	case tea.KeyMsg:
		return a.handleKeyMsg(msg)

	// Agent events
	case agentStreamMsg:
		return a.handleStreamMsg(msg)

	case agentReasoningMsg:
		return a.handleReasoningMsg(msg)

	case agentToolStartMsg:
		return a.handleToolStartMsg(msg)

	case agentToolEndMsg:
		return a.handleToolEndMsg(msg)

	case agentResponseMsg:
		a.querying = false
		a.runElapsed = 0
		a.input.Enable()
		a.reasoningAccum.Reset()
		a.activeToolID = ""
		if msg.err != nil {
			a.chat.AddMessage(ChatMessage{Role: "error", Content: msg.err.Error()})
		}
		return a, nil

	case elapsedTickMsg:
		if a.querying {
			a.runElapsed = int(time.Since(a.runStart).Seconds())
			return a, elapsedTick()
		}
		return a, nil
	}

	return a, nil
}

// ── Message handlers ──────────────────────────────────────────────────────────

func (a *App) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	a.width = msg.Width
	a.height = msg.Height
	a.ready = true

	// Layout: chat area takes most, input + status + mode rows at the bottom
	bottomRows := a.bottomRows()
	chatHeight := msg.Height - bottomRows
	if chatHeight < 5 {
		chatHeight = 5
	}
	a.chat.SetSize(msg.Width, chatHeight)
	a.input.SetSize(msg.Width)
	a.dialog.SetSize(msg.Width, msg.Height)
	return a, nil
}

// bottomRows returns the number of terminal rows occupied by the bottom region:
//   - status line (1)
//   - data line (1)
//   - working spinner line when running (1 or 0)
//   - input box with border (1) = 3 rows total, or 4 when running
func (a *App) bottomRows() int {
	rows := 3 // status line + data line + input box
	if a.querying {
		rows++ // working spinner line
	}
	return rows
}

func (a *App) handleStreamMsg(msg agentStreamMsg) (tea.Model, tea.Cmd) {
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

	// Show reasoning in a dedicated reasoning message
	text := a.reasoningAccum.String()
	// Bounded view: only show the last ~200 chars live
	if len(text) > 200 {
		text = "…" + text[len(text)-200:]
	}

	if len(a.chat.messages) == 0 || a.chat.messages[len(a.chat.messages)-1].Role != "reasoning" {
		a.chat.AddMessage(ChatMessage{Role: "reasoning", Content: "▎ thinking…"})
		a.chat.AddMessage(ChatMessage{
			Role:    "reasoning",
			Content: dimLine(clampLine(text, a.width-10)),
		})
	} else {
		// Replace the last line of the reasoning block
		vis := a.reasoningAccum.String()
		if len(vis) > 200 {
			vis = "…" + vis[len(vis)-200:]
		}
		a.chat.UpdateLastMessage(dimLine(clampLine(vis, a.width-10)))
	}
	return a, nil
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
	// Update the last tool_call message to finalized
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
	// Fallback: add as text
	label := "[ok]"
	if msg.isErr {
		label = "[err]"
	}
	a.chat.AddMessage(ChatMessage{
		Role:    "system",
		Content: fmt.Sprintf("%s %s", label, msg.name),
	})
	return a, nil
}

// ── Keyboard handling with Esc state machine ──────────────────────────────────

func (a *App) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ks := msg.String()

	// Approval dialog is modal
	if a.dialog.Visible() {
		return a.handleApprovalKey(ks)
	}

	// Scroll keys work in any state
	switch ks {
	case "pgup", "pgdown", "home", "end":
		a.chat.Update(msg)
		return a, nil
	}

	// Global shortcuts
	switch ks {
	case "ctrl+c":
		if a.querying {
			a.agent.Abort()
			a.chat.AddMessage(ChatMessage{Role: "system", Content: "Aborted"})
			a.querying = false
			a.input.Enable()
			a.reasoningAccum.Reset()
			return a, nil
		}
		return a, tea.Quit

	case "ctrl+d":
		return a, tea.Quit

	case "ctrl+l":
		a.chat.Clear()
		a.reasoningAccum.Reset()
		return a, nil

	case "ctrl+o":
		// Toggle reasoning view (placeholder — just ack)
		return a, nil

	case "shift+tab":
		return a.cycleMode()

	case "esc":
		return a.handleEsc()
	}

	// While running, most keys are ignored (except those above)
	if a.querying {
		return a, nil
	}

	// Input handling
	switch ks {
	case "enter":
		return a.handleEnter()

	case "alt+enter", "ctrl+j", "shift+enter":
		a.input.SetAltEnter()
		a.input.AppendRune('\n')
		return a, nil

	case "up":
		a.input.RecallPrevious()
		return a, nil

	case "down":
		a.input.RecallNext()
		return a, nil

	case "backspace":
		a.input.DeleteLast()
		a.input.ResetHistoryRecall()
		return a, nil

	case "tab":
		if a.input.CompletionActive() && len(a.input.CompletionItems()) > 0 {
			a.input.AcceptCompletion()
			return a, nil
		}
		return a, nil
	}

	// Rune input
	if len(msg.Runes) > 0 {
		a.input.ResetHistoryRecall()
		for _, r := range msg.Runes {
			a.input.AppendRune(r)
		}
		// Update completion if typing /
		if strings.HasPrefix(a.input.Value(), "/") {
			a.updateCompletion()
		}
		return a, nil
	}

	return a, nil
}

// handleEsc implements Reasonix-style Esc state machine:
//   - Running + pending bubble: unsend (restore text)
//   - Running: cancel turn
//   - YOLO mode: exit YOLO
//   - Plan mode: exit Plan
//   - Idle, empty input, double-Esc within 600ms: open rewind (future: placeholder)
//   - Idle, non-empty: clear input
func (a *App) handleEsc() (tea.Model, tea.Cmd) {
	if a.querying {
		a.agent.Abort()
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Canceled"})
		a.querying = false
		a.input.Enable()
		a.reasoningAccum.Reset()
		return a, nil
	}

	if a.mode == modeYOLO {
		// Back out of YOLO
		a.mode = modeAuto
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Mode: Auto"})
		return a, nil
	}
	if a.mode == modePlan {
		a.mode = modeAuto
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Mode: Auto"})
		return a, nil
	}

	// Idle: clear input
	if strings.TrimSpace(a.input.Value()) != "" {
		a.input.ClearInput()
		return a, nil
	}

	// Double-Esc on empty input
	if !a.lastEscAt.IsZero() && time.Since(a.lastEscAt) < 600*time.Millisecond {
		a.lastEscAt = time.Time{}
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Double Esc — rewind (placeholder)"})
		return a, nil
	}
	a.lastEscAt = time.Now()
	return a, nil
}

// cycleMode cycles Auto → Plan → YOLO → Auto, like Shift+Tab in Reasonix.
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

// handleApprovalKey handles keys in the approval dialog.
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

// handleEnter processes the Enter key: submits input or handles Alt+Enter.
func (a *App) handleEnter() (tea.Model, tea.Cmd) {
	if a.querying {
		return a, nil
	}

	val := strings.TrimSpace(a.input.Value())
	if val == "" {
		return a, nil
	}

	// Save to history
	a.input.SaveToHistory(val)

	// Handle /commands
	if strings.HasPrefix(val, "/") {
		a.input.ClearInput()
		return a.handleCommand(val)
	}

	a.input.ClearInput()
	a.querying = true
	a.input.Disable()
	a.runStart = time.Now()
	a.runElapsed = 0
	a.reasoningAccum.Reset()

	a.chat.AddMessage(ChatMessage{Role: "user", Content: val})
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
		a.agent.Reset()
		llmProvider := openai.New()
		a.agent = newAgent(a.cfg, llmProvider)
		a.agent.Subscribe(func(evt agent.AgentEvent) {
			a.handleAgentEvent(evt)
		})
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Conversation cleared"})
		return a, nil

	case "/help":
		helpText := `Commands:
  /help            Show help
  /clear           Clear history
  /model           Show model info
  /compact         Compact context
  /new             New session
  /quit, /exit     Exit

Keys:
  Enter            Send
  Alt+Enter        New line
  ↑/↓              History
  Ctrl+C           Abort/Quit
  Ctrl+L           Clear
  Shift+Tab        Cycle mode (Auto→Plan→YOLO)
  Esc              Back out (5 levels)
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

	case "/new":
		a.chat.Clear()
		a.reasoningAccum.Reset()
		a.agent.Reset()
		llmProvider := openai.New()
		a.agent = newAgent(a.cfg, llmProvider)
		a.agent.Subscribe(func(evt agent.AgentEvent) {
			a.handleAgentEvent(evt)
		})
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "New session started"})
		return a, nil

	default:
		a.chat.AddMessage(ChatMessage{Role: "error", Content: fmt.Sprintf("Unknown: %s. Try /help", cmd)})
		return a, nil
	}
}

// ── Agent runner ──────────────────────────────────────────────────────────────

func (a *App) runAgent(prompt string) tea.Cmd {
	return func() tea.Msg {
		queryTimeout := a.cfg.QueryTimeout
		if queryTimeout <= 0 {
			queryTimeout = 10 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
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
	if !strings.HasPrefix(val, "/") {
		a.input.DismissCompletion()
		return
	}

	knownCommands := []CompletionItem{
		{Label: "/help", Insert: "/help", Hint: "Show help"},
		{Label: "/clear", Insert: "/clear", Hint: "Clear history"},
		{Label: "/model", Insert: "/model", Hint: "Show model info"},
		{Label: "/compact", Insert: "/compact", Hint: "Compact context"},
		{Label: "/new", Insert: "/new", Hint: "New session"},
		{Label: "/quit", Insert: "/quit", Hint: "Exit"},
	}

	prefix := strings.ToLower(val)
	var matches []CompletionItem
	for _, item := range knownCommands {
		if strings.HasPrefix(strings.ToLower(item.Label), prefix) {
			matches = append(matches, item)
		}
	}
	a.input.SetCompletionItems(matches)
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

	// Title bar
	title := TitleStyle.Render("Crux Agent TUI")
	sep := lipgloss.NewStyle().
		Foreground(ColorBorder).
		Render(strings.Repeat("─", boxW))

	// Mode tag (pill)
	var modeTag string
	switch a.mode {
	case modeAuto:
		modeTag = PillAutoStyle.Render("Auto")
	case modePlan:
		modeTag = PillPlanStyle.Render("Plan")
	case modeYOLO:
		modeTag = PillYOLOStyle.Render("YOLO")
	}

	// Status line (row 1): mode pill + state + hints
	var statusText string
	switch {
	case a.querying:
		spinner := spinnerFrame(a.runElapsed)
		statusText = fmt.Sprintf("%s · %s thinking · %ds", modeTag, spinner, a.runElapsed)
	default:
		statusText = fmt.Sprintf("%s · ready (%s)", modeTag, dimLine("Shift+Tab to cycle"))
	}

	// Data line (row 2): model info
	modelInfo := fmt.Sprintf("%s · %s", dimLine(a.cfg.ProviderName), dimLine(a.cfg.ModelID))
	modeInfo := ""
	switch a.mode {
	case modePlan:
		modeInfo = dimLine(" · plan mode: read-only")
	case modeYOLO:
		modeInfo = lipgloss.NewStyle().Foreground(ColorDanger).Render(" · approvals: skipped")
	}

	// Working spinner line (shown above input when running)
	var workingLine string
	if a.querying {
		workingLine = lipgloss.NewStyle().Foreground(ColorMuted).Padding(0, 1).
			Render(fmt.Sprintf(" %s agent working…", spinnerFrame(a.runElapsed)))
	}

	// Input box
	inputBox := a.input.View()

	// Chat area
	chatContent := a.chat.View()

	// Build full layout
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(sep)
	b.WriteString("\n")
	b.WriteString(chatContent)
	b.WriteString("\n")

	// Bottom region: working line + input + status line + data line
	if workingLine != "" {
		b.WriteString(workingLine)
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

// spinnerFrame returns a simple spinner character based on elapsed time.
func spinnerFrame(elapsed int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return lipgloss.NewStyle().Foreground(ColorAccent).Render(frames[elapsed%len(frames)])
}

// extractPrimaryArg extracts a human-readable primary argument from a tool call.
func extractPrimaryArg(name, rawArgs string) string {
	// Simple extraction — just use the raw args trimmed
	arg := strings.TrimSpace(rawArgs)
	if len(arg) > 60 {
		arg = arg[:60] + "…"
	}
	return arg
}
