package command

import (
	"context"
	"crux-agent-chat/harness"
	"crux-agent-chat/ui"
	"github.com/hycjack/crux-ai/core"
	"fmt"
	"strings"

	runtime "crux-agent-runtime/agent"
)

// Context provides dependencies for command handlers.
type Context struct {
	Agent     Agent
	Harness   *harness.Harness
	Config    Config
	SessionID string
}

// HandlerResult represents the result of a command execution.
type HandlerResult struct {
	Handled          bool
	Done             bool
	ClearPending     bool
	ResetSession     bool
	StagedBlocks     []core.ContentBlock
	NewHarness       *harness.Harness
	NewAgent         Agent
	RestoreSessionID string
}

// Handler is a function that handles a command.
type Handler func(ctx *Context, input string) HandlerResult

// Agent interface for chat agent operations.
type Agent interface {
	ResetSubscribers()
	SetOverride([]core.Message)
	Subscribe(func(runtime.AgentEvent))
	Messages() []core.Message
	Run(ctx context.Context, prompt core.UserMessage) ([]core.Message, error)
	Abort()
}

// Config interface for configuration access.
type Config interface {
	GetModel() core.Model
	GetAPIKey() string
	GetProvider() core.KnownProvider
	GetModelID() string
}

// Registry holds registered command handlers.
type Registry struct {
	handlers map[string]Handler
}

// NewRegistry creates a new command registry.
func NewRegistry() *Registry {
	r := &Registry{
		handlers: make(map[string]Handler),
	}
	r.registerBuiltins()
	return r
}

func (r *Registry) registerBuiltins() {
	r.handlers["/quit"] = handleQuit
	r.handlers["/exit"] = handleQuit
	r.handlers["/clear"] = handleClear
	r.handlers["/help"] = handleHelp
	r.handlers["/tools"] = handleTools
	r.handlers["/tokens"] = handleTokens
	r.handlers["/session"] = handleSession
	r.handlers["/skills"] = handleSkills
	r.handlers["/compact"] = handleCompact
	r.handlers["/new"] = handleNew
	r.handlers["/restore"] = handleRestore
	r.handlers["/sessions"] = handleListSessions
	r.handlers["/history"] = handleHistory
}

// Register adds a custom command handler.
func (r *Registry) Register(command string, handler Handler) {
	r.handlers[command] = handler
}

// Handle processes a command input and returns the result.
func (r *Registry) Handle(ctx *Context, input string) HandlerResult {
	if len(input) == 0 || input[0] != '/' {
		return HandlerResult{}
	}

	// Find exact command matches
	handler, ok := r.handlers[input]
	if !ok {
		// Try to find partial matches for commands with arguments
		for cmd, h := range r.handlers {
			if len(input) >= len(cmd) && input[:len(cmd)] == cmd &&
				(len(input) == len(cmd) || input[len(cmd)] == ' ') {
				handler = h
				break
			}
		}
	}

	if handler != nil {
		return handler(ctx, input)
	}

	ui.PrintError("Unknown command: %s (type /help for help)", input)
	return HandlerResult{Handled: true}
}

// Built-in command handlers
func handleQuit(ctx *Context, input string) HandlerResult {
	ui.PrintInfo("Goodbye! 👋")
	return HandlerResult{Handled: true, Done: true}
}

func handleClear(ctx *Context, input string) HandlerResult {
	ctx.Agent.ResetSubscribers()
	ctx.Agent.SetOverride(nil)
	ui.PrintInfo("Conversation cleared.")
	return HandlerResult{Handled: true, ClearPending: true, ResetSession: true}
}

func handleHelp(ctx *Context, input string) HandlerResult {
	ui.PrintHelp()
	return HandlerResult{Handled: true}
}

func handleTools(ctx *Context, input string) HandlerResult {
	ui.PrintTools()
	return HandlerResult{Handled: true}
}

func handleTokens(ctx *Context, input string) HandlerResult {
	ui.PrintTokenUsage(ctx.Harness)
	return HandlerResult{Handled: true}
}

func handleSession(ctx *Context, input string) HandlerResult {
	ui.PrintSessionInfo(ctx.Harness)
	return HandlerResult{Handled: true}
}

func handleSkills(ctx *Context, input string) HandlerResult {
	ui.PrintSkills(ctx.Harness.LoadedSkills())
	return HandlerResult{Handled: true}
}

func handleCompact(ctx *Context, input string) HandlerResult {
	ui.PrintInfo("Compacting context...")
	return HandlerResult{Handled: true, ResetSession: true}
}

func handleNew(ctx *Context, input string) HandlerResult {
	return HandlerResult{Handled: true, ClearPending: true, ResetSession: true, NewHarness: nil, NewAgent: nil}
}

func handleRestore(ctx *Context, input string) HandlerResult {
	args := strings.Fields(input)
	if len(args) < 2 {
		ui.PrintError("Usage: /restore <session-id>")
		ui.PrintInfo("Use /sessions to list available sessions")
		return HandlerResult{Handled: true}
	}
	sessionID := args[1]
	return HandlerResult{Handled: true, ClearPending: true, ResetSession: true, NewHarness: nil, NewAgent: nil, RestoreSessionID: sessionID}
}

func handleListSessions(ctx *Context, input string) HandlerResult {
	sessions, err := ctx.Harness.ListSessions()
	if err != nil {
		ui.PrintError("Failed to list sessions: %v", err)
		return HandlerResult{Handled: true}
	}
	if len(sessions) == 0 {
		ui.PrintInfo("No sessions found.")
		return HandlerResult{Handled: true}
	}
	ui.PrintInfo("Available sessions:")
	for _, sess := range sessions {
		ui.PrintInfo("  %s", sess)
	}
	return HandlerResult{Handled: true}
}

func handleHistory(ctx *Context, input string) HandlerResult {
	messages := ctx.Agent.Messages()
	if len(messages) == 0 {
		ui.PrintInfo("No message history.")
		return HandlerResult{Handled: true}
	}

	ui.PrintInfo("Message history (%d messages):", len(messages))
	ui.PrintInfo("----------------------------------------")
	for i, msg := range messages {
		switch m := msg.(type) {
		case core.UserMessage:
			ui.PrintInfo("[%d] User: %s", i+1, formatContent(m.Content))
		case core.AssistantMessage:
			ui.PrintInfo("[%d] Assistant: %s", i+1, formatContent(m.Content))
		case core.ToolResultMessage:
			ui.PrintInfo("[%d] Tool (%s): %s", i+1, m.ToolName, formatContent(m.Content))
		default:
			ui.PrintInfo("[%d] %T: %v", i+1, msg, msg)
		}
	}
	ui.PrintInfo("----------------------------------------")
	return HandlerResult{Handled: true}
}

func formatContent(content any) string {
	switch c := content.(type) {
	case string:
		if len(c) > 200 {
			return c[:197] + "..."
		}
		return c
	case []core.ContentBlock:
		var text string
		for _, block := range c {
			switch b := block.(type) {
			case core.TextContent:
				text += b.Text
			case core.ThinkingContent:
				text += "[thinking] " + b.Thinking
			case core.ToolCall:
				text += "[tool_call] " + b.Name
			}
		}
		if len(text) > 200 {
			return text[:197] + "..."
		}
		return text
	default:
		return fmt.Sprintf("%v", content)
	}
}

// Helper handlers that need special treatment
func HandleClearImg() HandlerResult {
	ui.PrintInfo("Cleared staged images.")
	return HandlerResult{Handled: true, ClearPending: true}
}
