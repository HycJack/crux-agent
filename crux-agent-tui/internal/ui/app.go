package ui

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "github.com/hycjack/crux-ai/providers"

	agentruntime "crux-agent-runtime/agent"

	"github.com/hycjack/crux-ai/core"

	tea "github.com/charmbracelet/bubbletea"
)

// ─────────────────────────────────────────────────────────────────────────────
// Messages for the Bubble Tea event loop
// ─────────────────────────────────────────────────────────────────────────────

type agentResponseMsg struct {
	err error
}

type agentStreamMsg struct {
	text string
}

type agentToolStartMsg struct {
	name string
	args string
}

type agentToolEndMsg struct {
	name   string
	result string
	isErr  bool
}

type agentThinkStartMsg struct{}

type agentThinkDeltaMsg struct {
	text string
}

// ─────────────────────────────────────────────────────────────────────────────
// App Model
// ─────────────────────────────────────────────────────────────────────────────

// App is the main Bubble Tea model for the TUI agent chat.
type App struct {
	chat   *ChatView
	input  *InputView
	dialog *ApprovalDialog
	agent  *agentruntime.Agent
	cfg    *tuiConfig

	width         int
	height        int
	ready         bool
	querying      bool
	showHelp      bool
	showApproval  bool
	pendingImages int

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

	agent := newAgent(cfg)

	app := &App{
		chat:   NewChatView(),
		input:  NewInputView(),
		dialog: NewApprovalDialog(),
		agent:  agent,
		cfg:    cfg,
	}

	// Subscribe to agent events for real-time streaming
	agent.Subscribe(func(evt agentruntime.AgentEvent) {
		app.handleAgentEvent(evt)
	})

	// Welcome
	app.chat.AddMessage(ChatMessage{
		Role:    "system",
		Content: fmt.Sprintf("Crux Agent TUI — %s / %s\n\nType a message and press Enter to chat. Commands: /help", cfg.Provider, cfg.ModelID),
	})

	return app, nil
}

// newAgent creates a fresh coding agent from config.
func newAgent(cfg *tuiConfig) *agentruntime.Agent {
	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = defaultSystemPrompt
	}
	wd, _ := os.Getwd()
	systemPrompt += fmt.Sprintf("\n\nCurrent working directory: %s", wd)
	systemPrompt += fmt.Sprintf("\nCurrent time: %s", time.Now().Format(time.RFC3339))
	systemPrompt += fmt.Sprintf("\nOperating system: %s/%s", runtime.GOOS, runtime.GOARCH)

	model := cfg.getModel()
	model.Headers = make(map[string]string)

	state := &agentruntime.AgentState{
		Model:        model,
		SystemPrompt: systemPrompt,
		Tools:        allTools(),
		GetApiKey: func() string {
			return cfg.APIKey
		},
		SimpleStreamOptions: core.SimpleStreamOptions{
			StreamOptions: core.StreamOptions{
				APIKey: cfg.APIKey,
			},
		},
	}

	return agentruntime.New(agentruntime.AgentOptions{InitialState: state})
}

// handleAgentEvent processes agent events and sends them as Bubble Tea messages.
func (a *App) handleAgentEvent(evt agentruntime.AgentEvent) {
	switch e := evt.(type) {
	case agentruntime.EventMessageUpdate:
		switch evt := e.AssistantEvent.(type) {
		case core.EventTextDelta:
			a.publish(agentStreamMsg{text: evt.Delta})
		case core.EventThinkingDelta:
			a.publish(agentThinkDeltaMsg{text: evt.Delta})
		case core.EventThinkingStart:
			a.publish(agentThinkStartMsg{})
		}
	case agentruntime.EventToolExecStart:
		argsStr := truncate(string(e.Args), 300)
		a.publish(agentToolStartMsg{name: e.ToolName, args: argsStr})
	case agentruntime.EventToolExecEnd:
		resultPreview := truncate(string(e.Result), 500)
		a.publish(agentToolEndMsg{
			name:   e.ToolName,
			result: resultPreview,
			isErr:  e.IsError,
		})
	case agentruntime.EventTurnEnd:
		if e.Message.ErrorMessage != "" {
			a.publish(agentStreamMsg{text: "\n\nError: " + e.Message.ErrorMessage})
		}
		a.publish(agentResponseMsg{err: nil})
	}
}

func (a *App) publish(msg tea.Msg) {
	if a.program != nil {
		a.program.Send(msg)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Bubble Tea Model Interface
// ─────────────────────────────────────────────────────────────────────────────

func (a *App) Init() tea.Cmd { return nil }

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.ready = true

		// Layout: chat area takes most, input at the bottom
		inputHeight := 4 // input bar + border
		a.chat.SetSize(msg.Width, msg.Height-inputHeight)
		a.input.SetSize(msg.Width)
		a.dialog.SetSize(msg.Width, msg.Height)
		return a, nil

	case tea.KeyMsg:
		return a.handleKeyMsg(msg)

	// Agent streaming events
	case agentStreamMsg:
		if len(a.chat.messages) == 0 || a.chat.messages[len(a.chat.messages)-1].Role != "assistant" {
			a.chat.AddMessage(ChatMessage{Role: "assistant", Content: msg.text})
		} else {
			last := a.chat.messages[len(a.chat.messages)-1].Content
			a.chat.UpdateLastMessage(last + msg.text)
		}
		return a, nil

	case agentThinkStartMsg:
		a.chat.AddMessage(ChatMessage{Role: "thinking", Content: ""})
		return a, nil

	case agentThinkDeltaMsg:
		a.chat.UpdateLastMessage(msg.text)
		return a, nil

	case agentToolStartMsg:
		a.chat.AddMessage(ChatMessage{
			Role:    "tool_call",
			Content: fmt.Sprintf("%s(%s)", msg.name, msg.args),
		})
		return a, nil

	case agentToolEndMsg:
		status := "[ok]"
		if msg.isErr {
			status = "[err]"
		}
		line := fmt.Sprintf("%s %s -> %s", status, msg.name, msg.result)
		a.chat.AddMessage(ChatMessage{Role: "tool_result", Content: line})
		return a, nil

	case agentResponseMsg:
		a.querying = false
		a.input.Enable()
		if msg.err != nil {
			a.chat.AddMessage(ChatMessage{Role: "error", Content: msg.err.Error()})
		}
		return a, nil
	}

	return a, nil
}

func (a *App) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Approval dialog takes precedence
	if a.dialog.Visible() {
		switch msg.String() {
		case "y", "Y":
			a.dialog.Hide()
			a.showApproval = false
			a.chat.AddMessage(ChatMessage{Role: "system", Content: "Approved"})
			a.input.Enable()
			return a, nil
		case "a", "A":
			a.dialog.Hide()
			a.showApproval = false
			a.chat.AddMessage(ChatMessage{Role: "system", Content: fmt.Sprintf("'%s' always allowed", a.dialog.toolName)})
			a.input.Enable()
			return a, nil
		case "n", "N", "esc":
			a.dialog.Hide()
			a.showApproval = false
			a.chat.AddMessage(ChatMessage{Role: "system", Content: "Denied"})
			a.input.Enable()
			return a, nil
		}
		return a, nil
	}

	// Help overlay dismiss
	if a.showHelp {
		a.showHelp = false
		return a, nil
	}

	// Global shortcuts
	switch msg.String() {
	case "ctrl+c":
		if a.querying {
			a.agent.Abort()
			a.chat.AddMessage(ChatMessage{Role: "system", Content: "Aborted"})
			a.querying = false
			a.input.Enable()
			return a, nil
		}
		return a, tea.Quit
	case "ctrl+l":
		a.chat.Clear()
		return a, nil
	}

	// Don't process input while querying
	if a.querying {
		return a, nil
	}

	// Input handling
	switch msg.Type {
	case tea.KeyEnter:
		val := strings.TrimSpace(a.input.Value())
		if val == "" {
			return a, nil
		}
		a.input.ClearInput()

		if strings.HasPrefix(val, "/") {
			return a.handleCommand(val)
		}

		a.querying = true
		a.input.Disable()
		a.chat.AddMessage(ChatMessage{Role: "user", Content: val})

		// Run agent in background goroutine
		return a, a.runAgent(val)

	case tea.KeyBackspace, tea.KeyCtrlH:
		a.input.DeleteLast()
		return a, nil

	case tea.KeyRunes, tea.KeySpace:
		for _, r := range msg.Runes {
			a.input.AppendRune(r)
		}
		return a, nil
	}

	return a, nil
}

func (a *App) handleCommand(cmd string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(cmd)
	base := parts[0]

	switch base {
	case "/quit", "/exit":
		return a, tea.Quit

	case "/clear":
		a.chat.Clear()
		a.pendingImages = 0
		a.input.SetImageHint(0)
		a.agent = newAgent(a.cfg)
		a.agent.Subscribe(func(evt agentruntime.AgentEvent) {
			a.handleAgentEvent(evt)
		})
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Conversation cleared"})
		return a, nil

	case "/help":
		a.showHelp = true
		helpText := `Commands:
  /quit, /exit     Exit
  /clear           Clear history
  /help            This help
  /tools           List tools
  /compact         Compact context
  /tokens          Show messages count

Keys:
  Enter            Send message
  Ctrl+C           Abort / quit
  Ctrl+L           Clear screen`
		a.chat.AddMessage(ChatMessage{Role: "system", Content: helpText})
		return a, nil

	case "/tools":
		var b strings.Builder
		b.WriteString("Tools:\n")
		for _, t := range a.agent.State().Tools {
			b.WriteString(fmt.Sprintf("  - %s\n", t.Name))
		}
		a.chat.AddMessage(ChatMessage{Role: "system", Content: b.String()})
		return a, nil

	case "/tokens":
		state := a.agent.State()
		a.chat.AddMessage(ChatMessage{Role: "system", Content: fmt.Sprintf(
			"Messages: %d\nSystem prompt: %d chars",
			len(state.Messages),
			len(state.SystemPrompt),
		)})
		return a, nil

	case "/compact":
		a.chat.AddMessage(ChatMessage{Role: "system", Content: "Context compaction handled automatically by the agent."})
		return a, nil

	default:
		a.chat.AddMessage(ChatMessage{Role: "error", Content: fmt.Sprintf("Unknown: %s. Try /help", cmd)})
		return a, nil
	}
}

func (a *App) runAgent(prompt string) tea.Cmd {
	return func() tea.Msg {
		queryTimeout := a.cfg.QueryTimeout
		if queryTimeout <= 0 {
			queryTimeout = 10 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()

		_, err := a.agent.Run(ctx, core.UserMessage{
			Role:      "user",
			Content:   prompt,
			Timestamp: time.Now(),
		})
		if err != nil {
			return agentResponseMsg{err: err}
		}
		return agentResponseMsg{err: nil}
	}
}

func (a *App) View() string {
	if !a.ready {
		return "Initializing..."
	}

	var b strings.Builder

	// Title bar
	b.WriteString(" Crux Agent TUI ")
	b.WriteString("\n")
	b.WriteString(strings.Repeat("-", a.width))
	b.WriteString("\n")

	// Chat area
	b.WriteString(a.chat.View())

	// Status bar
	if a.querying {
		b.WriteString("\n")
		b.WriteString(" Agent thinking... (Ctrl+C to abort)")
	}

	// Input
	b.WriteString("\n")
	b.WriteString(a.input.View())

	// Overlays
	if a.dialog.Visible() {
		return a.dialog.View()
	}

	return b.String()
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
