package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ChatMessage represents a single message in the conversation.
type ChatMessage struct {
	Role    string // "user", "assistant", "system", "error", "thinking", "tool_call", "tool_result"
	Content string
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
		c.vp.KeyMap.PageDown.SetEnabled(false)
		c.vp.KeyMap.PageUp.SetEnabled(false)
		c.vp.KeyMap.HalfPageDown.SetEnabled(false)
		c.vp.KeyMap.HalfPageUp.SetEnabled(false)
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
// Used for streaming text updates.
func (c *ChatView) UpdateLastMessage(content string) {
	if len(c.messages) == 0 {
		return
	}
	c.messages[len(c.messages)-1].Content = content
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

// SetMessages replaces the entire message list.
func (c *ChatView) SetMessages(msgs []ChatMessage) {
	c.messages = msgs
	c.renderContent()
	c.vp.GotoBottom()
}

// Messages returns a copy of the message list.
func (c *ChatView) Messages() []ChatMessage {
	out := make([]ChatMessage, len(c.messages))
	copy(out, c.messages)
	return out
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
	w := c.width
	if w < 10 {
		w = 80
	}
	contentWidth := w - 2

	switch msg.Role {
	case "user":
		label := UserMsgStyle.Render("You:")
		content := wrapString(msg.Content, contentWidth)
		return lipgloss.JoinVertical(lipgloss.Left, label, content)

	case "assistant":
		label := AssistantMsgStyle.Copy().Bold(true).Foreground(ColorAccent).Render("Assistant:")
		content := wrapString(msg.Content, contentWidth)
		return lipgloss.JoinVertical(lipgloss.Left, label, content)

	case "system":
		label := SystemMsgStyle.Render("System:")
		content := wrapString(msg.Content, contentWidth)
		return lipgloss.JoinVertical(lipgloss.Left, label, content)

	case "error":
		label := ErrorMsgStyle.Render("Error:")
		content := wrapString(msg.Content, contentWidth)
		return lipgloss.JoinVertical(lipgloss.Left, label, content)

	case "thinking":
		return ThinkingStyle.Render("💭 " + msg.Content)

	case "tool_call":
		return ToolCallStyle.Render("🔧 " + msg.Content)

	case "tool_result":
		return ToolResultStyle.Render("  " + msg.Content)

	default:
		return wrapString(msg.Content, contentWidth)
	}
}

// View returns the rendered viewport content.
func (c *ChatView) View() string {
	if !c.ready {
		return "Loading..."
	}
	return c.vp.View()
}

// Update handles viewport navigation messages.
func (c *ChatView) Update(msg tea.Msg) (*ChatView, tea.Cmd) {
	var cmd tea.Cmd
	c.vp, cmd = c.vp.Update(msg)
	return c, cmd
}

// wrapString wraps text to a given width, preserving existing newlines.
func wrapString(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	var result []string
	for _, line := range lines {
		if len(line) <= maxWidth {
			result = append(result, line)
			continue
		}
		for len(line) > maxWidth {
			result = append(result, line[:maxWidth])
			line = line[maxWidth:]
		}
		if len(line) > 0 {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

// Helper to format tool call args for display.
func FormatToolCall(name string, argsJSON string) string {
	// Trim the args to show something useful
	display := argsJSON
	if len(display) > 200 {
		display = display[:200] + "..."
	}
	return fmt.Sprintf("%s(%s)", name, display)
}
