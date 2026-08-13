package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/quick"
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
	Role      string // "user", "assistant", "system", "error", "reasoning", "tool_call"
	Content   string
	ToolCall  *ToolCallInfo // non-nil only for tool_call role
	Streaming bool          // true while an assistant message is still accumulating tokens
}

// ── Selection ─────────────────────────────────────────────────────────────────

// selPos is a caret position: a content-line index and a visual column.
type selPos struct {
	line int
	col  int
}

// selection tracks a text selection over the transcript content.
type selection struct {
	active       bool
	anchor, head selPos // anchor = drag start, head = current
}

// stableAnchor returns true when the anchor should stay fixed (mouse drag).
func (s selection) ordered() (start, end selPos) {
	if s.anchor.line > s.head.line || (s.anchor.line == s.head.line && s.anchor.col > s.head.col) {
		return s.head, s.anchor
	}
	return s.anchor, s.head
}

func (s selection) empty() bool {
	return !s.active || (s.anchor.line == s.head.line && s.anchor.col == s.head.col)
}

// ── Scrollbar ─────────────────────────────────────────────────────────────────

// scrollbarThumb computes the scrollbar thumb position and size for a viewport
// showing `h` lines out of `total` lines, scrolled to `yoff`.
func scrollbarThumb(h, yoff, total int) (thumbStart, thumbSize int) {
	if total <= h {
		return 0, h // fully visible → full bar
	}
	thumbSize = max(1, h*h/total)
	thumbStart = yoff * (h - thumbSize) / (total - h)
	return
}

// ── ChatView ──────────────────────────────────────────────────────────────────

// ChatView displays the conversation history as a scrollable list with
// mouse-drag text selection and a custom scrollbar.
//
// Performance design (incremental rebuild):
//   - messageRenders[i] caches the pre-rendered ANSI string for messages[i].
//     Only dirty flags trigger full rebuilds (e.g. window resize).
//   - UpdateLastMessage / UpdateLastToolOutput re-render only the last message
//     instead of all messages.
//   - rebuild() does a full re-render (only when width changes or messages
//     are reordered).
//   - wrapContent() uses a cached lipgloss.Style that is only recreated when
//     contentWidth actually changes.
type ChatView struct {
	messages       []ChatMessage
	messageRenders []string // cached pre-rendered content for each message
	width          int
	height         int
	ready          bool
	dirty          bool // true if full rebuild needed (width change, pop, clear)

	// Content management
	content    string     // concatenation of messageRenders (built only on full rebuild)
	msgWrapped [][]string // wrapped lines per message (streaming splice source)
	msgStart   []int      // msgStart[i] = flat line index where message i's lines begin

	// Scrolling
	yoff  int // first visible line index
	total int // total wrapped lines

	// Selection
	sel     selection
	dragCol int // column being dragged at (-1 when not dragging)

	// Scrollbar
	showScrollbar bool

	// Cached contentWidth for wrapStyle cache
	lastCW int

	// Streaming markdown cache: holds incremental render state for the current
	// streaming assistant message so each token re-renders only the delta
	// instead of re-parsing the whole accumulated content.
	stream *streamMDState

	// Transcript top offset (set by App.View for mouse coordinate mapping)
	transcriptY int
}

// NewChatView creates a new chat viewport.
func NewChatView() *ChatView {
	return &ChatView{
		showScrollbar: true,
	}
}

// ── Size ──────────────────────────────────────────────────────────────────────

// SetSize updates the viewport dimensions and marks dirty for full rebuild.
func (c *ChatView) SetSize(w, h int) {
	if c.width != w {
		c.dirty = true
	}
	c.width = w
	c.height = h
	c.ready = true
	c.rebuildIfNeeded()
}

// ── Message management ────────────────────────────────────────────────────────

// AddMessage appends a new message and scrolls to bottom.
// Only the new message is rendered and wrapped; earlier messages are untouched.
func (c *ChatView) AddMessage(msg ChatMessage) {
	c.messages = append(c.messages, msg)
	// A new message means the previous streaming message finished accumulating;
	// drop its incremental render state so the next stream starts fresh.
	c.stream = nil
	// Only render the new message incrementally
	rendered := c.formatMessage(msg)
	c.messageRenders = append(c.messageRenders, rendered)
	lines := c.wrapMessage(rendered)
	c.msgWrapped = append(c.msgWrapped, lines)
	c.total += len(lines)
	c.recomputeMsgStart()
	c.GotoBottom()
}

// UpdateLastMessage updates the content of the most recent message (incremental).
// Only the last message's render + wrapped lines are recomputed; earlier
// messages are left untouched (no full transcript re-wrap).
func (c *ChatView) UpdateLastMessage(content string) {
	if len(c.messages) == 0 {
		return
	}
	c.messages[len(c.messages)-1].Content = content
	// Incremental: only re-render the last message
	c.messageRenders[len(c.messageRenders)-1] = c.formatMessage(c.messages[len(c.messages)-1])
	c.spliceLast()
	c.GotoBottom()
}

// ReplaceLastMessage replaces the last message entirely (incremental).
func (c *ChatView) ReplaceLastMessage(msg ChatMessage) {
	if len(c.messages) == 0 {
		c.AddMessage(msg)
		return
	}
	c.messages[len(c.messages)-1] = msg
	c.messageRenders[len(c.messageRenders)-1] = c.formatMessage(msg)
	c.spliceLast()
	c.GotoBottom()
}

// UpdateLastToolOutput updates the output of the last tool_call message (incremental).
func (c *ChatView) UpdateLastToolOutput(chunk string) {
	if len(c.messages) == 0 {
		return
	}
	last := &c.messages[len(c.messages)-1]
	if last.ToolCall != nil {
		last.ToolCall.Result += chunk
		last.ToolCall.Lines = strings.Count(last.ToolCall.Result, "\n")
	}
	// Incremental: only re-render last message
	c.messageRenders[len(c.messageRenders)-1] = c.formatMessage(c.messages[len(c.messages)-1])
	c.spliceLast()
	c.GotoBottom()
}

// PopLastMessage removes the most recent message and returns it (incremental).
func (c *ChatView) PopLastMessage() *ChatMessage {
	if len(c.messages) == 0 {
		return nil
	}
	last := c.messages[len(c.messages)-1]
	c.messages = c.messages[:len(c.messages)-1]
	c.messageRenders = c.messageRenders[:len(c.messageRenders)-1]
	// The last message's lines start at its msgStart entry; drop the tail.
	c.msgWrapped = c.msgWrapped[:len(c.msgWrapped)-1]
	if len(c.msgWrapped) > 0 {
		c.total = c.msgStart[len(c.msgWrapped)-1] + len(c.msgWrapped[len(c.msgWrapped)-1])
	} else {
		c.total = 0
	}
	c.msgStart = c.msgStart[:len(c.msgStart)-1]
	c.stream = nil
	c.GotoBottom()
	return &last
}

// Clear removes all messages (full rebuild).
func (c *ChatView) Clear() {
	c.messages = nil
	c.messageRenders = nil
	c.msgWrapped = nil
	c.msgStart = nil
	c.sel = selection{}
	c.stream = nil
	c.content = ""
	c.total = 0
}

// Width returns the viewport width.  Returns the viewport width.

// Height returns the viewport height.
func (c *ChatView) Height() int { return c.height }

func (c *ChatView) Width() int { return c.width }

// ── Content rebuild (incremental) ─────────────────────────────────────────────

// contentWidth returns the usable content width (excluding scrollbar).
func (c *ChatView) contentWidth() int {
	cw := c.width
	if c.showScrollbar {
		cw-- // one column for the scrollbar
	}
	if cw < 1 {
		cw = 1
	}
	return cw
}

// rebuildIfNeeded triggers a full rebuild if the view is dirty or width changed.
func (c *ChatView) rebuildIfNeeded() {
	if !c.dirty && c.lastCW == c.contentWidth() {
		return
	}
	c.dirty = false
	c.fullRebuild()
}

// fullRebuild re-renders all messages from scratch (expensive, called on resize or pop/clear).
func (c *ChatView) fullRebuild() {
	renders := make([]string, len(c.messages))
	for i, msg := range c.messages {
		renders[i] = c.formatMessage(msg)
	}
	c.messageRenders = renders
	c.rebuildContentFromRenders()
}

// rebuildContentFromRenders re-wraps every message's render from scratch.
// Called only on full rebuild (resize, pop-of-middle, clear) — never on the
// per-token streaming path.
func (c *ChatView) rebuildContentFromRenders() {
	if len(c.messageRenders) == 0 {
		c.content = ""
		c.msgWrapped = nil
		c.msgStart = nil
		c.total = 0
		return
	}
	// Estimate capacity to reduce allocations
	est := len(c.messageRenders[0]) * len(c.messageRenders)
	var b strings.Builder
	b.Grow(est)
	wrapped := make([][]string, 0, len(c.messageRenders))
	total := 0
	for i, r := range c.messageRenders {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(r)
		lines := c.wrapMessage(r)
		wrapped = append(wrapped, lines)
		total += len(lines)
	}
	c.content = b.String()
	c.msgWrapped = wrapped
	c.total = total
	c.recomputeMsgStart()
}

// wrapMessage wraps a single message's rendered ANSI into lines using the
// current content width. Wrapping per-message and joining is output-identical
// to wrapping the concatenation of messages, because every message is joined
// with a "\n" (a hard wrap point) — see tests.
func (c *ChatView) wrapMessage(render string) []string {
	cw := c.contentWidth()
	c.lastCW = cw
	if cw <= 0 {
		return nil
	}
	ws := getWrapStyle(cw)
	return strings.Split(ws.Render(render), "\n")
}

// recomputeMsgStart rebuilds the cumulative per-message line offsets from
// msgWrapped lengths. O(number of messages), never called per streaming token.
func (c *ChatView) recomputeMsgStart() {
	c.msgStart = make([]int, len(c.msgWrapped))
	acc := 0
	for i, m := range c.msgWrapped {
		c.msgStart[i] = acc
		acc += len(m)
	}
}

// lineAt returns the wrapped line at the given absolute line index, or "" if
// out of range. Random access via the cumulative per-message offsets avoids
// maintaining a flattened copy that would have to be re-copied on every token.
func (c *ChatView) lineAt(idx int) string {
	if idx < 0 || idx >= c.total {
		return ""
	}
	// Backwards scan: streaming appends land near the tail, so this is cheap.
	for i := len(c.msgStart) - 1; i >= 0; i-- {
		if idx >= c.msgStart[i] {
			off := idx - c.msgStart[i]
			if off < len(c.msgWrapped[i]) {
				return c.msgWrapped[i][off]
			}
			return ""
		}
	}
	return ""
}

// spliceLast re-wraps only the last message's line slice in place. Earlier
// messages' line slices and offsets are never touched, so per-token streaming
// cost does not grow with the size of a fixed transcript.
func (c *ChatView) spliceLast() {
	last := len(c.msgWrapped) - 1
	if last < 0 {
		return
	}
	// Re-wrap only the last message's render.
	c.msgWrapped[last] = c.wrapMessage(c.messageRenders[last])
	// msgStart[last] still points at the (unchanged) lines before it; recompute
	// the total from the per-message lengths. O(number of messages), not lines.
	total := 0
	for _, m := range c.msgWrapped {
		total += len(m)
	}
	c.total = total
}

// getWrapStyle returns a cached lipgloss.Style for a given width.
// Avoids allocating a new Style on every wrapContent call.
var (
	wrapStyleCache lipgloss.Style
	wrapStyleCW    int
)

func getWrapStyle(cw int) lipgloss.Style {
	if cw != wrapStyleCW {
		wrapStyleCache = lipgloss.NewStyle().Width(cw)
		wrapStyleCW = cw
	}
	return wrapStyleCache
}

// ── Scrolling ─────────────────────────────────────────────────────────────────

// YOffset returns the current scroll offset.
func (c *ChatView) YOffset() int { return c.yoff }

// GotoBottom scrolls to the bottom.
func (c *ChatView) GotoBottom() {
	if c.total <= c.height {
		c.yoff = 0
		return
	}
	c.yoff = c.total - c.height
}

// GotoTop scrolls to the top.
func (c *ChatView) GotoTop() {
	c.yoff = 0
}

// ScrollUp scrolls up by n lines.
func (c *ChatView) ScrollUp(n int) {
	c.yoff -= n
	if c.yoff < 0 {
		c.yoff = 0
	}
}

// ScrollDown scrolls down by n lines.
func (c *ChatView) ScrollDown(n int) {
	c.yoff += n
	maxYoff := max(0, c.total-c.height)
	if c.yoff > maxYoff {
		c.yoff = maxYoff
	}
}

// AtBottom returns true if the viewport is at the bottom.
func (c *ChatView) AtBottom() bool {
	return c.yoff >= max(0, c.total-c.height)
}

// ── Selection ─────────────────────────────────────────────────────────────────

// startSelection begins a text selection at the given screen position.
// screenLine is relative to the viewport (0 = top visible line).
func (c *ChatView) startSelection(screenLine, col int) {
	absLine := c.yoff + screenLine
	if absLine < 0 || absLine >= c.total {
		return
	}
	c.sel.active = true
	c.sel.anchor = selPos{line: absLine, col: col}
	c.sel.head = c.sel.anchor
}

// updateSelection extends the selection to the given screen position.
func (c *ChatView) updateSelection(screenLine, col int) {
	if !c.sel.active {
		return
	}
	absLine := c.yoff + screenLine
	if absLine < 0 {
		absLine = 0
	}
	if absLine >= c.total {
		absLine = c.total - 1
	}
	c.sel.head = selPos{line: absLine, col: col}
}

// endSelection finalizes a text selection.
func (c *ChatView) endSelection() {
	if !c.sel.active || c.sel.empty() {
		c.sel = selection{}
	}
}

// clearSelection clears the selection.
func (c *ChatView) clearSelection() {
	c.sel = selection{}
}

// HasSelection returns true if there's an active non-empty selection.
func (c *ChatView) HasSelection() bool {
	return c.sel.active && !c.sel.empty()
}

// SelectedText returns the currently selected text as a string.
func (c *ChatView) SelectedText() string {
	if !c.HasSelection() {
		return ""
	}
	start, end := c.sel.ordered()
	if start.line == end.line {
		// Single line
		line := stripANSI(c.wrappedLineSafe(start.line))
		return safeSlice(line, start.col, end.col)
	}

	var parts []string
	// First line
	first := stripANSI(c.wrappedLineSafe(start.line))
	parts = append(parts, safeSlice(first, start.col, displayWidth(first)))

	// Middle lines
	for l := start.line + 1; l < end.line; l++ {
		parts = append(parts, stripANSI(c.wrappedLineSafe(l)))
	}

	// Last line
	last := stripANSI(c.wrappedLineSafe(end.line))
	parts = append(parts, safeSlice(last, 0, end.col))

	return strings.Join(parts, "\n")
}

// wrappedLineSafe returns a wrapped line, or "" if out of range.
func (c *ChatView) wrappedLineSafe(idx int) string {
	return c.lineAt(idx)
}

// ── View ──────────────────────────────────────────────────────────────────────

// View returns the rendered viewport with scrollbar and selection.
func (c *ChatView) View() string {
	if !c.ready {
		return "Loading..."
	}
	if c.height <= 0 || c.total == 0 {
		return ""
	}

	// Ensure content is up-to-date before rendering
	c.rebuildIfNeeded()

	cw := c.contentWidth()
	h := c.height
	total := c.total
	yoff := c.yoff

	// Clip yoff to valid range
	maxYoff := max(0, total-h)
	if yoff > maxYoff {
		yoff = maxYoff
	}

	blank := strings.Repeat(" ", cw)
	thumbStart, thumbSize := scrollbarThumb(h, yoff, total)
	start, end := c.sel.ordered()

	rows := make([]string, h)
	for r := 0; r < h; r++ {
		idx := yoff + r
		line := blank
		if idx >= 0 && idx < total {
			line = c.lineAt(idx)

			// Pad to content width — use blank if shorter, else concatenate
			if dw := displayWidth(stripANSI(line)); dw < cw {
				line += blank[cw-dw:] // reuse tail of the precomputed blank
			}
		}

		// Apply selection highlight
		if c.sel.active && !c.sel.empty() {
			selStart, selEnd := selSpanOnLine(idx, start, end, cw)
			if selStart >= 0 {
				line = lipgloss.StyleRanges(line, lipgloss.NewRange(selStart, selEnd, selStyle))
			}
		}

		// Scrollbar
		if c.showScrollbar {
			thumb := " "
			if r >= thumbStart && r < thumbStart+thumbSize {
				if thumbStart+thumbSize >= h {
					thumb = scrollThumbStyle.Render("▐")
				} else {
					thumb = scrollThumbStyle.Render("▌")
				}
			} else {
				thumb = scrollTrackStyle.Render("│")
			}
			rows[r] = line + thumb
		} else {
			rows[r] = line
		}
	}

	return strings.Join(rows, "\n")
}

// Update handles key and mouse messages for viewport navigation and selection.
// Returns true if the viewport handled the message.
func (c *ChatView) Update(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return c.handleKey(msg)
	case tea.MouseMsg:
		return c.handleMouse(msg)
	case tea.WindowSizeMsg:
		// Don't handle — the App model handles window resize
		return false
	}
	return false
}

func (c *ChatView) handleKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "pgup":
		c.ScrollUp(c.height / 2)
		return true
	case "pgdown":
		c.ScrollDown(c.height / 2)
		return true
	}
	return false
}

func (c *ChatView) handleMouse(msg tea.MouseMsg) bool {
	switch msg.Type {
	case tea.MouseWheelUp:
		c.ScrollUp(3)
		return true
	case tea.MouseWheelDown:
		c.ScrollDown(3)
		return true
	case tea.MouseLeft:
		line := c.screenYToTranscriptLine(msg.Y)
		if line < 0 {
			return false
		}
		// Press: start selection
		if msg.Action == tea.MouseActionPress {
			c.startSelection(line, msg.X)
			return true
		}
		// Release: end selection
		if msg.Action == tea.MouseActionRelease {
			c.endSelection()
			return true
		}
	case tea.MouseMotion:
		if msg.Action == tea.MouseActionPress {
			// Mouse button held (drag) — X is the column, Y is the row
			line := c.screenYToTranscriptLine(msg.Y)
			if line < 0 {
				return false
			}
			c.updateSelection(line, msg.X)
			return true
		}
	}
	return false
}

// ── Mouse coordinate helpers ──────────────────────────────────────────────────

// SetTranscriptY records the terminal row where the transcript viewport starts.
// The App.View calls this so mouse Y coordinates can be mapped to viewport lines.
func (c *ChatView) SetTranscriptY(y int) {
	c.transcriptY = y
}

// screenYToTranscriptLine maps a terminal screen Y to a transcript line index
// (0-based within the viewport). Returns -1 if outside the transcript area.
func (c *ChatView) screenYToTranscriptLine(screenY int) int {
	lineInViewport := screenY - c.transcriptY
	if lineInViewport < 0 || lineInViewport >= c.height {
		return -1
	}
	return lineInViewport
}

// ── Formatting ────────────────────────────────────────────────────────────────

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
		var content string
		if msg.Streaming {
			// Fast incremental path: only the appended delta is re-rendered.
			content = c.streamRenderAssistant(msg.Content, innerW)
		} else {
			// Finalized: do the full markdown + syntax-highlight render once.
			content = renderMarkdown(msg.Content, innerW)
		}
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

// ── Markdown rendering ───────────────────────────────────────────────────────

// streamMDState carries incremental markdown render state for the currently
// streaming assistant message. Each new token re-renders only the appended
// delta instead of re-parsing the whole accumulated content, keeping the
// streaming path roughly O(delta) per token.
//
// While streaming, prose is rendered with inline markdown and code blocks are
// shown as plain (unhighlighted) lines so tokens stay cheap; the full
// markdown + chroma render runs once the message finalizes (Streaming=false).
type streamMDState struct {
	committed    strings.Builder // ANSI for all fully-terminated output lines (no trailing \n)
	committedLen int             // bytes of the current content encoded into `committed`
	prevContent  string          // content from the previous call, for growth check
	inCode       bool            // currently inside a ``` code fence
	codeLang     string          // language hint after the opening fence
	width        int
}

// boldInlineStyle is a cached bold style used by the inline-markdown fast path
// (avoids allocating a new Style per token).
var boldInlineStyle = lipgloss.NewStyle().Bold(true)

// maxStreamPartial caps how much of a still-growing (newline-less) partial line
// is rendered each token, bounding the worst-case cost of a single very long
// unbroken line during streaming. Once newline arrives the full line is
// committed and rendered correctly, so this only affects transient display.
const maxStreamPartial = 512

// markdownInline applies inline markdown (code + bold) to a single line.
func markdownInline(line string) string {
	line = codeRE.ReplaceAllString(line, CodeBlockStyle.Render("$1"))
	line = boldRE.ReplaceAllString(line, boldInlineStyle.Render("$1"))
	return line
}

// streamRenderAssistant incrementally renders an in-progress assistant message.
func (c *ChatView) streamRenderAssistant(content string, width int) string {
	if width < 10 {
		width = 80
	}

	s := c.stream
	// Reset/full-render when the message is replaced or the width changes.
	if s == nil || s.width != width || !strings.HasPrefix(content, s.prevContent) {
		full := renderMarkdown(content, width)
		s = &streamMDState{prevContent: content, width: width, committedLen: len(content)}
		s.committed.WriteString(full)
		// Reconstruct code-fence state so the next delta is classified correctly.
		s.inCode, s.codeLang = scanCodeState(content)
		c.stream = s
		return full
	}

	s.prevContent = content
	// Commit every complete (newline-terminated) line that has appeared since the
	// last call; the trailing partial line is re-rendered fresh each call.
	rest := content[s.committedLen:]
	for {
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 {
			break
		}
		s.commitLine(rest[:nl])
		s.committedLen += nl + 1
		rest = rest[nl+1:]
	}

	partial := content[s.committedLen:]
	var partialRender string
	if partial != "" {
		// The trailing partial line is transient: once a newline arrives it is
		// committed correctly (full render). While it is still growing we cap its
		// display length so a single very long unbroken line cannot cause O(n²)
		// re-renders on every token.
		if len(partial) > maxStreamPartial {
			partial = partial[:maxStreamPartial] + "…"
		}
		if s.inCode {
			partialRender = CodeBlockStyle.Render("  " + partial)
		} else {
			partialRender = markdownInline(partial)
		}
	}

	if partialRender == "" {
		return s.committed.String()
	}
	if s.committed.Len() > 0 {
		return s.committed.String() + "\n" + partialRender
	}
	return partialRender
}

// commitLine consumes one complete (newline-terminated) input line into the
// committed output buffer.
func (s *streamMDState) commitLine(line string) {
	trimmed := line
	if strings.HasPrefix(trimmed, "```") {
		if s.inCode {
			// Closing fence: back to prose. The fence line renders nothing.
			s.inCode = false
			s.codeLang = ""
		} else {
			// Opening fence: subsequent lines are code, shown plain while streaming.
			s.inCode = true
			s.codeLang = strings.TrimSpace(trimmed[3:])
		}
		return
	}

	if s.committed.Len() > 0 {
		s.committed.WriteString("\n")
	}
	if s.inCode {
		// Plain code line while streaming (highlighted on finalize).
		s.committed.WriteString(CodeBlockStyle.Render("  " + trimmed))
	} else {
		s.committed.WriteString(markdownInline(trimmed))
	}
}

// scanCodeState walks complete content and returns (inCode, codeLang) so the
// incremental renderer can resume after a full re-render.
func scanCodeState(content string) (bool, string) {
	inCode := false
	lang := ""
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "```") {
			if inCode {
				inCode = false
				lang = ""
			} else {
				inCode = true
				lang = strings.TrimSpace(line[3:])
			}
		}
	}
	return inCode, lang
}

// renderMarkdown renders markdown content in the terminal.
func renderMarkdown(text string, width int) string {
	if width < 10 {
		width = 80
	}

	var result strings.Builder
	lines := strings.Split(text, "\n")
	inCodeBlock := false
	isDiffBlock := false
	codeLang := ""
	var codeLines []string

	flushCodeBlock := func() {
		if len(codeLines) == 0 {
			return
		}
		if isDiffBlock {
			// Render diff lines with chroma-syntax-aware formatting
			result.WriteString("\n")
			highlightDiffBlock(&result, codeLines)
		} else {
			// Use chroma for syntax highlighting of non-diff code blocks
			result.WriteString("\n")
			highlightCodeBlock(&result, codeLines, codeLang, width)
		}
		codeLines = nil
	}

	for _, line := range lines {
		trimmed := line

		if strings.HasPrefix(trimmed, "```") {
			if inCodeBlock {
				flushCodeBlock()
				inCodeBlock = false
				isDiffBlock = false
				codeLang = ""
			} else {
				inCodeBlock = true
				// Check for diff language identifier
				rest := strings.TrimSpace(trimmed[3:])
				if strings.EqualFold(rest, "diff") {
					isDiffBlock = true
				} else {
					codeLang = rest
				}
			}
			continue
		}

		if inCodeBlock {
			codeLines = append(codeLines, trimmed)
			continue
		}

		// Inline code patterns
		line = codeRE.ReplaceAllString(line, CodeBlockStyle.Render("$1"))

		// Bold: **text**
		line = boldRE.ReplaceAllString(line, lipgloss.NewStyle().Bold(true).Render("$1"))

		if displayWidth(line) > width {
			// Don't truncate — let the outer wrapContent handle line wrapping.
			// truncateWidth would discard content beyond the limit.
			// Instead, we let lipgloss.Width().Render() in wrapContent handle wrapping.
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
		result.WriteString(line)
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

// ── Chroma syntax highlighting ────────────────────────────────────────────────

// styleName is the chroma style to use. We use a dark-friendly style that works
// well with the Tokyo Night terminal theme.
const chromaStyleName = "catppuccin"

// highlightCodeBlock renders code lines with chroma syntax highlighting.
// It uses catppuccin (a dark-friendly style) and the terminal16m formatter for
// true-colour ANSI output, then wraps each line with the code block background style.
func highlightCodeBlock(w *strings.Builder, lines []string, lang string, width int) {
	code := strings.Join(lines, "\n")
	var highlighted string
	if lang == "" {
		// No language specified: try to auto-detect
		l := lexers.Analyse(code)
		if l != nil {
			lang = l.Config().Name
		}
	}

	if lang != "" {
		var buf strings.Builder
		err := quick.Highlight(&buf, code, lang, "terminal16m", chromaStyleName)
		if err == nil && buf.Len() > 0 {
			highlighted = buf.String()
		}
	}

	if highlighted == "" {
		// Fallback: render as plain code block (no chroma)
		renderPlainCodeLines(w, lines, width)
		return
	}

	// Apply code block background to each line of the chroma-highlighted output
	hlLines := strings.Split(strings.TrimRight(highlighted, "\n"), "\n")
	codeWidth := width - 2
	if codeWidth < 10 {
		codeWidth = 10
	}
	for _, hlLine := range hlLines {
		rendered := CodeBlockStyle.Width(codeWidth).Render("  " + hlLine)
		if displayWidth(rendered) > width {
			rendered = truncateWidth(rendered, width-1) + "…"
		}
		w.WriteString(rendered)
		w.WriteString("\n")
	}
}

// renderPlainCodeLines renders code lines without syntax highlighting.
func renderPlainCodeLines(w *strings.Builder, lines []string, width int) {
	codeWidth := width - 2
	if codeWidth < 10 {
		codeWidth = 10
	}
	maxLen := 0
	for _, l := range lines {
		if dw := displayWidth(l); dw > maxLen {
			maxLen = dw
		}
	}
	innerW := maxLen + 4
	if innerW > codeWidth {
		innerW = codeWidth
	}
	for _, line := range lines {
		trunc := line
		if displayWidth(trunc) > innerW-4 {
			trunc = truncateWidth(trunc, innerW-7) + "..."
		}
		w.WriteString(CodeBlockStyle.Render("  " + trunc))
		w.WriteString("\n")
	}
}

// highlightDiffBlock renders diff code lines using chroma's diff lexer for
// accurate syntax highlighting with diff colours.
func highlightDiffBlock(w *strings.Builder, lines []string) {
	code := strings.Join(lines, "\n")
	var buf strings.Builder
	err := quick.Highlight(&buf, code, "diff", "terminal16m", chromaStyleName)
	if err == nil && buf.Len() > 0 {
		// Chroma produced ANSI output for the diff — render it line by line
		hlLines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		for _, hlLine := range hlLines {
			w.WriteString(renderChromaDiffLine(hlLine))
			w.WriteString("\n")
		}
		return
	}

	// Fallback: use our own diff line rendering
	for _, line := range lines {
		w.WriteString(renderDiffLine(line))
		w.WriteString("\n")
	}
}

// renderChromaDiffLine applies additional background styling on top of
// chroma's ANSI diff output to match our TUI's visual language.
func renderChromaDiffLine(line string) string {
	if line == "" {
		return line
	}
	// Chroma's terminal16m diff formatter applies its own foreground colors.
	// We add background styling to match our visual language.
	stripped := stripANSI(line)
	if stripped == "" {
		return " " + line
	}
	switch stripped[0] {
	case '+':
		if strings.HasPrefix(stripped, "+++") {
			return DiffHeaderStyle.Render(" ") + line
		}
		// Wrap chroma's output with our background style (compose)
		bgStyle := lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#d1fae5", Dark: "#14351d"})
		return bgStyle.Render(" ") + line
	case '-':
		if strings.HasPrefix(stripped, "---") {
			return DiffHeaderStyle.Render(" ") + line
		}
		bgStyle := lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#fee2e2", Dark: "#3a1619"})
		return bgStyle.Render(" ") + line
	case '@':
		return DiffHeaderStyle.Render(" ") + line
	default:
		return " " + line
	}
}

// ── Diff rendering (fallback) ─────────────────────────────────────────────────

// renderDiffLine renders a single diff line with colour coding (fallback).
func renderDiffLine(line string) string {
	if line == "" {
		return line
	}
	// Strip ANSI before checking first char
	stripped := stripANSI(line)
	if stripped == "" {
		// ANSI-only line: render with a leading space to preserve alignment
		return " " + line
	}
	switch stripped[0] {
	case '+':
		if strings.HasPrefix(stripped, "+++") {
			return DiffHeaderStyle.Render(" ") + line
		}
		bgStyle := lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#d1fae5", Dark: "#14351d"}).Foreground(ColorDiffAdd)
		return bgStyle.Render(" ") + line
	case '-':
		if strings.HasPrefix(stripped, "---") {
			return DiffHeaderStyle.Render(" ") + line
		}
		bgStyle := lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#fee2e2", Dark: "#3a1619"}).Foreground(ColorDiffDel)
		return bgStyle.Render(" ") + line
	case '@':
		return DiffHeaderStyle.Render(" ") + line
	case 'd':
		if strings.HasPrefix(stripped, "diff ") {
			return DiffHeaderStyle.Render(line)
		}
		return " " + line
	default:
		return " " + line
	}
}

// dimLine renders a line as dim/muted.
func dimLine(s string) string {
	return dimLineStyle.Render(s)
}

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// safeSlice returns a substring from col s to col e, handling wide chars.
func safeSlice(s string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(s) {
		end = len(s)
	}
	if start >= end {
		return ""
	}
	// Convert byte positions: walk rune by rune tracking display width
	var runes []rune
	for _, r := range s {
		runes = append(runes, r)
	}
	if start >= len(runes) {
		return ""
	}
	if end > len(runes) {
		end = len(runes)
	}
	return string(runes[start:end])
}

// selSpanOnLine computes the column range on a single wrapped line that falls
// within the selection. Returns (-1, 0) if the line is not selected.
func selSpanOnLine(lineIdx int, start, end selPos, lineWidth int) (int, int) {
	if lineIdx < start.line || lineIdx > end.line {
		return -1, 0
	}
	if lineIdx == start.line && lineIdx == end.line {
		return start.col, end.col
	}
	if lineIdx == start.line {
		return start.col, lineWidth
	}
	if lineIdx == end.line {
		return 0, end.col
	}
	return 0, lineWidth
}

// ── Width helpers (no external dep needed) ────────────────────────────────────

var (
	ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	codeRE = regexp.MustCompile("`([^`]+)`")
	boldRE = regexp.MustCompile(`\*\*(.+?)\*\*`)
)

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
