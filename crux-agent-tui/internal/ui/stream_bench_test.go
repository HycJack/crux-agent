package ui

import (
	"strings"
	"testing"
)

// benchStream simulates a realistic streaming reply: the assistant message grows
// token-by-token (word/code granularity) over a fixed transcript of large tool
// outputs, and each token is followed by a View() render.
func benchStream(b *testing.B, transcriptMsgs, toolLines, lines int) {
	c := NewChatView()
	c.SetSize(120, 40)
	for i := 0; i < transcriptMsgs; i++ {
		body := strings.Repeat("some tool output content line here\n", toolLines)
		c.AddMessage(ChatMessage{Role: "tool_call", ToolCall: &ToolCallInfo{
			Name: "bash", Args: "x", Result: body, Streaming: false, Lines: toolLines,
		}})
	}
	c.AddMessage(ChatMessage{Role: "assistant", Content: "", Streaming: true})

	var sb strings.Builder
	for i := 0; i < lines; i++ {
		sb.WriteString("This sentence has inline `code` spans and **bold** words to render.\n")
	}
	full := sb.String()

	b.ResetTimer()
	pos := 0
	for i := 0; i < b.N; i++ {
		pos += 5 // ~a token
		if pos > len(full) {
			pos = 0
			c.stream = nil
			c.UpdateLastMessage("")
			continue
		}
		c.UpdateLastMessage(full[:pos])
		_ = c.View()
	}
}

// BenchmarkStream_TinyTranscript: 2 short tool outputs, 20-line reply.
func BenchmarkStream_TinyTranscript(b *testing.B) { benchStream(b, 2, 5, 20) }

// BenchmarkStream_LargeTranscript: 8 tool outputs x 40 lines, same reply. The
// streaming per-token cost must not grow with the fixed transcript size.
func BenchmarkStream_LargeTranscript(b *testing.B) { benchStream(b, 8, 40, 20) }

// BenchmarkStream_Growing: a much longer reply to check per-token cost stays
// roughly flat as the streaming message itself grows.
func BenchmarkStream_Growing(b *testing.B) { benchStream(b, 2, 5, 120) }
