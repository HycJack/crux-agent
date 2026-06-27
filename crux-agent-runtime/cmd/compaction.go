package main

import (
	"fmt"
	"os"

	"github.com/hycjack/crux-ai/core"

	"crux-agent-runtime/agent"
	ctxpkg "crux-agent-runtime/context"
)

// buildCompactionConfig wires up automatic context-window compaction.
//
// Strategy:
//  1. Pre-call: when estimated tokens exceed MaxTokens (60k, well below
//     Xiaomi's 128k window), run a chained compactor:
//     LLM summarize (lossy, preserves intent) → slide window (cheap).
//  2. Overflow retry: if the LLM still rejects with a context-overflow
//     error, force-compact and retry up to OverflowRetries times. The
//     retried call goes out with a strictly shorter context, so it
//     should fit.
//
// The summarizer uses the same model as the agent — there's no point
// running a smarter model just for this, but it must be cheap (timeout 30s).
func buildCompactionConfig(model core.Model, apiKey string) agent.CompactionConfig {
	summarizer := &ctxpkg.LLMSummarize{
		KeepLast:   8,
		MinTrigger: 20,
		Summarize:  buildSummarizeFunc(model, apiKey),
	}

	chained := &ctxpkg.ChainedCompactor{
		Compactors: []ctxpkg.Compactor{
			summarizer,                // try lossy summary first
			ctxpkg.NewSlideWindow(40), // fall back to plain slide window
		},
	}

	return agent.CompactionConfig{
		Compactor:       chained,
		MaxTokens:       60000, // trigger threshold
		ReserveTokens:   4096,
		OverflowRetries: 2,
		OnCompact: func(prevTokens, newTokens, prevMsgs, newMsgs int) {
			fmt.Fprintf(os.Stderr,
				"[compaction] %d tokens, %d msgs → %d tokens, %d msgs\n",
				prevTokens, prevMsgs, newTokens, newMsgs)
		},
	}
}