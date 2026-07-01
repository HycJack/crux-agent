package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// SubmitMsg is sent when the user presses Enter to submit input.
type SubmitMsg string

// CompletionItem is a single autocomplete suggestion.
type CompletionItem struct {
	Label  string // displayed text
	Insert string // text to insert on accept
	Hint   string // description / hint
}

// InputView provides a text input bar at the bottom of the screen.
// Supports:
//   - Input history (↑/↓ to recall previous submissions)
//   - Multi-line via Alt+Enter
//   - /command autocomplete menu
type InputView struct {
	width     int
	disabled  bool
	focused   bool
	prompt    string
	imageHint string
	input     string // current input text

	// Input history
	history       []string
	historyCursor int    // -1 means not browsing history
	historyDraft  string // saved current input when starting to browse

	// Autocomplete
	completionItems  []CompletionItem
	completionSel    int
	completionActive bool
	completionPrefix string

	// Multi-line support
	altEnter bool // true if Enter was preceded by Alt
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
	iv.altEnter = false
}

// Blur removes focus.
func (iv *InputView) Blur() {
	iv.focused = false
}

// Disable prevents input.
func (iv *InputView) Disable() {
	iv.disabled = true
	iv.focused = false
	iv.altEnter = false
}

// Enable allows input.
func (iv *InputView) Enable() {
	iv.disabled = false
	iv.focused = true
	iv.altEnter = false
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

// ── Text manipulation ─────────────────────────────────────────────────────────

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
	iv.completionActive = false
	iv.completionItems = nil
}

// InsertAtCursor inserts text at the cursor position (end of input for now).
func (iv *InputView) InsertAtCursor(text string) {
	iv.input += text
}

// ── Input history ─────────────────────────────────────────────────────────────

// SaveToHistory saves a submitted line into history.
func (iv *InputView) SaveToHistory(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	if len(iv.history) > 0 && iv.history[len(iv.history)-1] == line {
		return // dedup consecutive repeats
	}
	iv.history = append(iv.history, line)
	iv.historyCursor = -1
	iv.historyDraft = ""
}

// RecallPrevious replaces input with the previous history entry.
// Returns true if the cursor moved.
func (iv *InputView) RecallPrevious() bool {
	if len(iv.history) == 0 {
		return false
	}
	if iv.historyCursor < 0 {
		// Save current draft
		iv.historyDraft = iv.input
		iv.historyCursor = len(iv.history) - 1
	} else if iv.historyCursor > 0 {
		iv.historyCursor--
	}
	iv.input = iv.history[iv.historyCursor]
	if iv.historyCursor == 0 {
		// At the earliest entry — don't wrap
	}
	return true
}

// RecallNext moves forward in history (toward the draft).
// Returns true if the cursor moved.
func (iv *InputView) RecallNext() bool {
	if iv.historyCursor < 0 || len(iv.history) == 0 {
		return false
	}
	iv.historyCursor++
	if iv.historyCursor >= len(iv.history) {
		// Back to the draft
		iv.historyCursor = -1
		iv.input = iv.historyDraft
		iv.historyDraft = ""
	} else {
		iv.input = iv.history[iv.historyCursor]
	}
	return true
}

// ResetHistoryRecall clears the history browsing state.
func (iv *InputView) ResetHistoryRecall() {
	iv.historyCursor = -1
	iv.historyDraft = ""
}

// IsBrowsingHistory returns true when the user is browsing history.
func (iv *InputView) IsBrowsingHistory() bool {
	return iv.historyCursor >= 0
}

// ── Autocomplete ──────────────────────────────────────────────────────────────

// SetCompletionItems sets the current autocomplete suggestions.
func (iv *InputView) SetCompletionItems(items []CompletionItem) {
	if len(items) > 0 {
		iv.completionItems = items
		iv.completionSel = 0
		iv.completionActive = true
	} else {
		iv.completionActive = false
		iv.completionItems = nil
	}
}

// CompletionItems returns the current completion items.
func (iv *InputView) CompletionItems() []CompletionItem {
	return iv.completionItems
}

// CompletionSelected returns the selected index.
func (iv *InputView) CompletionSelected() int {
	return iv.completionSel
}

// CompletionActive returns whether the completion menu is open.
func (iv *InputView) CompletionActive() bool {
	return iv.completionActive
}

// MoveCompletion moves the selection by delta.
func (iv *InputView) MoveCompletion(delta int) {
	n := len(iv.completionItems)
	if n == 0 {
		return
	}
	iv.completionSel = (iv.completionSel + delta + n) % n
}

// AcceptCompletion replaces the input prefix with the selected item.
func (iv *InputView) AcceptCompletion() bool {
	if !iv.completionActive || len(iv.completionItems) == 0 {
		return false
	}
	item := iv.completionItems[iv.completionSel]
	iv.input = item.Insert + " "
	iv.completionActive = false
	iv.completionItems = nil
	return true
}

// DismissCompletion closes the completion menu.
func (iv *InputView) DismissCompletion() {
	iv.completionActive = false
	iv.completionItems = nil
}

// UpdateCompletion filters completion items based on the current input.
func (iv *InputView) UpdateCompletion() {
	// Only activate for /commands
	if !strings.HasPrefix(iv.input, "/") {
		iv.completionActive = false
		iv.completionItems = nil
		return
	}
	// The menu items are set externally by the app model
	iv.completionPrefix = iv.input
}

// ── Alt+Enter state ──────────────────────────────────────────────────────────

// SetAltEnter marks that Enter was pressed with Alt.
func (iv *InputView) SetAltEnter() {
	iv.altEnter = true
}

// ClearAltEnter clears the Alt+Enter state.
func (iv *InputView) ClearAltEnter() {
	iv.altEnter = false
}

// AltEntered returns whether Alt+Enter was pressed.
func (iv *InputView) AltEntered() bool {
	return iv.altEnter
}

// ── Rendering ─────────────────────────────────────────────────────────────────

// View returns the rendered input box.
func (iv *InputView) View() string {
	boxW := iv.width
	if boxW < 10 {
		boxW = 10
	}

	var boxStyle lipgloss.Style
	if iv.focused {
		boxStyle = InputFocusedStyle
	} else {
		boxStyle = InputBoxStyle
	}

	prompt := InputPromptStyle.Render(iv.prompt + " ")

	displayText := iv.input
	if iv.disabled {
		displayText = dimLine("(waiting for agent...)")
	}

	// Show history indicator
	historyLabel := ""
	if iv.IsBrowsingHistory() {
		historyLabel = dimLine(" (history) ")
	}

	// Combine
	content := prompt + displayText + historyLabel + iv.imageHint
	return boxStyle.Width(boxW).Render(content)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
