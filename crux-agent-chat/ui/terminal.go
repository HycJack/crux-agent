// Package ui provides the terminal interface for the coding agent.
package ui

import (
	"fmt"
	"strings"

	"crux-ai/core"
	runtime "crux-agent-runtime/agent"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorRed    = "\033[31m"
)

// Subscriber returns a function that prints agent events to the terminal.
func Subscriber() func(runtime.AgentEvent) {
	return func(evt runtime.AgentEvent) {
		switch e := evt.(type) {
		case runtime.EventMessageUpdate:
			handleMessageUpdate(e)
		case runtime.EventToolExecStart:
			// Show tool name and truncated args
			argsStr := truncate(string(e.Args), 300)
			fmt.Printf("\n%s🔧 [%s]%s %s\n", colorYellow, e.ToolName, colorReset, argsStr)
		case runtime.EventToolExecEnd:
			status := colorGreen + "✓"
			if e.IsError {
				status = colorRed + "✗"
			}
			// Fix #6: show result summary
			resultPreview := truncate(string(e.Result), 500)
			fmt.Printf("%s%s [%s]%s\n", status, colorReset, e.ToolName, colorReset)
			if resultPreview != "" && resultPreview != "null" {
				fmt.Printf("%s  → %s%s\n", colorDim, resultPreview, colorReset)
			}
		}
	}
}

func handleMessageUpdate(e runtime.EventMessageUpdate) {
	switch evt := e.AssistantEvent.(type) {
	case core.EventTextDelta:
		fmt.Print(evt.Delta)
	case core.EventThinkingDelta:
		fmt.Printf("%s%s%s", colorDim, evt.Delta, colorReset)
	case core.EventThinkingStart:
		fmt.Printf("\n%s💭 thinking...%s\n", colorDim, colorReset)
	}
}

// PrintBanner prints the agent startup banner.
func PrintBanner(provider, model string) {
	fmt.Printf(`
%s╔══════════════════════════════════════╗
║     %s🚀 Crux Agent Chat%s                ║
╠══════════════════════════════════════╣
║  Provider: %-25s║
║  Model:    %-25s║
╚══════════════════════════════════════╝%s

Type your message and press Enter.
Commands: /quit  /clear  /help
`, colorCyan, colorBold, colorReset, provider, model, colorReset)
}

// PrintHelp prints the help message.
func PrintHelp() {
	fmt.Printf(`
%sCommands:%s
  /quit, /exit    Exit the agent
  /clear          Clear conversation history
  /help           Show this help message
  /tools          List available tools

Type any message to chat with the agent.
The agent can read/write files, run shell commands, and edit code.
`, colorBold, colorReset)
}

// PrintTools prints available tools.
func PrintTools() {
	fmt.Printf("\n%sAvailable Tools:%s\n", colorBold, colorReset)
	toolNames := []struct{ name, desc string }{
		{"bash", "Execute shell commands"},
		{"read_file", "Read file contents (with line numbers)"},
		{"write_file", "Write content to a file"},
		{"list_files", "List directory contents"},
		{"edit_file", "Search and replace in a file"},
	}
	for _, t := range toolNames {
		fmt.Printf("  %s%-12s%s %s\n", colorCyan, t.name, colorReset, t.desc)
	}
	fmt.Println()
}

// PrintError prints an error message.
func PrintError(msg string, args ...any) {
	fmt.Printf("%s❌ %s%s\n", colorRed, fmt.Sprintf(msg, args...), colorReset)
}

// PrintInfo prints an info message.
func PrintInfo(msg string, args ...any) {
	fmt.Printf("%sℹ %s%s\n", colorBlue, fmt.Sprintf(msg, args...), colorReset)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// FormatUserPrompt formats the user input prompt.
func FormatUserPrompt() string {
	return fmt.Sprintf("\n%s👤 You:%s ", colorGreen, colorReset)
}

// PrintSeparator prints a visual separator.
func PrintSeparator() {
	fmt.Printf("%s%s%s\n", colorDim, strings.Repeat("─", 60), colorReset)
}
