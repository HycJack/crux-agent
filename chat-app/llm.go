package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hycjack/crux-ai/ai"
	"github.com/hycjack/crux-ai/core"

	"chat-app/logutil"
)

// newSyncSummarizer returns a synchronous LLM completion function.
//
// The returned closure sends userPrompt as a single user message and uses
// systemPrompt as the system instruction. The response is concatenated
// from TextContent blocks and trimmed.
//
// If apiKey is empty, the closure returns "NONE" — extracts interpret
// this as "nothing to remember" and skip the LLM call entirely.
func newSyncSummarizer(model core.Model, apiKey string, timeout time.Duration, systemPrompt string) func(ctx context.Context, userPrompt string) (string, error) {
	return func(ctx context.Context, userPrompt string) (string, error) {
		if apiKey == "" {
			return "NONE", nil
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		resp, err := ai.CompleteSimpleWithContext(callCtx, model,
			core.Context{
				SystemPrompt: systemPrompt,
				Messages: []core.Message{
					core.UserMessage{
						Role:      core.MessageRoleUser,
						Content:   userPrompt,
						Timestamp: time.Now(),
					},
				},
			},
			core.SimpleStreamOptions{
				StreamOptions: core.StreamOptions{
					APIKey: apiKey,
				},
			},
		)
		if err != nil {
			logutil.Warnf("[llm] sync summarizer error: %v", err)
			return "", err
		}
		return extractText(resp.Content), nil
	}
}

// extractText concatenates the text from TextContent blocks in a content
// slice, trimming whitespace.
func extractText(blocks []core.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if c, ok := b.(core.TextContent); ok {
			sb.WriteString(c.Text)
		}
	}
	return strings.TrimSpace(sb.String())
}

// buildSummarizeFunc returns a summarizer function used by context compaction.
func buildSummarizeFunc(model core.Model, apiKey string) func(ctx context.Context, dropped []core.Message) (string, error) {
	const systemPrompt = "You are a conversation summarizer."
	const userPromptHeader = "Summarize the following conversation in 1-2 sentences, keeping key facts (user identity, preferences, decisions, context):\n\n"
	summarize := newSyncSummarizer(model, apiKey, 30*time.Second, systemPrompt)

	return func(ctx context.Context, dropped []core.Message) (string, error) {
		if apiKey == "" {
			return "", fmt.Errorf("compaction: no API key")
		}
		var sb strings.Builder
		sb.WriteString(userPromptHeader)
		for _, msg := range dropped {
			switch m := msg.(type) {
			case core.UserMessage:
				fmt.Fprintf(&sb, "User: %v\n", m.Content)
			case core.AssistantMessage:
				fmt.Fprintf(&sb, "Assistant: %s\n", extractText(m.Content))
			}
		}
		return summarize(ctx, sb.String())
	}
}
