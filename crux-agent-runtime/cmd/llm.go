package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hycjack/crux-ai/ai"
	"github.com/hycjack/crux-ai/core"
)

// newSyncSummarizer returns a synchronous LLM completion function.
//
// The returned closure sends userPrompt as a single user message and uses
// systemPrompt as the system instruction. The response is concatenated
// from TextContent blocks and trimmed.
//
// If apiKey is empty, the closure returns "NONE" — autolearn extractors
// interpret this as "nothing to remember" and skip the LLM call entirely.
//
// Pass an empty systemPrompt if the prompt itself already contains the
// necessary system context (e.g. autolearn extraction prompts that
// include role + whitelist + output format inline).
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
			return "", err
		}
		return extractText(resp.Content), nil
	}
}

// buildSummarizeFunc returns a summarizer function used by LLMSummarize.
// It serializes dropped messages into a prompt and delegates to the
// shared newSyncSummarizer helper.
func buildSummarizeFunc(model core.Model, apiKey string) func(ctx context.Context, dropped []core.Message) (string, error) {
	const systemPrompt = "你是一个对话摘要助手。"
	const userPromptHeader = "请用 1-2 句话总结下面这段对话的要点，保留关键事实（用户身份、偏好、决策、上下文）：\n\n"
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
				fmt.Fprintf(&sb, "用户: %v\n", m.Content)
			case core.AssistantMessage:
				fmt.Fprintf(&sb, "助手: %s\n", extractText(m.Content))
			}
		}
		return summarize(ctx, sb.String())
	}
}

// extractText concatenates the text from TextContent blocks in a content
// slice, trimming whitespace. Used by every summarizer / event consumer.
func extractText(blocks []core.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if c, ok := b.(core.TextContent); ok {
			sb.WriteString(c.Text)
		}
	}
	return strings.TrimSpace(sb.String())
}