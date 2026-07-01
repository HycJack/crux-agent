package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Tool card types ───────────────────────────────────────────────────────────

// ToolCallInfo represents a tool invocation shown as a card.
type ToolCallInfo struct {
	Name      string // tool name, e.g. "read_file"
	Args      string // human-readable args, e.g. "path/to/file"
	RawArgs   string // full JSON args
	Result    string // tool output text (streaming or final)
	IsError   bool
	Streaming bool   // true while the tool is still running
	Lines     int    // total lines of output produced
	StartTime string // relative time when tool started
}

// ChatMessage represents a single message in the conversation.
type ChatMessage struct {
	Role     string // "user", "assistant", "system", "error", "reasoning", "tool_call"
	Content  string
	ToolCall *ToolCallInfo // non-nil only for tool_call role
}

// ChatView displays the conversation history as a scrollable list.
type ChatView struct {
	messages []ChatMessage
	vp       viewport.Model
	width    int
	height   int
	ready    bool
}

// NewChatView creates a new chat viewport.
func NewChatView() *ChatView {
	return &ChatView{}
}

// SetSize updates the viewport dimensions.
func (c *ChatView) SetSize(w, h int) {
	c.width = w
	c.height = h
	if !c.ready {
		c.vp = viewport.New(w, h)
		c.ready = true
	}
	c.vp.Width = w
	c.vp.Height = h
}

// AddMessage appends a new message and scrolls to bottom.
func (c *ChatView) AddMessage(msg ChatMessage) {
	c.messages = append(c.messages, msg)
	c.renderContent()
	c.vp.GotoBottom()
}

// UpdateLastMessage updates the content of the most recent message.
func (c *ChatView) UpdateLastMessage(content string) {
	if len(c.messages) == 0 {
		return
	}
	c.messages[len(c.messages)-1].Content = content
	c.renderContent()
	c.vp.GotoBottom()
}

// ReplaceLastMessage replaces the last message entirely.
func (c *ChatView) ReplaceLastMessage(msg ChatMessage) {
	if len(c.messages) == 0 {
		c.messages = append(c.messages, msg)
	} else {
		c.messages[len(c.messages)-1] = msg
	}
	c.renderContent()
	c.vp.GotoBottom()
}

// UpdateLastToolOutput updates the output of the last tool_call message.
func (c *ChatView) UpdateLastToolOutput(chunk string) {
	if len(c.messages) == 0 {
		return
	}
	last := &c.messages[len(c.messages)-1]
	if last.ToolCall != nil {
		last.ToolCall.Result += chunk
		last.ToolCall.Lines = strings.Count(last.ToolCall.Result, "\n")
	}
	c.renderContent()
	c.vp.GotoBottom()
}

// PopLastMessage removes the most recent message and returns it.
func (c *ChatView) PopLastMessage() *ChatMessage {
	if len(c.messages) == 0 {
		return nil
	}
	last := c.messages[len(c.messages)-1]
	c.messages = c.messages[:len(c.messages)-1]
	c.renderContent()
	c.vp.GotoBottom()
	return &last
}

// Clear removes all messages.
func (c *ChatView) Clear() {
	c.messages = nil
	c.renderContent()
}

// Height returns the viewport height.
func (c *ChatView) Height() int {
	return c.height
}

// Width returns the viewport width.
func (c *ChatView) Width() int {
	return c.width
}

// renderContent rebuilds the viewport content from messages.
func (c *ChatView) renderContent() {
	var b strings.Builder
	for i, msg := range c.messages {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.formatMessage(msg))
	}
	c.vp.SetContent(b.String())
}

// formatMessage renders a single message with appropriate styling.
func (c *ChatView) formatMessage(msg ChatMessage) string {
	innerW := c.width - 4
	if innerW < 10 {
		innerW = 80
	}

	switch msg.Role {
	case "user":
		label := UserMsgStyle.Render("You:")
		content := renderPlain(msg.Content, innerW)
		return lipgloss.JoinVertical(lipgloss.Left, label, content)

	case "assistant":
		label := AssistantMsgStyle.Render("Assistant:")
		content := renderMarkdown(msg.Content, innerW)
		return lipgloss.JoinVertical(lipgloss.Left, label, content)

	case "system":
		label := SystemMsgStyle.Render("System:")
		content := renderPlain(msg.Content, innerW)
		return lipgloss.JoinVertical(lipgloss.Left, label, content)

	case "error":
		label := ErrorMsgStyle.Render("Error:")
		content := renderPlain(msg.Content, innerW)
		return lipgloss.JoinVertical(lipgloss.Left, label, content)

	case "reasoning":
		return ReasoningStyle.Render(msg.Content)

	case "tool_call":
		return c.formatToolCard(msg.ToolCall)

	default:
		return renderMarkdown(msg.Content, innerW)
	}
}

// formatToolCard renders a tool invocation card like Reasonix's style:
//
//	● Read(path/to/file)          ← header with verb + primary arg
//	  ⎿  (tool output lines…)     ← connector block for output
func (c *ChatView) formatToolCard(tc *ToolCallInfo) string {
	if tc == nil {
		return ""
	}

	verb := toolVerb(tc.Name)
	dot := lipgloss.NewStyle().Foreground(toolDotColor(tc.Name)).Render("●")
	header := fmt.Sprintf("%s %s(%s)", dot, verb, tc.Args)

	cardW := c.width - 4
	if cardW < 10 {
		cardW = 80
	}
	header = ToolCardHeaderStyle.Render(clampLine(header, cardW))

	// Build the result block
	var lines []string
	if tc.Streaming {
		// Show bounded tail of output with working indicator
		outputLines := strings.Split(strings.TrimRight(tc.Result, "\n"), "\n")
		if len(outputLines) > 0 && (len(outputLines) > 1 || outputLines[0] != "") {
			tail := outputLines
			if len(tail) > 8 {
				tail = tail[len(tail)-8:]
			}
			for _, ln := range tail {
				w := cardW - connectorWidth
				if w < 1 {
					w = 1
				}
				lines = append(lines, dimLine(clampLine(ln, w)))
			}
		} else {
			// No output yet — show working animation placeholder
			elapsed := "-"
			if tc.StartTime != "" {
				elapsed = tc.StartTime
			}
			lines = append(lines, dimLine(fmt.Sprintf("working · %ss", elapsed)))
		}
	} else {
		// Finalized: show summary
		if tc.IsError {
			lines = append(lines, dimLine(fmt.Sprintf("duration · error")))
		} else if tc.Lines > 0 {
			lines = append(lines, dimLine(fmt.Sprintf("%d lines", tc.Lines)))
		} else {
			lines = append(lines, dimLine("done"))
		}
		resultPreview := strings.TrimSpace(tc.Result)
		if resultPreview != "" && len(resultPreview) > 200 {
			resultPreview = resultPreview[:200] + "..."
		}
		if resultPreview != "" {
			for _, ln := range strings.Split(resultPreview, "\n") {
				w := cardW - connectorWidth
				if w < 1 {
					w = 1
				}
				lines = append(lines, dimLine(clampLine(ln, w)))
			}
		}
	}

	result := ""
	if len(lines) > 0 {
		result = "\n" + ConnectorBlock(lines)
	}

	return header + result
}

// ── View ──────────────────────────────────────────────────────────────────────

// View returns the rendered viewport content.
func (c *ChatView) View() string {
	if !c.ready {
		return "Loading..."
	}
	return c.vp.View()
}

// Update handles viewport navigation messages.
func (c *ChatView) Update(msg tea.Msg) bool {
	_, ok := msg.(tea.KeyMsg)
	if !ok {
		return false
	}
	km := msg.(tea.KeyMsg)
	switch km.String() {
	case "pgup":
		c.vp.ScrollUp(c.height / 2)
		return true
	case "pgdown":
		c.vp.ScrollDown(c.height / 2)
		return true
	case "home":
		c.vp.GotoTop()
		return true
	case "end":
		c.vp.GotoBottom()
		return true
	}
	return false
}

// ── Markdown rendering ───────────────────────────────────────────────────────

// renderMarkdown renders markdown content in the terminal.
func renderMarkdown(text string, width int) string {
	if width < 10 {
		width = 80
	}

	var result strings.Builder
	lines := strings.Split(text, "\n")
	inCodeBlock := false
	var codeBlockLang string
	var codeLines []string

	flushCodeBlock := func() {
		if len(codeLines) == 0 {
			return
		}
		maxLen := 0
		for _, l := range codeLines {
			w := displayWidth(l)
			if w > maxLen {
				maxLen = w
			}
		}
		codeWidth := maxLen + 4
		if codeWidth > width-2 {
			codeWidth = width - 2
		}

		result.WriteString("\n")
		for _, line := range codeLines {
			truncated := line
			if displayWidth(truncated) > codeWidth-4 {
				truncated = truncateWidth(truncated, codeWidth-7) + "..."
			}
			result.WriteString(CodeBlockStyle.Render("  " + truncated))
			result.WriteString("\n")
		}
		codeLines = nil
	}

	for _, line := range lines {
		trimmed := line

		// Code block handling
		if strings.HasPrefix(trimmed, "```") {
			if inCodeBlock {
				flushCodeBlock()
				inCodeBlock = false
				codeBlockLang = ""
			} else {
				inCodeBlock = true
				codeBlockLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			}
			continue
		}

		if inCodeBlock {
			codeLines = append(codeLines, trimmed)
			continue
		}

		// Inline code patterns
		codeRe := regexp.MustCompile("`([^`]+)`")
		line = codeRe.ReplaceAllString(line, CodeBlockStyle.Render("$1"))

		// Bold: **text**
		boldRe := regexp.MustCompile(`\*\*(.+?)\*\*`)
		line = boldRe.ReplaceAllString(line, lipgloss.NewStyle().Bold(true).Render("$1"))

		// Wrap text to width
		if displayWidth(line) > width {
			line = truncateWidth(line, width-1)
		}

		result.WriteString(line)
		result.WriteString("\n")
	}
	if inCodeBlock {
		flushCodeBlock()
	}

	return strings.TrimRight(result.String(), "\n")
}

// renderPlain renders plain text, wrapping long lines.
func renderPlain(text string, width int) string {
	if width < 10 {
		width = 80
	}
	var result strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if displayWidth(line) > width {
			result.WriteString(truncateWidth(line, width-1))
		} else {
			result.WriteString(line)
		}
		result.WriteString("\n")
	}
	return strings.TrimRight(result.String(), "\n")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// toolVerb returns a human-readable verb for a tool name.
func toolVerb(name string) string {
	switch name {
	case "bash":
		return "Bash"
	case "read_file":
		return "Read"
	case "write_file":
		return "Write"
	case "edit_file":
		return "Update"
	case "list_files":
		return "List"
	case "glob":
		return "Glob"
	case "grep", "search":
		return "Search"
	case "web_fetch":
		return "Fetch"
	default:
		return name
	}
}

// clampLine truncates a line to fit within width, preserving ANSI.
func clampLine(s string, w int) string {
	if displayWidth(s) <= w {
		return s
	}
	return truncateWidth(s, w-1) + "…"
}

// dimLine renders a line as dim/muted.
func dimLine(s string) string {
	return lipgloss.NewStyle().Foreground(ColorMuted).Render(s)
}

// ── Width helpers (no external dep needed) ────────────────────────────────────

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func displayWidth(s string) int {
	plain := ansiRE.ReplaceAllString(s, "")
	count := 0
	for _, r := range plain {
		if r > 0x2FF && r < 0x3000 {
			count += 2
		} else if r >= 0x3000 && r <= 0x9FFF {
			count += 2
		} else if r >= 0xFF01 && r <= 0xFF60 {
			count += 2
		} else if r >= 0xFFE0 && r <= 0xFFE6 {
			count += 2
		} else if r >= 0x1F300 && r <= 0x1F9FF {
			count += 2
		} else {
			count++
		}
	}
	return count
}

func truncateWidth(s string, maxWidth int) string {
	plain := ansiRE.ReplaceAllString(s, "")
	count := 0
	result := ""
	for _, r := range plain {
		w := 1
		if r > 0x2FF && r < 0x3000 {
			w = 2
		} else if r >= 0x3000 && r <= 0x9FFF {
			w = 2
		} else if r >= 0xFF01 && r <= 0xFF60 {
			w = 2
		} else if r >= 0xFFE0 && r <= 0xFFE6 {
			w = 2
		} else if r >= 0x1F300 && r <= 0x1F9FF {
			w = 2
		}
		if count+w > maxWidth {
			break
		}
		result += string(r)
		count += w
	}
	return result
}
