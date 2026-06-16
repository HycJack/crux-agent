// Package tools provides built-in tools for the agent.
//
// Available tools:
//   - read_file  - read file contents (with optional line range)
//   - write_file - create or overwrite a file
//   - bash       - run a shell command
//   - glob       - list files matching a pattern
//   - grep       - search file contents
//
// All tools accept JSON parameters and return []core.ContentBlock results.
package tools

import (
	"encoding/json"
	"fmt"

	"github.com/hycjack/crux-ai/core"
	"crux-agent-runtime/agent"
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
func errResult(msg string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: textBlock(msg),
		IsError: true,
	}
}

// All returns the canonical built-in tool set.
func All() []agent.AgentTool {
	return []agent.AgentTool{
		Read(),
		Write(),
		Bash(),
		Glob(),
		Grep(),
	}
}
