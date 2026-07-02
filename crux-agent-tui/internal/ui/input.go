package ui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

// SubmitMsg is sent when the user presses Enter to submit input.
type SubmitMsg string

// CompletionItem is a single autocomplete suggestion.
type CompletionItem struct {
	Label       string // displayed text
	Insert      string // text to insert on accept
	Hint        string // description / hint
	IsDirectory bool   // true if this item is a directory (for @ completion)
}

// InputView provides a text input bar at the bottom of the screen.
// Supports:
//   - Cursor movement (←/→, Ctrl+←/→)
//   - Multi-line input via Alt+Enter
//   - Input history (↑/↓)
//   - /command autocomplete menu
//   - Ctrl+U clear line
//   - Ctrl+W delete word
//   - Auto-height up to maxInputRows
type InputView struct {
	width     int
	disabled  bool
	focused   bool
	prompt    string
	imageHint string
	input     string

	// Cursor position in runes (0 = beginning)
	cursor int

	// Input history
	history       []string
	historyCursor int    // -1 means not browsing history
	historyDraft  string // saved current input when starting to browse

	// Autocomplete
	completionItems  []CompletionItem
	completionSel    int
	completionActive bool
	completionPrefix string
	completionType   int // 0 = none, 1 = slash command, 2 = at file path

	// Multi-line
	altEnter bool // true if Enter was preceded by Alt

	// Max rows the input box can grow to
	maxInputRows int
}

// NewInputView creates a new input view.
func NewInputView() *InputView {
	return &InputView{
		focused:      true,
		disabled:     false,
		prompt:       "You:",
		maxInputRows: 8,
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

// SetValue sets the input text and moves cursor to end.
func (iv *InputView) SetValue(v string) {
	iv.input = v
	iv.cursor = len([]rune(v))
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

// ── Cursor ────────────────────────────────────────────────────────────────────

// Cursor returns the cursor position in runes.
func (iv *InputView) Cursor() int { return iv.cursor }

// CursorLeft moves the cursor left by one rune.
func (iv *InputView) CursorLeft() {
	if iv.cursor > 0 {
		iv.cursor--
	}
}

// CursorRight moves the cursor right by one rune.
func (iv *InputView) CursorRight() {
	runes := []rune(iv.input)
	if iv.cursor < len(runes) {
		iv.cursor++
	}
}

// CursorWordLeft moves the cursor to the start of the previous word.
func (iv *InputView) CursorWordLeft() {
	runes := []rune(iv.input)
	if iv.cursor <= 0 {
		return
	}
	// Skip current word boundary
	pos := iv.cursor - 1
	for pos > 0 && unicode.IsSpace(runes[pos]) {
		pos--
	}
	for pos > 0 && !unicode.IsSpace(runes[pos]) {
		pos--
	}
	if pos == 0 && !unicode.IsSpace(runes[pos]) {
		iv.cursor = 0
	} else {
		iv.cursor = pos + 1
	}
}

// CursorWordRight moves the cursor to the start of the next word.
func (iv *InputView) CursorWordRight() {
	runes := []rune(iv.input)
	end := len(runes)
	if iv.cursor >= end {
		return
	}
	pos := iv.cursor
	// Skip current word
	for pos < end && !unicode.IsSpace(runes[pos]) {
		pos++
	}
	// Skip spaces
	for pos < end && unicode.IsSpace(runes[pos]) {
		pos++
	}
	iv.cursor = pos
}

// CursorHome moves the cursor to the beginning.
func (iv *InputView) CursorHome() {
	iv.cursor = 0
}

// CursorEnd moves the cursor to the end.
func (iv *InputView) CursorEnd() {
	iv.cursor = len([]rune(iv.input))
}

// VisibleRows returns the number of rows the input text occupies.
// The minimum is 1 (even for empty input).
func (iv *InputView) VisibleRows() int {
	if iv.width <= 0 {
		return 1
	}
	// Account for border (2 chars) + prompt + padding
	lineW := iv.width - 6 // box border(2) + padding(2) + prompt space
	if lineW < 1 {
		lineW = 1
	}
	lines := strings.Split(iv.input, "\n")
	total := 0
	for _, l := range lines {
		w := displayWidth(l)
		if w == 0 {
			total++ // empty line still takes a row
		} else {
			total += (w + lineW - 1) / lineW // ceil division
		}
	}
	if total > iv.maxInputRows {
		total = iv.maxInputRows
	}
	if total < 1 {
		total = 1
	}
	return total
}

// ── Text manipulation ─────────────────────────────────────────────────────────

// AppendRune appends a rune at the cursor position.
func (iv *InputView) AppendRune(r rune) {
	runes := []rune(iv.input)
	pos := iv.cursor
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}
	// Insert at position
	newRunes := make([]rune, 0, len(runes)+1)
	newRunes = append(newRunes, runes[:pos]...)
	newRunes = append(newRunes, r)
	newRunes = append(newRunes, runes[pos:]...)
	iv.input = string(newRunes)
	iv.cursor = pos + 1
}

// DeleteLast removes the character before the cursor.
func (iv *InputView) DeleteLast() {
	runes := []rune(iv.input)
	if iv.cursor <= 0 || len(runes) == 0 {
		return
	}
	pos := iv.cursor
	newRunes := make([]rune, 0, len(runes)-1)
	newRunes = append(newRunes, runes[:pos-1]...)
	newRunes = append(newRunes, runes[pos:]...)
	iv.input = string(newRunes)
	iv.cursor = pos - 1
}

// DeleteForward removes the character at the cursor position (Delete key).
func (iv *InputView) DeleteForward() {
	runes := []rune(iv.input)
	if iv.cursor >= len(runes) {
		return
	}
	newRunes := make([]rune, 0, len(runes)-1)
	newRunes = append(newRunes, runes[:iv.cursor]...)
	newRunes = append(newRunes, runes[iv.cursor+1:]...)
	iv.input = string(newRunes)
}

// DeleteWordBackward deletes the word before the cursor (Ctrl+W).
func (iv *InputView) DeleteWordBackward() {
	runes := []rune(iv.input)
	if iv.cursor <= 0 {
		return
	}
	pos := iv.cursor - 1
	// Skip spaces
	for pos > 0 && unicode.IsSpace(runes[pos]) {
		pos--
	}
	// Skip word
	for pos > 0 && !unicode.IsSpace(runes[pos]) {
		pos--
	}
	delStart := pos
	if pos == 0 && !unicode.IsSpace(runes[pos]) {
		delStart = 0
	} else if unicode.IsSpace(runes[pos]) {
		delStart = pos + 1
	} else {
		delStart = pos + 1
	}
	newRunes := make([]rune, 0, len(runes)-(iv.cursor-delStart))
	newRunes = append(newRunes, runes[:delStart]...)
	newRunes = append(newRunes, runes[iv.cursor:]...)
	iv.input = string(newRunes)
	iv.cursor = delStart
}

// ClearInput clears the input text.
func (iv *InputView) ClearInput() {
	iv.input = ""
	iv.cursor = 0
	iv.completionActive = false
	iv.completionItems = nil
	iv.completionType = 0
}

// InsertAtCursor inserts text at the cursor position.
func (iv *InputView) InsertAtCursor(text string) {
	runes := []rune(iv.input)
	pos := iv.cursor
	textRunes := []rune(text)
	newRunes := make([]rune, 0, len(runes)+len(textRunes))
	newRunes = append(newRunes, runes[:pos]...)
	newRunes = append(newRunes, textRunes...)
	newRunes = append(newRunes, runes[pos:]...)
	iv.input = string(newRunes)
	iv.cursor = pos + len(textRunes)
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
func (iv *InputView) RecallPrevious() bool {
	if len(iv.history) == 0 {
		return false
	}
	if iv.historyCursor < 0 {
		iv.historyDraft = iv.input
		iv.historyCursor = len(iv.history) - 1
	} else if iv.historyCursor > 0 {
		iv.historyCursor--
	}
	iv.SetValue(iv.history[iv.historyCursor])
	return true
}

// RecallNext moves forward in history.
func (iv *InputView) RecallNext() bool {
	if iv.historyCursor < 0 || len(iv.history) == 0 {
		return false
	}
	iv.historyCursor++
	if iv.historyCursor >= len(iv.history) {
		iv.historyCursor = -1
		iv.SetValue(iv.historyDraft)
		iv.historyDraft = ""
	} else {
		iv.SetValue(iv.history[iv.historyCursor])
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
// ct is the completion type: 0 = auto-detect, 1 = slash command, 2 = @ file path.
func (iv *InputView) SetCompletionItems(items []CompletionItem, ct int) {
	if len(items) > 0 {
		iv.completionItems = items
		iv.completionSel = 0
		iv.completionActive = true
		iv.completionType = ct
	} else {
		iv.completionActive = false
		iv.completionItems = nil
		iv.completionType = 0
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

// CompletionType returns the completion type:
// 0 = none, 1 = slash command, 2 = @ file path.
func (iv *InputView) CompletionType() int {
	if !iv.completionActive {
		return 0
	}
	return iv.completionType
}

// MoveCompletion moves the selection by delta.
func (iv *InputView) MoveCompletion(delta int) {
	n := len(iv.completionItems)
	if n == 0 {
		return
	}
	iv.completionSel = (iv.completionSel + delta + n) % n
}

// AcceptCompletion replaces the input with the selected item.
// Returns true if the completion was accepted.
// For @ file path completions, directories are kept open (trailing /) while
// files are accepted with a trailing space.
func (iv *InputView) AcceptCompletion() bool {
	if !iv.completionActive || len(iv.completionItems) == 0 {
		return false
	}
	item := iv.completionItems[iv.completionSel]

	if iv.completionType == 2 && item.IsDirectory {
		// Directory: insert path with trailing / and keep completion open
		// by replacing the last @-based token
		iv.SetValue(item.Insert + "/")
		// After inserting a directory, we'll trigger a refresh from App
		// via the next UpdateCompletion call
		return true
	}

	// File or slash command: accept with trailing space
	iv.SetValue(item.Insert + " ")
	iv.completionActive = false
	iv.completionItems = nil
	iv.completionType = 0
	return true
}

// DismissCompletion closes the completion menu.
func (iv *InputView) DismissCompletion() {
	iv.completionActive = false
	iv.completionItems = nil
	iv.completionType = 0
}

// UpdateCompletion checks if completion should be active.
// It detects both /commands and @file paths.
func (iv *InputView) UpdateCompletion() {
	if strings.HasPrefix(iv.input, "/") {
		// Already handled by slash commands — prefix is the whole input
		iv.completionPrefix = iv.input
		return
	}
	// Check for @ trigger — find the last @ symbol in the input
	lastAt := strings.LastIndex(iv.input, "@")
	if lastAt >= 0 {
		// The @ must be at a word boundary (preceded by space or at start)
		if lastAt == 0 || iv.input[lastAt-1] == ' ' {
			iv.completionPrefix = iv.input[lastAt:] // includes @
			iv.completionType = 2                   // @ file path
			// Don't clear items here — App will set them via completion callback
			return
		}
	}
	// No completion pattern detected: dismiss
	iv.completionActive = false
	iv.completionItems = nil
	iv.completionType = 0
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

// ── Completion menu rendering ─────────────────────────────────────────────────

// CompletionHeight returns the number of rows the completion menu will occupy.
func (iv *InputView) CompletionHeight() int {
	if !iv.completionActive || len(iv.completionItems) == 0 {
		return 0
	}
	n := len(iv.completionItems)
	if n > 8 {
		n = 8
	}
	return n + 1 // items + border effect
}

// RenderCompletionMenu renders the autocomplete menu above the input box.
// For @ file path completions, directories are shown with a trailing "/".
// Returns empty string if no menu is active.
func (iv *InputView) RenderCompletionMenu() string {
	if !iv.completionActive || len(iv.completionItems) == 0 {
		return ""
	}
	boxW := iv.width
	if boxW < 10 {
		boxW = 10
	}

	var b strings.Builder
	n := len(iv.completionItems)
	shown := 0
	maxShow := 8

	for i := 0; i < n && shown < maxShow; i++ {
		item := iv.completionItems[i]
		selected := i == iv.completionSel

		label := item.Label
		if item.IsDirectory {
			label += "/"
		}

		if selected {
			// Selected item: accent background + bold
			selText := " " + label
			selBg := lipgloss.NewStyle().
				Background(ColorSelect).
				Foreground(ColorAccent).
				Bold(true).
				Width(boxW - 3)
			b.WriteString(" " + selBg.Render(selText))
			if item.Hint != "" {
				b.WriteString(" " + dimLine(item.Hint))
			}
			b.WriteString("\n")
		} else {
			line := " " + label
			if item.Hint != "" {
				line += dimLine("  " + item.Hint)
			}
			if displayWidth(line) > boxW-2 {
				line = clampLine(line, boxW-4)
			}
			b.WriteString(line + "\n")
		}
		shown++
	}

	// Show "+N more" indicator
	if n > maxShow {
		b.WriteString(" " + todoDimStyle.Render(fmt.Sprintf("  +%d more", n-maxShow)) + "\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// AtCompletionPrefix returns the @-prefixed path currently being completed,
// or empty string if not in @ mode. e.g. "@src/" → "src/".
func (iv *InputView) AtCompletionPath() string {
	if iv.completionType != 2 {
		return ""
	}
	prefix := iv.completionPrefix
	if len(prefix) > 1 {
		return prefix[1:] // strip leading @
	}
	return ""
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

	if iv.disabled {
		displayText := dimLine("(waiting for agent...)")
		return boxStyle.Width(boxW).Render(prompt + displayText)
	}

	// Build the display: prompt + input text with cursor
	displayText := iv.input
	historyLabel := ""
	if iv.IsBrowsingHistory() {
		historyLabel = " " + dimLine("(history)")
	}

	// Cursor: use the terminal cursor (not rendered), but show the text
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
