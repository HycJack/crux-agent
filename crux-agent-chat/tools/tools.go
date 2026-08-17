// Package tools defines the coding tools available to the agent.
package tools

import (
	"encoding/json"

	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/crux-ai/core"
)

// ToolDef is a tool definition with its execution function.
type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Execute     engine.ToolExecuteFunc
}

func toolResult(text string) engine.AgentToolResult {
	return engine.AgentToolResult{
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: text}},
	}
}

func toolError(text string) engine.AgentToolResult {
	return engine.AgentToolResult{
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: text}},
		IsError: true,
	}
}

// AllTools returns all available coding tools as engine.AgentTool.
func AllTools() []engine.AgentTool {
	defs := []ToolDef{
		BashTool,
		ReadFileTool,
		WriteFileTool,
		ListFilesTool,
		EditFileTool,
		ReadImageTool,
	}

	tools := make([]engine.AgentTool, len(defs))
	for i, d := range defs {
		tools[i] = engine.AgentTool{
			Name:        d.Name,
			Description: d.Description,
			Parameters:  d.Parameters,
			Execute:     d.Execute,
		}
	}
	return tools
}
