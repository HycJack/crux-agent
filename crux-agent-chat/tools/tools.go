// Package tools defines the coding tools available to the agent.
package tools

import (
	"encoding/json"

	"crux-agent-runtime/agent"
	"github.com/hycjack/crux-ai/core"
)

// ToolDef is a tool definition with its execution function.
type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Execute     agent.ToolExecuteFunc
}

func toolResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: text}},
	}
}

func toolError(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: text}},
		IsError: true,
	}
}

// AllTools returns all available coding tools as agent.AgentTool.
func AllTools() []agent.AgentTool {
	defs := []ToolDef{
		BashTool,
		ReadFileTool,
		WriteFileTool,
		ListFilesTool,
		EditFileTool,
		ReadImageTool,
	}

	tools := make([]agent.AgentTool, len(defs))
	for i, d := range defs {
		tools[i] = agent.AgentTool{
			Name:        d.Name,
			Description: d.Description,
			Parameters:  d.Parameters,
			Execute:     d.Execute,
		}
	}
	return tools
}
