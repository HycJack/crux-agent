package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ApprovalResult is the result of an approval dialog.
type ApprovalResult int

const (
	ApprovalPending ApprovalResult = iota
	ApprovalDenied
	ApprovalAllowed
	ApprovalAlways
)

// ApprovalChoiceMsg is sent when the user makes a choice in the approval dialog.
type ApprovalChoiceMsg struct {
	Result ApprovalResult
	ToolID string
}

// ApprovalDialog shows a tool approval prompt overlay.
type ApprovalDialog struct {
	visible  bool
	toolName string
	toolID   string
	args     string
	width    int
	height   int
}

// NewApprovalDialog creates a new approval dialog.
func NewApprovalDialog() *ApprovalDialog {
	return &ApprovalDialog{}
}

// Show displays the approval prompt for a tool call.
func (d *ApprovalDialog) Show(toolName, toolID, args string) {
	d.visible = true
	d.toolName = toolName
	d.toolID = toolID
	d.args = args
}

// Hide dismisses the dialog.
func (d *ApprovalDialog) Hide() {
	d.visible = false
	d.toolName = ""
	d.toolID = ""
	d.args = ""
}

// Visible returns whether the dialog is currently shown.
func (d *ApprovalDialog) Visible() bool {
	return d.visible
}

// ToolID returns the ID of the tool being approved.
func (d *ApprovalDialog) ToolID() string {
	return d.toolID
}

// SetSize updates dimensions.
func (d *ApprovalDialog) SetSize(w, h int) {
	d.width = w
	d.height = h
}

// Update handles key events for the dialog.
func (d *ApprovalDialog) Update(msg tea.Msg) *ApprovalDialog {
	if !d.visible {
		return d
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			return d
		case "n", "N", "esc":
			d.Hide()
			return d
		case "a", "A":
			return d
		}
	}
	return d
}

// View renders the dialog as an overlay. Returns empty string if not visible.
func (d *ApprovalDialog) View() string {
	if !d.visible {
		return ""
	}

	var b strings.Builder

	// Title
	b.WriteString(DialogTitleStyle.Render("⚠ Tool Approval Needed"))
	b.WriteString("\n\n")

	// Tool name
	b.WriteString(DialogKeyStyle.Render("Tool: "))
	b.WriteString(ToolNameStyle.Render(d.toolName))
	b.WriteString("\n")

	// Args
	if d.args != "" {
		displayArgs := d.args
		if len(displayArgs) > 500 {
			displayArgs = displayArgs[:500] + "\n...(truncated)"
		}
		b.WriteString(DialogKeyStyle.Render("Args:\n"))
		b.WriteString(DialogDescStyle.Render(displayArgs))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(DialogKeyStyle.Render("[y]") + DialogDescStyle.Render(" allow "))
	b.WriteString(DialogKeyStyle.Render("[a]") + DialogDescStyle.Render(" always allow this tool "))
	b.WriteString(DialogKeyStyle.Render("[n/esc]") + DialogDescStyle.Render(" deny"))
	b.WriteString("\n")

	dialogContent := b.String()

	// Center the dialog on screen
	dialogW := d.width - 10
	if dialogW > 72 {
		dialogW = 72
	}
	if dialogW < 40 {
		dialogW = 40
	}

	styledDialog := DialogStyle.
		Width(dialogW).
		Render(dialogContent)

	dialogH := lipgloss.Height(styledDialog)
	topPad := (d.height - dialogH) / 2
	if topPad < 0 {
		topPad = 0
	}
	leftPad := (d.width - lipgloss.Width(styledDialog)) / 2
	if leftPad < 0 {
		leftPad = 0
	}

	// Build overlay: centered dialog with dimmed background
	overlay := strings.Repeat("\n", topPad) +
		strings.Repeat(" ", leftPad) + styledDialog

	return overlay
}

// ApprovalChoiceKey is a helper to determine what key was pressed.
func ApprovalChoiceKey(msg tea.KeyMsg) ApprovalResult {
	switch msg.String() {
	case "y", "Y":
		return ApprovalAllowed
	case "a", "A":
		return ApprovalAlways
	case "n", "N", "esc":
		return ApprovalDenied
	default:
		return ApprovalPending
	}
}

// formatApprovalArgs pretty-prints tool arguments for display.
func formatApprovalArgs(name string, argsJSON []byte) string {
	// Try to pretty-print JSON
	var pretty strings.Builder
	if len(argsJSON) > 0 {
		raw := string(argsJSON)
		// Simple formatting: just show the raw JSON with some trimming
		if len(raw) > 600 {
			raw = raw[:600] + "\n  ...(truncated)"
		}
		pretty.WriteString(raw)
	}
	return pretty.String()
}

// formatApprovalToolInfo creates a one-line tool call info.
func formatApprovalToolInfo(toolName string, argsJSON string) string {
	display := argsJSON
	if len(display) > 100 {
		display = display[:100] + "..."
	}
	return fmt.Sprintf("%s(%s)", toolName, display)
}
