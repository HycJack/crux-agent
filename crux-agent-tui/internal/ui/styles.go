package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── Color Palette (Tokyo Night inspired) ──────────────────────────────────────

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

// ── Cached styles (rebuilt on theme change) ───────────────────────────────────

var (
	// Pills
	PillAutoStyle lipgloss.Style
	PillPlanStyle lipgloss.Style
	PillYOLOStyle lipgloss.Style

	// Title
	TitleStyle    lipgloss.Style
	SubtitleStyle lipgloss.Style

	// Chat messages
	UserMsgStyle      lipgloss.Style
	UserBubbleStyle   lipgloss.Style
	AssistantMsgStyle lipgloss.Style
	SystemMsgStyle    lipgloss.Style
	ErrorMsgStyle     lipgloss.Style

	// Thinking / reasoning
	ThinkingStyle       lipgloss.Style
	ReasoningStyle      lipgloss.Style
	ReasoningBlockStyle lipgloss.Style

	// Tool cards
	ToolCardHeaderStyle    lipgloss.Style
	ToolCardConnectorStyle lipgloss.Style
	ToolCallStyle          lipgloss.Style
	ToolResultStyle        lipgloss.Style

	// Diff
	DiffAddStyle    lipgloss.Style
	DiffDelStyle    lipgloss.Style
	DiffHeaderStyle lipgloss.Style

	// Input
	InputPromptStyle  lipgloss.Style
	InputBoxStyle     lipgloss.Style
	InputFocusedStyle lipgloss.Style

	// Dialog
	DialogStyle      lipgloss.Style
	DialogTitleStyle lipgloss.Style
	DialogKeyStyle   lipgloss.Style
	DialogDescStyle  lipgloss.Style

	// Tool info
	ToolInfoStyle lipgloss.Style

	// Status bar
	StatusBarStyle lipgloss.Style
	DataLineStyle  lipgloss.Style

	// Code block
	CodeBlockStyle lipgloss.Style

	// Selection & scrollbar (from chat.go)
	selStyle         lipgloss.Style
	scrollThumbStyle lipgloss.Style
	scrollTrackStyle lipgloss.Style

	// Todo panel (app.go)
	todoHeaderStyle lipgloss.Style
	todoGreenStyle  lipgloss.Style
	todoYellowStyle lipgloss.Style
	todoDimStyle    lipgloss.Style

	// Spinner
	spinnerStyle lipgloss.Style

	// Working line
	workingStyle lipgloss.Style

	// Separator
	separatorStyle lipgloss.Style

	// Dim line cache
	dimLineStyle lipgloss.Style

	// Danger foreground
	dangerFgStyle lipgloss.Style

	// Mode info styles
	modePlanInfoStyle lipgloss.Style
	modeYOLOInfoStyle lipgloss.Style
)

// refreshStyles rebuilds all cached styles. Call this once at init and whenever
// the colour palette changes (e.g. theme switch). All exported style variables
// are valid after this call.
func refreshStyles() {
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

	TitleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorAccent).
		PaddingLeft(1)

	SubtitleStyle = lipgloss.NewStyle().
		Foreground(ColorMuted).
		PaddingLeft(1)

	UserMsgStyle = lipgloss.NewStyle().
		Foreground(ColorGreen).
		Bold(true).
		Padding(0, 1)

	UserBubbleStyle = lipgloss.NewStyle().
		Background(ColorUserBG).
		Padding(0, 2).
		Margin(0, 1)

	AssistantMsgStyle = lipgloss.NewStyle().
		Foreground(ColorFg).
		Padding(0, 1)

	SystemMsgStyle = lipgloss.NewStyle().
		Foreground(ColorMuted).
		Padding(0, 1)

	ErrorMsgStyle = lipgloss.NewStyle().
		Foreground(ColorRed).
		Bold(true).
		Padding(0, 1)

	ThinkingStyle = lipgloss.NewStyle().
		Foreground(ColorMuted).
		Italic(true).
		PaddingLeft(2)

	ReasoningStyle = lipgloss.NewStyle().
		Foreground(ColorMuted).
		PaddingLeft(2)

	ReasoningBlockStyle = lipgloss.NewStyle().
		Foreground(ColorMuted).
		PaddingLeft(6)

	ToolCardHeaderStyle = lipgloss.NewStyle().
		PaddingLeft(2)

	ToolCardConnectorStyle = lipgloss.NewStyle().
		Foreground(ColorMuted)

	ToolCallStyle = lipgloss.NewStyle().
		Foreground(ColorYellow).
		PaddingLeft(2)

	ToolResultStyle = lipgloss.NewStyle().
		Foreground(ColorCyan).
		PaddingLeft(6)

	DiffAddStyle = lipgloss.NewStyle().
		Foreground(ColorDiffAdd)

	DiffDelStyle = lipgloss.NewStyle().
		Foreground(ColorDiffDel)

	DiffHeaderStyle = lipgloss.NewStyle().
		Foreground(ColorAccent).
		Bold(true)

	InputPromptStyle = lipgloss.NewStyle().
		Foreground(ColorGreen).
		Bold(true)

	InputBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorBorder).
		Padding(0, 1)

	InputFocusedStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorAccent).
		Padding(0, 1)

	DialogStyle = lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(ColorYellow).
		Padding(1, 2).
		Width(60)

	DialogTitleStyle = lipgloss.NewStyle().
		Foreground(ColorYellow).
		Bold(true)

	DialogKeyStyle = lipgloss.NewStyle().
		Foreground(ColorAccent).
		Bold(true)

	DialogDescStyle = lipgloss.NewStyle().
		Foreground(ColorMuted)

	ToolInfoStyle = lipgloss.NewStyle().
		Foreground(ColorAccent).
		Bold(true)

	StatusBarStyle = lipgloss.NewStyle().
		Foreground(ColorMuted)

	DataLineStyle = lipgloss.NewStyle().
		Foreground(ColorMuted)

	CodeBlockStyle = lipgloss.NewStyle().
		Background(ColorCodeBG).
		Foreground(ColorFg).
		Padding(0, 2)

	selStyle = lipgloss.NewStyle().Reverse(true)
	scrollThumbStyle = lipgloss.NewStyle().Foreground(ColorMuted)
	scrollTrackStyle = lipgloss.NewStyle().Foreground(ColorBorder)

	todoHeaderStyle = lipgloss.NewStyle().Foreground(ColorAccent)
	todoGreenStyle = lipgloss.NewStyle().Foreground(ColorGreen)
	todoYellowStyle = lipgloss.NewStyle().Foreground(ColorYellow)
	todoDimStyle = lipgloss.NewStyle().Foreground(ColorMuted)

	spinnerStyle = lipgloss.NewStyle().Foreground(ColorAccent)

	workingStyle = lipgloss.NewStyle().Foreground(ColorMuted).Padding(0, 1)

	separatorStyle = lipgloss.NewStyle().Foreground(ColorBorder)

	dimLineStyle = lipgloss.NewStyle().Foreground(ColorMuted)

	dangerFgStyle = lipgloss.NewStyle().Foreground(ColorDanger)

	modePlanInfoStyle = lipgloss.NewStyle().Foreground(ColorMuted)
	modeYOLOInfoStyle = lipgloss.NewStyle().Foreground(ColorDanger)
}

func init() {
	refreshStyles()
}

// ── Helper: tool verb colour ──────────────────────────────────────────────────

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
