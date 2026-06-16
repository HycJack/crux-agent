package session

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Branch represents a fork of a conversation session.
// || 会话分支
type Branch struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	SessionID string            `json:"session_id"`
	ParentID  string            `json:"parent_id"` // 分叉点的消息 ID
	Summary   string            `json:"summary"`   // 分支摘要
	Messages  []SessionTreeEntry `json:"messages"`
	CreatedAt time.Time         `json:"created_at"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// SummaryFunc generates a branch summary.
// || 分支摘要生成函数
type SummaryFunc func(ctx context.Context, sourceTitle string, messages []SessionTreeEntry) (string, error)

// BranchConfig configures branching behavior.
type BranchConfig struct {
	MaxBranches    int          // 最大分支数（默认 10）
	AutoSummary    bool         // 自动摘要（默认 true）
	SummaryFunc    SummaryFunc  // 自定义摘要函数（nil = 使用截断）
	TruncateLength int          // 截断摘要长度（默认 500）
}

// DefaultBranchConfig returns sensible defaults.
func DefaultBranchConfig() BranchConfig {
	return BranchConfig{
		MaxBranches:    10,
		AutoSummary:    true,
		TruncateLength: 500,
	}
}

// TruncateSummary generates a deterministic, non-LLM summary.
// || 截断摘要（不依赖 LLM）
func TruncateSummary(msgs []SessionTreeEntry, maxMessages int) string {
	if len(msgs) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Branch summary\n\n")

	count := 0
	for _, m := range msgs {
		if m.Type != EntryUserMessage && m.Type != EntryAssistantMessage {
			continue
		}

		// Extract content from message data
		content := extractTextFromEntry(m)
		if len(content) > 150 {
			content = content[:150] + "..."
		}

		typeName := "user"
		if m.Type == EntryAssistantMessage {
			typeName = "assistant"
		}

		fmt.Fprintf(&sb, "- [%s] %s\n", typeName, content)
		count++
		if count >= maxMessages {
			sb.WriteString("- ... (more messages omitted)\n")
			break
		}
	}
	return sb.String()
}

// extractTextFromEntry extracts text content from a session entry.
func extractTextFromEntry(entry SessionTreeEntry) string {
	msgs := entry.GetMessages()
	if len(msgs) == 0 {
		return ""
	}
	msg := msgs[0]
	// Try to get content from the message
	switch m := msg.(type) {
	case interface{ GetContent() string }:
		return m.GetContent()
	default:
		// Fallback: use raw message data
		data := string(entry.MessageData)
		// Try to extract "content" field
		if idx := strings.Index(data, `"content":"`); idx >= 0 {
			start := idx + len(`"content":"`)
			end := strings.Index(data[start:], `"`)
			if end > 0 {
				return data[start : start+end]
			}
		}
		return data
	}
}

// LLMSummary generates a branch summary using an LLM.
// || LLM 摘要
func LLMSummary(ctx context.Context, completeFn func(ctx context.Context, prompt string) (string, error), title string, msgs []SessionTreeEntry) (string, error) {
	if completeFn == nil {
		return TruncateSummary(msgs, 5), nil
	}

	prompt := buildSummaryPrompt(title, msgs)
	out, err := completeFn(ctx, prompt)
	if err != nil {
		// Degrade gracefully to truncation
		return TruncateSummary(msgs, 5), nil
	}
	if strings.TrimSpace(out) == "" {
		return TruncateSummary(msgs, 5), nil
	}
	return out, nil
}

// buildSummaryPrompt builds the LLM prompt for branch summarization.
func buildSummaryPrompt(title string, msgs []SessionTreeEntry) string {
	var sb strings.Builder
	sb.WriteString("You MUST create a structured summary of a conversation branch.\n")
	sb.WriteString("This branch was just left (the user forked to a new path), so the summary\n")
	sb.WriteString("will be used to give context if/when the user returns here.\n\n")
	fmt.Fprintf(&sb, "Source session: %q\n", title)
	fmt.Fprintf(&sb, "Messages in branch: %d\n\n", len(msgs))

	sb.WriteString("Conversation (oldest first, truncated to 300 chars per message):\n")
	count := 0
	for _, m := range msgs {
		if m.Type != EntryUserMessage && m.Type != EntryAssistantMessage {
			continue
		}
		content := extractTextFromEntry(m)
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		typeName := "user"
		if m.Type == EntryAssistantMessage {
			typeName = "assistant"
		}
		fmt.Fprintf(&sb, "[%s]: %s\n", typeName, content)
		count++
		if count >= 20 {
			sb.WriteString("... (truncated)\n")
			break
		}
	}

	sb.WriteString(`
You MUST use EXACT format (sections can be omitted if not applicable):

## Goal
[What was the user trying to accomplish in this branch?]

## Constraints & Preferences
- [Constraints, preferences, requirements mentioned]
- [(none) if none mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Work started but not finished]

### Blocked
- [Issues preventing progress]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [What should happen next to continue]

Sections MUST be kept concise. You MUST preserve exact file paths, function names, error messages.
`)
	return sb.String()
}

// AppendSummaryMessage appends the summary as a system message to the session.
// || 将摘要作为系统消息追加到会话
func AppendSummaryMessage(sess *Session, summary string) error {
	if summary == "" {
		return nil
	}
	return sess.Append(SessionTreeEntry{
		Type:      EntrySystemPrompt,
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"prompt": "## Branch summary\n\n" + summary,
			"type":   "branch_summary",
		},
	})
}
