package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── Color Palette (Tokyo Night inspired) ──────────────────────────────────────
// Each role colour is a pair of (light, dark) hexadecimal values so the terminal
// can adapt to the user's background automatically via AdaptiveColor.

var (
	ColorBg        = lipgloss.AdaptiveColor{Light: "#f5f5f5", Dark: "#1a1b26"}
	ColorFg        = lipgloss.AdaptiveColor{Light: "#24283b", Dark: "#c0caf5"}
	ColorMuted     = lipgloss.AdaptiveColor{Light: "#9699b0", Dark: "#565f89"}
	ColorAccent    = lipgloss.AdaptiveColor{Light: "#7c3aed", Dark: "#bb9af7"}
	ColorGreen     = lipgloss.AdaptiveColor{Light: "#16a34a", Dark: "#9ece6a"}
	ColorRed       = lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#f7768e"}
	ColorYellow    = lipgloss.AdaptiveColor{Light: "#ca8a04", Dark: "#e0af68"}
	ColorBlue      = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#7aa2f7"}
	ColorCyan      = lipgloss.AdaptiveColor{Light: "#0891b2", Dark: "#7dcfff"}
	ColorOrange    = lipgloss.AdaptiveColor{Light: "#ea580c", Dark: "#ff9e64"}
	ColorBorder    = lipgloss.AdaptiveColor{Light: "#d1d5db", Dark: "#3b4261"}
	ColorSelect    = lipgloss.AdaptiveColor{Light: "#ede9fe", Dark: "#2d2f4e"}
	ColorDanger    = lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#e5484d"}
	ColorDiffAdd   = lipgloss.AdaptiveColor{Light: "#16a34a", Dark: "#9ece6a"}
	ColorDiffDel   = lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#f7768e"}
	ColorToolRead  = lipgloss.AdaptiveColor{Light: "#0891b2", Dark: "#7dcfff"}
	ColorToolWrite = lipgloss.AdaptiveColor{Light: "#16a34a", Dark: "#9ece6a"}
	ColorToolProc  = lipgloss.AdaptiveColor{Light: "#7c3aed", Dark: "#c678dd"}
	ColorToolShell = lipgloss.AdaptiveColor{Light: "#ca8a04", Dark: "#e0af68"}
	ColorUserBG    = lipgloss.AdaptiveColor{Light: "#f0f0f5", Dark: "#222631"}
	ColorCodeBG    = lipgloss.AdaptiveColor{Light: "#f4f4f5", Dark: "#1e1e2e"}
)

// ── Mode pill colours ─────────────────────────────────────────────────────────
var (
	PillAutoStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#f59e0b")).
			Foreground(lipgloss.Color("#111827")).
			Bold(true).
			Padding(0, 1)

	PillPlanStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#2563eb")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Padding(0, 1)

	PillYOLOStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#e5484d")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Padding(0, 1)
)

// ── Title ─────────────────────────────────────────────────────────────────────
var TitleStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(ColorAccent).
	PaddingLeft(1)

// SubtitleStyle is muted info text.
var SubtitleStyle = lipgloss.NewStyle().
	Foreground(ColorMuted).
	PaddingLeft(1)

// ── Chat message styles ───────────────────────────────────────────────────────
var UserMsgStyle = lipgloss.NewStyle().
	Foreground(ColorGreen).
	Bold(true).
	Padding(0, 1)

var UserBubbleStyle = lipgloss.NewStyle().
	Background(ColorUserBG).
	Padding(0, 2).
	Margin(0, 1)

var AssistantMsgStyle = lipgloss.NewStyle().
	Foreground(ColorFg).
	Padding(0, 1)

// SystemMsgStyle for metadata / notices.
var SystemMsgStyle = lipgloss.NewStyle().
	Foreground(ColorMuted).
	Padding(0, 1)

// ErrorMsgStyle for error messages.
var ErrorMsgStyle = lipgloss.NewStyle().
	Foreground(ColorRed).
	Bold(true).
	Padding(0, 1)

// ── Thinking / reasoning ─────────────────────────────────────────────────────
var ThinkingStyle = lipgloss.NewStyle().
	Foreground(ColorMuted).
	Italic(true).
	PaddingLeft(2)

// ReasoningStyle is the dim "▎ thinking…" marker.
var ReasoningStyle = lipgloss.NewStyle().
	Foreground(ColorMuted).
	PaddingLeft(2)

// ReasoningBlockStyle is the dim text block under "▎ thinking…".
var ReasoningBlockStyle = lipgloss.NewStyle().
	Foreground(ColorMuted).
	PaddingLeft(6)

// ── Tool call cards ───────────────────────────────────────────────────────────
// ToolCardHeaderStyle formats "● Verb(args)" — the card header line.
var ToolCardHeaderStyle = lipgloss.NewStyle().
	PaddingLeft(2)

// ToolCardConnectorStyle formats the "⎿" gutter for continuation blocks.
var ToolCardConnectorStyle = lipgloss.NewStyle().
	Foreground(ColorMuted)

// ToolCallStyle for "🔧 ToolName(args)".
var ToolCallStyle = lipgloss.NewStyle().
	Foreground(ColorYellow).
	PaddingLeft(2)

// ToolResultStyle for tool result output.
var ToolResultStyle = lipgloss.NewStyle().
	Foreground(ColorCyan).
	PaddingLeft(6)

// ── Diff ──────────────────────────────────────────────────────────────────────
var DiffAddStyle = lipgloss.NewStyle().
	Foreground(ColorDiffAdd)

var DiffDelStyle = lipgloss.NewStyle().
	Foreground(ColorDiffDel)

var DiffHeaderStyle = lipgloss.NewStyle().
	Foreground(ColorAccent).
	Bold(true)

// ── Input ─────────────────────────────────────────────────────────────────────
var InputPromptStyle = lipgloss.NewStyle().
	Foreground(ColorGreen).
	Bold(true)

var InputBoxStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(ColorBorder).
	Padding(0, 1)

var InputFocusedStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(ColorAccent).
	Padding(0, 1)

// ── Dialog (tool approval) ────────────────────────────────────────────────────
var DialogStyle = lipgloss.NewStyle().
	Border(lipgloss.DoubleBorder()).
	BorderForeground(ColorYellow).
	Padding(1, 2).
	Width(60)

var DialogTitleStyle = lipgloss.NewStyle().
	Foreground(ColorYellow).
	Bold(true)

var DialogKeyStyle = lipgloss.NewStyle().
	Foreground(ColorAccent).
	Bold(true)

var DialogDescStyle = lipgloss.NewStyle().
	Foreground(ColorMuted)

// ── Tool info ─────────────────────────────────────────────────────────────────
var ToolInfoStyle = lipgloss.NewStyle().
	Foreground(ColorAccent).
	Bold(true)

// ── Status bar ────────────────────────────────────────────────────────────────
var StatusBarStyle = lipgloss.NewStyle().
	Foreground(ColorMuted)

var DataLineStyle = lipgloss.NewStyle().
	Foreground(ColorMuted)

// ── Code block ────────────────────────────────────────────────────────────────
var CodeBlockStyle = lipgloss.NewStyle().
	Background(ColorCodeBG).
	Foreground(ColorFg).
	Padding(0, 2)

// ── Helper: tool verb colour ──────────────────────────────────────────────────
// toolDotColor returns the colour for a tool's status dot based on its category.
func toolDotColor(name string) lipgloss.AdaptiveColor {
	switch name {
	case "read_file", "glob", "list_files", "grep", "search":
		return ColorToolRead
	case "write_file", "edit_file", "multi_edit", "delete_range":
		return ColorToolWrite
	case "bash", "shell", "execute":
		return ColorToolShell
	case "wait", "bash_output", "kill_shell":
		return ColorToolProc
	default:
		return ColorAccent
	}
}

// ── Helper: connector block ───────────────────────────────────────────────────
const connectorStr = "  ⎿  "

// connectorWidth is the visible width of the connector prefix.
var connectorWidth = lipgloss.Width(connectorStr)

// ConnectorBlock wraps a list of lines under the "⎿" gutter.
func ConnectorBlock(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	indent := strings.Repeat(" ", connectorWidth)
	out := ToolCardConnectorStyle.Render(connectorStr) + lines[0]
	for _, ln := range lines[1:] {
		out += "\n" + indent + ln
	}
	return out
}
