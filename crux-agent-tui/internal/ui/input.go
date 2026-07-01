package ui

import (
	"github.com/charmbracelet/lipgloss"
)

// SubmitMsg is sent when the user presses Enter to submit input.
type SubmitMsg string

// InputView provides a text input bar at the bottom of the screen.
// It uses a simple input field. Submit is handled by the parent model
// by intercepting tea.KeyEnter before it reaches the input widget.
type InputView struct {
	width     int
	disabled  bool
	focused   bool
	prompt    string
	imageHint string
	input     string // current input text (we track it ourselves)
}

// NewInputView creates a new input view.
func NewInputView() *InputView {
	return &InputView{
		focused:  true,
		disabled: false,
		prompt:   "You:",
	}
}

// SetSize updates the width.
func (iv *InputView) SetSize(w int) {
	iv.width = w
}

// Focused returns whether input is focused.
func (iv *InputView) Focused() bool { return iv.focused }

// Focus sets focus.
func (iv *InputView) Focus() {
	iv.focused = true
	iv.disabled = false
}

// Blur removes focus.
func (iv *InputView) Blur() {
	iv.focused = false
}

// Disable prevents input.
func (iv *InputView) Disable() {
	iv.disabled = true
	iv.focused = false
}

// Enable allows input.
func (iv *InputView) Enable() {
	iv.disabled = false
	iv.focused = true
}

// SetValue sets the input text.
func (iv *InputView) SetValue(v string) {
	iv.input = v
}

// Value returns the current input text.
func (iv *InputView) Value() string {
	return iv.input
}

// SetImageHint sets the staged image count hint.
func (iv *InputView) SetImageHint(count int) {
	if count > 0 {
		iv.imageHint = ToolInfoStyle.Render("📎 " + itoa(count) + " image(s)")
	} else {
		iv.imageHint = ""
	}
}

// AppendRune appends a rune to the input.
func (iv *InputView) AppendRune(r rune) {
	iv.input += string(r)
}

// DeleteLast removes the last character.
func (iv *InputView) DeleteLast() {
	if len(iv.input) > 0 {
		runes := []rune(iv.input)
		iv.input = string(runes[:len(runes)-1])
	}
}

// ClearInput clears the input text.
func (iv *InputView) ClearInput() {
	iv.input = ""
}

// itoa converts an integer to a string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []rune{}
	for n > 0 {
		digits = append([]rune{rune('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// View renders the input bar.
func (iv *InputView) View() string {
	prompt := InputPromptStyle.Render(iv.prompt + " ")

	var inputContent string
	if iv.focused && !iv.disabled {
		// Show cursor at the end
		inputContent = InputFocusedStyle.
			Width(iv.width - lipgloss.Width(prompt) - 2).
			Render(iv.input + "█")
	} else {
		inputContent = InputBoxStyle.
			Width(iv.width - lipgloss.Width(prompt) - 2).
			Render(iv.input)
	}

	hint := ""
	if iv.imageHint != "" {
		hint = " " + iv.imageHint
	}

	return lipgloss.JoinHorizontal(lipgloss.Bottom, prompt, hint, inputContent)
}
