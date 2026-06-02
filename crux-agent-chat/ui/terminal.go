// Package ui provides the terminal interface for the coding agent.
package ui

import (
	"crux-agent-chat/harness"
	"fmt"
	"os"
	goruntime "runtime"
	"strings"
	"sync"

	runtime "crux-agent-runtime/agent"
	"crux-ai/core"
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

var vtOnce sync.Once

// enableVTSupport enables Windows virtual terminal (ANSI) processing on
// stdout and stderr so the color escapes below render correctly. No-op on
// other platforms.
func enableVTSupport() {
	if goruntime.GOOS != "windows" {
		return
	}
	vtOnce.Do(func() {
		for _, f := range []*os.File{os.Stdout, os.Stderr} {
			enableVTOnHandle(f)
		}
	})
}

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
	enableVTSupport()
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
  /quit, /exit       Exit the agent
  /clear             Clear conversation history (and staged images)
  /help              Show this help message
  /tools             List available tools
  /paste <path>...   Stage one or more images for the next turn
  /clearimg          Clear staged images without clearing history

%sMultimodal:%s
  Just type a message that contains an image path (jpg/jpeg/png/gif/webp)
  and the agent will attach it. Example:
    "What is in C:\Users\me\Desktop\screenshot.png?"
  Or stage multiple images first and ask your question:
    /paste a.png b.png
    Describe the differences.

Type any message to chat with the agent.
The agent can read/write files, run shell commands, and edit code.
`, colorBold, colorReset, colorBold, colorReset)
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
		{"read_image", "Read a local image as a multimodal attachment"},
	}
	for _, t := range toolNames {
		fmt.Printf("  %s%-12s%s %s\n", colorCyan, t.name, colorReset, t.desc)
	}
	fmt.Println()
}

// PrintError prints an error message.
func PrintError(msg string, args ...any) {
	enableVTSupport()
	fmt.Printf("%s❌ %s%s\n", colorRed, fmt.Sprintf(msg, args...), colorReset)
}

// PrintInfo prints an info message.
func PrintInfo(msg string, args ...any) {
	enableVTSupport()
	fmt.Printf("%sℹ %s%s\n", colorBlue, fmt.Sprintf(msg, args...), colorReset)
}

// PrintWarn prints a warning message.
func PrintWarn(msg string, args ...any) {
	enableVTSupport()
	fmt.Printf("%s⚠ %s%s\n", colorYellow, fmt.Sprintf(msg, args...), colorReset)
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

// FormatUserPrompt formats the user input prompt. If imageCount > 0 the
// count of pending images is shown in the prompt suffix.
func FormatUserPrompt(imageCount int) string {
	if imageCount == 0 {
		return fmt.Sprintf("\n%s👤 You:%s ", colorGreen, colorReset)
	}
	return fmt.Sprintf("\n%s👤 You:%s %s📎 %d image(s) attached%s ",
		colorGreen, colorReset, colorCyan, imageCount, colorReset)
}

// PrintMultimodalHint prints a short hint about image attachments when the
// session starts, so the user knows about the new capability.
func PrintMultimodalHint() {
	fmt.Printf("%s💡 Tip: drop an image path into the prompt, or use /paste <path> to attach images for the next turn.%s\n\n",
		colorDim, colorReset)
}

// PrintSeparator prints a visual separator.
func PrintSeparator() {
	enableVTSupport()
	fmt.Printf("%s%s%s\n", colorDim, strings.Repeat("─", 60), colorReset)
}

// PrintHarnessSummary prints a one-line summary of the harness status
// (session id, skills loaded, etc.) when the REPL starts.
func PrintHarnessSummary(h *harness.Harness) {
	enableVTSupport()
	skills := h.LoadedSkillsCount()
	if skills > 0 {
		fmt.Printf("%s🧩 Harness: session=%s · %d skill(s) loaded · %s%s\n\n",
			colorDim, h.SessionID(), skills, h.SessionPath(), colorReset)
	} else {
		fmt.Printf("%s🧩 Harness: session=%s · 0 skills · %s%s\n\n",
			colorDim, h.SessionID(), h.SessionPath(), colorReset)
	}
}

// PrintTokenUsage prints the cumulative token usage for this session.
func PrintTokenUsage(h *harness.Harness) {
	t := h.Snapshot()
	fmt.Printf("\n%sToken usage (this session):%s\n", colorBold, colorReset)
	fmt.Printf("  Input:         %d\n", t.Input)
	fmt.Printf("  Output:        %d\n", t.Output)
	if t.CacheRead > 0 {
		fmt.Printf("  Cache read:    %d\n", t.CacheRead)
	}
	if t.CacheWrite > 0 {
		fmt.Printf("  Cache write:   %d\n", t.CacheWrite)
	}
	fmt.Printf("  Total:         %d\n", t.Total)
	if t.Compactions > 0 {
		fmt.Printf("  Compactions:   %d\n", t.Compactions)
	}
	if cost := h.EstimatedCost(); cost > 0 {
		fmt.Printf("  Estimated cost: $%.4f\n", cost)
	}
	fmt.Println()
}

// PrintSessionInfo prints the session id and JSONL file path.
func PrintSessionInfo(h interface {
	SessionID() string
	SessionPath() string
}) {
	fmt.Printf("\n%sSession:%s\n", colorBold, colorReset)
	fmt.Printf("  ID:   %s\n", h.SessionID())
	fmt.Printf("  File: %s\n\n", h.SessionPath())
}

// PrintSkills prints the list of loaded skills.
func PrintSkills(skills []harness.SkillSummary) {
	fmt.Printf("\n%sLoaded skills:%s\n", colorBold, colorReset)
	if len(skills) == 0 {
		fmt.Printf("  %s(none — drop SKILL.md files in .crux/skills/ or ~/.crux/skills/)%s\n\n", colorDim, colorReset)
		return
	}
	for _, s := range skills {
		fmt.Printf("  %s● %s%s — %s\n", colorCyan, s.Name, colorReset, s.Description)
		fmt.Printf("      %s%s%s\n", colorDim, s.FilePath, colorReset)
	}
	fmt.Println()
}
