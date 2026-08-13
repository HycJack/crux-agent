// Package tools provides the built-in agent tools for the chat app.
//
// These tools were ported from crux-agent-runtime/tools so the app no
// longer depends on the runtime module. They are cross-platform
// (Windows / Linux / macOS) and produce agent-engine engine.AgentTool
// values directly.
package tools

import (
	"encoding/json"
	"fmt"

	"github.com/hycjack/agent-engine/engine"
	core "github.com/hycjack/crux-ai/core"
)

// mustSchema returns a json.RawMessage for the given literal.
// Panics on invalid JSON (caller error).
func mustSchema(s string) json.RawMessage {
	if !json.Valid([]byte(s)) {
		panic(fmt.Sprintf("tools: invalid schema literal: %s", s))
	}
	return json.RawMessage(s)
}

// textBlock is a tiny constructor for a single text content block.
func textBlock(s string) []core.ContentBlock {
	return []core.ContentBlock{core.TextContent{Type: "text", Text: s}}
}

// errResult is a helper for the canonical error result shape.
func errResult(msg string) engine.AgentToolResult {
	return engine.AgentToolResult{
		Content: textBlock(msg),
		IsError: true,
	}
}

// All returns the canonical built-in tool set.
func All() []engine.AgentTool {
	return []engine.AgentTool{
		Read(),
		Write(),
		Bash(),
		Glob(),
		Grep(),
		WebFetch(),
	}
}
