// Package hooks provides adapter callbacks that integrate crux-memory
// with the crux-agent-runtime agent loop.
//
// The OnEvent callback fires on every agent event. We filter for
// user/assistant message completion events and forward them to the pipeline.
package hooks

import (
	"context"
	"log"
	"sync"

	"github.com/crux-memory/crux-memory/l0"
	"github.com/crux-memory/crux-memory/pipeline"
)

// AgentEvent is the minimal subset of crux-agent-runtime's AgentEvent
// that we care about. We define it locally to avoid the import cycle and
// to keep crux-memory agent-runtime agnostic — adapters translate.
type AgentEvent struct {
	Type      string // "user_message" | "assistant_message" | "tool_call" | "tool_result" | ...
	SessionID string
	Role      string // "user" | "assistant" | "tool"
	Content   string
}

// AgentHook wires OnEvent → pipeline.Capture. Safe for concurrent use.
type AgentHook struct {
	p   *pipeline.Pipeline
	mu  sync.Mutex
	run map[string]bool // sessionID → already-running
}

// NewAgentHook constructs a hook bound to the pipeline.
func NewAgentHook(p *pipeline.Pipeline) *AgentHook {
	return &AgentHook{p: p, run: make(map[string]bool)}
}

// OnEvent is the callback to plug into AgentLoopConfig.OnEvent.
//
// All event handling is best-effort: a panic in the LLM stage must not
// crash the agent loop, so callers should wrap this in defer/recover if
// they want strict guarantees. We log instead.
func (h *AgentHook) OnEvent(ctx context.Context, ev AgentEvent) {
	switch ev.Type {
	case "user_message", "assistant_message":
		if ev.SessionID == "" || ev.Content == "" {
			return
		}
		role := ev.Role
		if role == "" {
			if ev.Type == "user_message" {
				role = "user"
			} else {
				role = "assistant"
			}
		}
		if err := h.p.Capture(ctx, ev.SessionID, toRole(role), ev.Content); err != nil {
			log.Printf("[memory-hook] capture failed session=%s: %v", ev.SessionID, err)
			return
		}
		// Trigger a MaybeTick asynchronously so the agent loop isn't blocked.
		go h.tickAsync(ctx, ev.SessionID)
	}
}

func (h *AgentHook) tickAsync(ctx context.Context, sessionID string) {
	h.mu.Lock()
	if h.run[sessionID] {
		h.mu.Unlock()
		return
	}
	h.run[sessionID] = true
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.run, sessionID)
		h.mu.Unlock()
	}()

	if err := h.p.MaybeTick(ctx); err != nil {
		log.Printf("[memory-hook] tick failed: %v", err)
	}
}

func toRole(s string) l0.Role {
	switch s {
	case "user":
		return l0.RoleUser
	case "assistant":
		return l0.RoleAssistant
	case "system":
		return l0.RoleSystem
	case "tool":
		return l0.RoleTool
	}
	return l0.RoleUser
}