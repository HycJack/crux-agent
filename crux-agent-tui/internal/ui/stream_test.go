package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestPerMessageWrapEqualsConcat: wrapping each message individually and
// joining must produce the same flattened lines as wrapping the concatenation.
func TestPerMessageWrapEqualsConcat(t *testing.T) {
	c := NewChatView()
	c.SetSize(100, 30)
	msgs := []ChatMessage{
		{Role: "user", Content: "hello " + strings.Repeat("world word ", 20)},
		{Role: "assistant", Content: strings.Repeat("old tool output content ", 30)},
		{Role: "tool_call", ToolCall: &ToolCallInfo{Name: "bash", Args: "x",
			Result: strings.Repeat("line content here\n", 15), Streaming: false, Lines: 15}},
	}

	for _, m := range msgs {
		c.AddMessage(m)
	}
	got := flattenMsgWrapped(c)

	renders := make([]string, len(msgs))
	for i, m := range msgs {
		renders[i] = c.formatMessage(m)
	}
	ref := strings.Split(getWrapStyle(c.contentWidth()).Render(strings.Join(renders, "\n")), "\n")

	if !reflect.DeepEqual(got, ref) {
		t.Fatalf("per-message wrap != concat wrap\n got=%q\n ref=%q", got, ref)
	}
}

// TestSpliceMatchesFullRebuild: after incrementally updating the last message,
// the ChatView must expose the same wrapped lines as a full rebuild.
func TestSpliceMatchesFullRebuild(t *testing.T) {
	newView := func() *ChatView {
		vc := NewChatView()
		vc.SetSize(90, 30)
		vc.AddMessage(ChatMessage{Role: "user", Content: "q1"})
		vc.AddMessage(ChatMessage{Role: "assistant", Content: strings.Repeat("a1 word ", 10), Streaming: true})
		vc.AddMessage(ChatMessage{Role: "user", Content: "q2"})
		vc.AddMessage(ChatMessage{Role: "assistant", Content: "a2", Streaming: true})
		return vc
	}

	c := newView()
	cur := ""
	for i := 0; i < 40; i++ {
		cur += "delta word "
		c.UpdateLastMessage(cur)
	}
	final := "final answer " + strings.Repeat("long streaming content token ", 30)
	c.UpdateLastMessage(final)
	got := flattenMsgWrapped(c)

	refc := newView()
	refc.UpdateLastMessage(final)
	refc.fullRebuild() // full rebuild from scratch

	if !reflect.DeepEqual(got, flattenMsgWrapped(refc)) {
		t.Fatalf("incremental splice != full rebuild\n got=%q\n ref=%q", got, flattenMsgWrapped(refc))
	}
}

// flattenMsgWrapped flattens the per-message wrapped lines into one slice.
func flattenMsgWrapped(c *ChatView) []string {
	var out []string
	for _, m := range c.msgWrapped {
		out = append(out, m...)
	}
	return out
}

// TestStreamPlainProseMatchesFullMarkdown: for pure-prose assistant messages
// (no code blocks), the incremental streaming render must match the full render.
func TestStreamPlainProseMatchesFullMarkdown(t *testing.T) {
	c := NewChatView()
	c.SetSize(110, 50)
	c.AddMessage(ChatMessage{Role: "assistant", Content: "", Streaming: true})

	texts := []string{
		"Here is some **bold** and `code`.",
		"\nSecond line with more words and inline code `x` here.",
		"\nThe quick brown fox jumps over the lazy dog repeatedly " + strings.Repeat("padding ", 30),
		"\nLast line **important**.",
	}
	var cur strings.Builder
	for _, tx := range texts {
		cur.WriteString(tx)
		c.UpdateLastMessage(cur.String())
		got := c.streamRenderAssistant(cur.String(), c.width-4)
		ref := renderMarkdown(cur.String(), c.width-4)
		if got != ref {
			t.Fatalf("streaming prose != full markdown\n content=%q\n got=%q\n ref=%q",
				cur.String(), got, ref)
		}
	}
}

// TestStreamFinalizeMatchesFullMarkdown: a message that was streamed (code shown
// plain) then finalized must render with full markdown once Streaming=false.
func TestStreamFinalizeMatchesFullMarkdown(t *testing.T) {
	c := NewChatView()
	c.SetSize(110, 60)
	c.AddMessage(ChatMessage{Role: "assistant", Content: "", Streaming: true})

	streamed := "Here is prose first.\n```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```\nThen **bold** end."
	for i := 1; i <= len(streamed); i += 3 {
		c.UpdateLastMessage(streamed[:i])
	}

	// finalize
	last := c.messages[len(c.messages)-1]
	last.Streaming = false
	c.ReplaceLastMessage(last)

	got := c.formatMessage(last)
	want := renderMarkdown(last.Content, c.width-4)
	want = lipgloss.JoinVertical(lipgloss.Left, AssistantMsgStyle.Render("Assistant:"), want)
	if got != want {
		t.Fatalf("finalized assistant != full markdown\n got=%q\n want=%q", got, want)
	}
}
