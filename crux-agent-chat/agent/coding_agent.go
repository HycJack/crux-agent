// Package agent wires up the coding agent with tools and configuration.
package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"crux-agent-chat/config"
	"crux-agent-chat/harness"
	"crux-agent-chat/tools"
	"crux-agent-harness/approval"
	agentruntime "crux-agent-runtime/agent"
	"github.com/hycjack/crux-ai/core"
)

// maxToolResultSize is the maximum size of a tool result content before it gets
// trimmed during context cleanup. Tool results larger than this are replaced
// with a short metadata line, because the LLM has already consumed the full
// content in the previous turn and no longer needs the raw data.
const maxToolResultSize = 2000

// Options configures NewCodingAgentWithHarness.
type Options struct {
	Config  *config.Config
	Harness *harness.Harness
}

// NewCodingAgent creates a fully configured coding agent from the config.
func NewCodingAgent(cfg *config.Config) *agentruntime.Agent {
	return NewCodingAgentWithHarness(Options{Config: cfg})
}

// NewCodingAgentWithHarness creates a coding agent that integrates with
// the harness (approval, skills, context, session, token tracking).
//
// If opts.Harness is nil, falls back to the standalone behavior
// (no approval, no skills, no compaction).
func NewCodingAgentWithHarness(opts Options) *agentruntime.Agent {
	cfg := opts.Config
	h := opts.Harness

	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = config.DefaultSystemPrompt
	}

	wd, _ := os.Getwd()
	systemPrompt += fmt.Sprintf("\n\nCurrent working directory: %s", wd)
	systemPrompt += fmt.Sprintf("\nCurrent time: %s", time.Now().Format(time.RFC3339))
	systemPrompt += fmt.Sprintf("\nOperating system: %s/%s", runtime.GOOS, runtime.GOARCH)

	if h != nil {
		// Append a skills section so the model knows about the
		// specialized instructions it can use.
		systemPrompt = h.AppendSkillsToPrompt(systemPrompt)
	}

	model := cfg.GetModel()
	model.Headers = make(map[string]string)

	agentTools := tools.AllTools()

	state := &agentruntime.AgentState{
		Model:        model,
		SystemPrompt: systemPrompt,
		Tools:        agentTools,
		GetApiKey: func() string {
			return cfg.APIKey
		},
		SimpleStreamOptions: core.SimpleStreamOptions{
			StreamOptions: core.StreamOptions{
				APIKey: cfg.APIKey,
			},
		},
		// GetFollowUpMessages can be used to inject messages after each turn.
		// Example: auto-summarization after every 5 messages.
		GetFollowUpMessages: func() []core.Message {
			// Customize this based on your needs.
			// Example: Return nil to not inject any follow-up messages.
			//
			// To enable auto-summarization, you could do something like:
			// if messageCount >= 5 {
			//     return []core.Message{
			//         core.UserMessage{
			//             Role:    "user",
			//             Content: "请总结一下我们刚才的对话。",
			//         },
			//     }
			// }
			return nil
		},
	}

	if h != nil {
		state.BeforeToolCall = func(ctx agentruntime.BeforeToolCallContext) *agentruntime.ToolCallBlock {
			return approvalHook(h, ctx)
		}

		// Load persisted messages from the session as the agent's initial
		// message list.  This ensures session isolation: each program start
		// loads its own session history and appends to it across turns.
		state.Messages = h.BuildContext()

		// contextToolsCore caches the core.Tool list for context checking.
		var contextToolsOnce sync.Once
		var contextTools []core.Tool
		// TransformContext is called before every LLM call inside the agent loop.
		// It performs two things:
		//   1. Trim large ToolResult payloads that the LLM has already consumed
		//      (e.g. read_file contents, bash output) — zero-cost cleanup.
		//   2. If still over budget, run LLM-based compaction on older messages.
		state.TransformContext = func(messages []core.Message) []core.Message {
			contextToolsOnce.Do(func() {
				contextTools = harness.AgentToolsToCore(tools.AllTools())
			})

			// Step 1: trim large tool results that the LLM no longer needs.
			trimmed := tidyContext(messages)

			// Step 2: if still over budget, run compaction on the trimmed list.
			est := h.EstimateUsage(state.SystemPrompt, trimmed, contextTools)
			log.Printf("TransformContext: %d tokens, %d messages", est.Used, len(trimmed))

			if !h.ShouldCompact(state.SystemPrompt, trimmed, contextTools) {
				return trimmed
			}
			log.Printf("TransformContext: compaction triggered")
			compactCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			newMsgs, res, err := h.Compact(compactCtx, state.SystemPrompt, trimmed, contextTools)
			if err != nil || res == nil {
				log.Printf("TransformContext: compaction failed or not needed: %v", err)
				return trimmed
			}
			_ = h.RecordCompaction(res.Summary, res.TokensBefore)
			log.Printf("TransformContext: compacted %d -> %d tokens", res.TokensBefore, res.TokensAfter)
			return newMsgs
		}
	}

	agent := agentruntime.New(agentruntime.AgentOptions{InitialState: state})

	if h != nil {
		// Subscribe the harness collector to agent events for run statistics.
		agent.Subscribe(h.NewAgentSubscriber())
	}

	return agent
}

// tidyContext trims overly large tool results that the LLM has already
// consumed and no longer needs the raw data for. This is a zero-cost
// alternative to LLM-based compaction for big payloads like file contents
// or command output.
//
// It preserves the most recent 3 assistant+tool-result pairs in full so
// the model can still reference the current turn's data.
func tidyContext(messages []core.Message) []core.Message {
	// Keep the last keepPairs * 2 messages protected from trimming:
	//   protected messages = 3 assistant msgs + their tool results
	//   In the worst case each assistant is followed by multiple tool results,
	//   so we use a generous upper bound of keepPairs*4 messages.
	const keepPairs = 3
	protectedCount := keepPairs * 4
	if protectedCount > len(messages) {
		protectedCount = len(messages)
	}
	trimLimit := len(messages) - protectedCount

	result := make([]core.Message, len(messages))
	for i, msg := range messages {
		if i < trimLimit {
			result[i] = trimMessage(msg)
		} else {
			result[i] = msg
		}
	}
	return result
}

// trimMessage reduces the content of a message if it carries a large payload
// that the LLM no longer needs (file contents, command output, image data).
func trimMessage(msg core.Message) core.Message {
	toolMsg, ok := msg.(core.ToolResultMessage)
	if !ok {
		return msg
	}

	// Only trim tools whose results are "read once" — the LLM read the file
	// in a previous turn and no longer needs the raw content.
	switch toolMsg.ToolName {
	case "read_file", "bash", "read_image":
		// Check total content length.
		totalLen := 0
		hasImage := false
		for _, block := range toolMsg.Content {
			if tc, ok := block.(core.TextContent); ok {
				totalLen += len(tc.Text)
			}
			if _, ok := block.(core.ImageContent); ok {
				hasImage = true
			}
		}
		if totalLen <= maxToolResultSize && !hasImage {
			return msg // small enough, keep as-is
		}

		// Replace with a compact metadata line.
		replacement := buildTrimmedSummary(toolMsg)
		toolMsg.Content = []core.ContentBlock{
			core.TextContent{Type: "text", Text: replacement},
		}
		return toolMsg

	default:
		return msg
	}
}

// buildTrimmedSummary returns a one-line summary of what the tool result
// contained, so the model knows what happened without the full payload.
func buildTrimmedSummary(msg core.ToolResultMessage) string {
	// Count lines and estimate size.
	totalLines := 0
	totalChars := 0
	for _, block := range msg.Content {
		if tc, ok := block.(core.TextContent); ok {
			totalLines += strings.Count(tc.Text, "\n")
			if !strings.HasSuffix(tc.Text, "\n") {
				totalLines++
			}
			totalChars += len(tc.Text)
		}
	}

	switch msg.ToolName {
	case "read_file":
		// Extract the file path if it appears in the first text block.
		return fmt.Sprintf("[read_file result trimmed: ~%d lines / %d chars]", totalLines, totalChars)
	case "bash":
		return fmt.Sprintf("[bash output trimmed: ~%d lines / %d chars]", totalLines, totalChars)
	case "read_image":
		return "[image data trimmed]"
	default:
		return fmt.Sprintf("[%s result trimmed: ~%d chars]", msg.ToolName, totalChars)
	}
}

// approvalHook bridges the harness's approval gate to the agent's
// BeforeToolCall hook. It blocks tools when the gate says so, or
// delegates the ask to the harness (which prompts on stdin).
func approvalHook(h *harness.Harness, ctx agentruntime.BeforeToolCallContext) (blockResult *agentruntime.ToolCallBlock) {
	// Recover from any panic during approval to prevent crashing the agent loop.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("🔒 approvalHook: panic recovered: %v", r)
			stack := debug.Stack()
			log.Printf("🔒 approvalHook: stack: %s", string(stack))
			// On panic, block the tool to be safe rather than crashing the loop.
			blockResult = &agentruntime.ToolCallBlock{
				Block:  true,
				Reason: fmt.Sprintf("approval hook panicked: %v", r),
			}
		}
	}()

	req := approval.Request{
		ToolName: ctx.ToolCall.Name,
		ToolID:   ctx.ToolCall.ID,
		Args:     ctx.Args,
	}
	log.Printf("🔒 approvalHook: evaluating tool call: %s", req.ToolName)
	res := h.EvaluateApproval(req)
	log.Printf("🔒 approvalHook: decision: %v, reason: %s", res.Decision, res.Reason)
	switch res.Decision {
	case approval.DecisionBlock:
		log.Printf("🔒 approvalHook: tool call blocked: %s", res.Reason)
		return &agentruntime.ToolCallBlock{Block: true, Reason: res.Reason}
	case approval.DecisionAllow:
		log.Printf("🔒 approvalHook: tool call allowed")
		return nil
	case approval.DecisionAsk:
		log.Printf("🔒 approvalHook: asking user for approval")
		if !h.AskUser(req) {
			log.Printf("🔒 approvalHook: user denied")
			return &agentruntime.ToolCallBlock{Block: true, Reason: "user denied"}
		}
		log.Printf("🔒 approvalHook: user approved")
		return nil
	}
	return nil
}

// RunOnce runs a single user query through the agent. The prompt may be a
// plain string (text-only) or a slice of core.ContentBlock (multimodal,
// e.g. text + image attachments).
func RunOnce(ctx context.Context, a *agentruntime.Agent, prompt any) ([]core.Message, error) {
	return a.Run(ctx, core.UserMessage{
		Role:      "user",
		Content:   prompt,
		Timestamp: time.Now(), // Fix #2: set timestamp
	})
}
