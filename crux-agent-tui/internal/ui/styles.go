package ui

import "github.com/charmbracelet/lipgloss"

// Color palette (Tokyo Night inspired)
var (
	ColorBg     = lipgloss.AdaptiveColor{Light: "#f5f5f5", Dark: "#1a1b26"}
	ColorFg     = lipgloss.AdaptiveColor{Light: "#24283b", Dark: "#c0caf5"}
	ColorMuted  = lipgloss.AdaptiveColor{Light: "#9699b0", Dark: "#565f89"}
	ColorAccent = lipgloss.AdaptiveColor{Light: "#7c3aed", Dark: "#bb9af7"}
	ColorGreen  = lipgloss.AdaptiveColor{Light: "#16a34a", Dark: "#9ece6a"}
	ColorRed    = lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#f7768e"}
	ColorYellow = lipgloss.AdaptiveColor{Light: "#ca8a04", Dark: "#e0af68"}
	ColorBlue   = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#7aa2f7"}
	ColorCyan   = lipgloss.AdaptiveColor{Light: "#0891b2", Dark: "#7dcfff"}
	ColorOrange = lipgloss.AdaptiveColor{Light: "#ea580c", Dark: "#ff9e64"}
	ColorBorder = lipgloss.AdaptiveColor{Light: "#d1d5db", Dark: "#3b4261"}
	ColorSelect = lipgloss.AdaptiveColor{Light: "#ede9fe", Dark: "#2d2f4e"}

	// Title
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorAccent).
			PaddingLeft(1)

	// Subtitle / muted info
	SubtitleStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			PaddingLeft(1)

	// Chat messages
	UserMsgStyle = lipgloss.NewStyle().
			Foreground(ColorGreen).
			Bold(true).
			Padding(0, 1)

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

	// Thinking / tool call status
	ThinkingStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true).
			PaddingLeft(2)

	ToolCallStyle = lipgloss.NewStyle().
			Foreground(ColorYellow).
			PaddingLeft(2)

	ToolResultStyle = lipgloss.NewStyle().
			Foreground(ColorCyan).
			PaddingLeft(4)

	// Input
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

	// Dialog (tool approval)
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

	// Tool info
	ToolInfoStyle = lipgloss.NewStyle().
			Foreground(ColorAccent).
			Bold(true).
			Padding(0, 1)

	ToolNameStyle = lipgloss.NewStyle().
			Foreground(ColorYellow).
			Bold(true)

	// Status bar
	StatusBarStyle = lipgloss.NewStyle().
			Background(ColorBorder).
			Foreground(ColorFg).
			Padding(0, 1)

	// Separator
	SeparatorStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)
)
