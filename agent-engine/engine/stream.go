package engine

import (
	"context"
	"log"

	core "github.com/hycjack/crux-ai/core"
)

// streamAssistantResponse handles the full LLM streaming path:
//  1. Transform context (apply TransformContext hook)
//  2. Pre-call compaction if token budget is exceeded
//  3. Convert to LLM messages
//  4. Invoke StreamFn or default crux-ai stream
//  5. Consume events and handle overflow retries
// handleOverflowRetry is a shared helper for streamAssistantResponse that
// handles context-overflow recovery with compaction.
func handleOverflowRetry(ctx context.Context, config AgentLoopConfig, messages *[]core.Message, stream *AgentEventStream) (core.AssistantMessage, []core.Message, error) {
	retried, retryErr := retryWithCompaction(ctx, config, messages, stream)
	if retryErr != nil {
		return core.AssistantMessage{}, *messages, retryErr
	}
	stream.Push(EventMessageEnd{Message: retried})
	return retried, *messages, nil
}

func streamAssistantResponse(ctx context.Context, config AgentLoopConfig, messages []core.Message, stream *AgentEventStream) (core.AssistantMessage, []core.Message, error) {
	trimmedMessages := transformContext(ctx, config, messages)

	// Pre-call compaction: if the compactor is configured and estimated
	// tokens exceed MaxTokens, run compaction before sending.
	trimmedMessages = maybeCompactPreCall(ctx, config, trimmedMessages)

	llmMessages := convertToLLM(config, trimmedMessages)
	opts := resolveStreamOptions(&config)
	llmCtx := toContextMessages(llmMessages, config.SystemPrompt, config.Tools)

	llmStream, err := invokeStreamFn(ctx, config, llmCtx, opts)
	if err != nil {
		if core.IsContextOverflow(err) {
			return handleOverflowRetry(ctx, config, &trimmedMessages, stream)
		}
		return core.AssistantMessage{}, trimmedMessages, err
	}

	partialMsg, err := consumeStreamEvents(ctx, llmStream, stream)
	if err != nil {
		if core.IsContextOverflow(err) {
			return handleOverflowRetry(ctx, config, &trimmedMessages, stream)
		}
		return core.AssistantMessage{}, trimmedMessages, err
	}

	stream.Push(EventMessageEnd{Message: partialMsg})
	return partialMsg, trimmedMessages, nil
}

// maybeCompactPreCall runs the compactor if the configured budget is exceeded.
func maybeCompactPreCall(ctx context.Context, config AgentLoopConfig, messages []core.Message) []core.Message {
	comp := config.Compaction.Compactor
	if comp == nil {
		return messages
	}
	maxTokens := config.Compaction.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 100000
	}
	reserveTokens := config.Compaction.ReserveTokens
	if reserveTokens < 0 {
		reserveTokens = 0
	}

	counter := config.Compaction.TokenCounter
	if counter == nil {
		counter = defaultTokenCounter
	}

	tokens := counter(config.SystemPrompt, messages, toCoreTools(config.Tools))
	if tokens <= maxTokens-reserveTokens {
		return messages
	}

	prevTokens := tokens
	prevCount := len(messages)
	newMsgs, changed, err := comp(ctx, messages)
	if err != nil || !changed {
		return messages
	}
	if config.Compaction.OnCompact != nil {
		newTokens := counter(config.SystemPrompt, newMsgs, toCoreTools(config.Tools))
		config.Compaction.OnCompact(prevTokens, newTokens, prevCount, len(newMsgs))
	}
	log.Printf("agent: pre-call compaction: %d tokens → ? tokens, %d msgs → %d msgs",
		prevTokens, prevCount, len(newMsgs))
	return newMsgs
}

// retryWithCompaction is the overflow-error fallback: force-compact and
// retry the LLM call.
func retryWithCompaction(ctx context.Context, config AgentLoopConfig, messages *[]core.Message, stream *AgentEventStream) (core.AssistantMessage, error) {
	comp := config.Compaction.Compactor
	if comp == nil {
		return core.AssistantMessage{}, &core.OverflowError{Message: "compaction not configured"}
	}
	retries := config.Compaction.OverflowRetries
	if retries <= 0 {
		retries = 1
	}

	counter := config.Compaction.TokenCounter
	if counter == nil {
		counter = defaultTokenCounter
	}

	var lastErr error
	for i := 0; i < retries; i++ {
		// Emit retry event for UI visibility
		stream.Push(EventRetry{
			Attempt:     i + 1,
			MaxAttempts: retries,
			Message:     "context overflow, compacting and retrying",
		})

		prevTokens := counter(config.SystemPrompt, *messages, toCoreTools(config.Tools))
		prevCount := len(*messages)

		newMsgs, changed, err := comp(ctx, *messages)
		if err != nil {
			lastErr = err
			break
		}
		if !changed || len(newMsgs) >= len(*messages) {
			lastErr = &core.OverflowError{Message: "compactor made no progress"}
			break
		}
		*messages = newMsgs
		log.Printf("agent: overflow retry %d/%d — compacted %d tokens, %d → %d msgs",
			i+1, retries, prevTokens, prevCount, len(newMsgs))
		if config.Compaction.OnCompact != nil {
			newTokens := counter(config.SystemPrompt, *messages, toCoreTools(config.Tools))
			config.Compaction.OnCompact(prevTokens, newTokens, prevCount, len(newMsgs))
		}

		llmMessages := convertToLLM(config, *messages)
		opts := resolveStreamOptions(&config)
		llmCtx := toContextMessages(llmMessages, config.SystemPrompt, config.Tools)

		llmStream, err := invokeStreamFn(ctx, config, llmCtx, opts)
		if err != nil {
			if !core.IsContextOverflow(err) {
				return core.AssistantMessage{}, err
			}
			lastErr = err
			continue
		}

		partialMsg, err := consumeStreamEvents(ctx, llmStream, stream)
		if err != nil {
			if !core.IsContextOverflow(err) {
				return core.AssistantMessage{}, err
			}
			lastErr = err
			continue
		}
		return partialMsg, nil
	}
	if lastErr == nil {
		lastErr = &core.OverflowError{Message: "exhausted overflow retries"}
	}
	return core.AssistantMessage{}, lastErr
}

// defaultTokenCounter is a simple character-based fallback.
// Uses rune count (not byte count) for better UTF-8 accuracy.
func defaultTokenCounter(systemPrompt string, messages []core.Message, tools []core.Tool) int {
	total := estimateTokens(systemPrompt)
	for _, msg := range messages {
		switch m := msg.(type) {
		case core.UserMessage:
			if s, ok := m.Content.(string); ok {
				total += estimateTokens(s)
			}
		case core.AssistantMessage:
			for _, block := range m.Content {
				if tc, ok := block.(core.TextContent); ok {
					total += estimateTokens(tc.Text)
				}
			}
		case core.ToolResultMessage:
			for _, block := range m.Content {
				if tc, ok := block.(core.TextContent); ok {
					total += estimateTokens(tc.Text)
				}
			}
		}
		total += 4 // per-message overhead
	}
	for _, t := range tools {
		total += estimateTokens(t.Name) + estimateTokens(t.Description) + estimateTokens(string(t.Parameters)) + 10
	}
	return total
}

// estimateTokens approximates token count from a string using rune-based heuristic.
// ASCII: ~4 chars per token; non-ASCII (CJK, etc.): ~2 chars per token.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	ascii := 0
	nonAscii := 0
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			nonAscii++
		}
	}
	return (ascii + 1) / 4 + (nonAscii + 1) / 2
}
