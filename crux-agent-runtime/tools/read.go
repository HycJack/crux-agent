package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"crux-agent-runtime/agent"
)

const readSchema = `{
	"type": "object",
	"properties": {
		"filePath": { "type": "string", "description": "Absolute or working-directory-relative path of the file to read." },
		"offset":   { "type": "integer", "description": "0-based line offset to start reading at (optional)." },
		"limit":    { "type": "integer", "description": "Maximum number of lines to return (optional)." }
	},
	"required": ["filePath"]
}`

// Read returns the read_file tool.
func Read() agent.AgentTool {
	return agent.AgentTool{
		Name:        "read_file",
		Description: "Read the contents of a file. Optionally limit by offset/line.",
		Parameters:  mustSchema(readSchema),
		Execute:     executeRead,
	}
}

type readArgs struct {
	FilePath string `json:"filePath"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

func executeRead(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args readArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if args.FilePath == "" {
		return errResult("filePath is required"), nil
	}

	// Resolve absolute path
	absPath, err := filepath.Abs(args.FilePath)
	if err != nil {
		return errResult(fmt.Sprintf("read_file: %v", err)), nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return errResult(fmt.Sprintf("read_file: %v", err)), nil
	}

	text := string(data)
	if args.Offset > 0 || args.Limit > 0 {
		lines := strings.Split(text, "\n")
		start := args.Offset
		if start < 0 {
			start = 0
		}
		if start > len(lines) {
			start = len(lines)
		}
		end := len(lines)
		if args.Limit > 0 && start+args.Limit < end {
			end = start + args.Limit
		}
		text = strings.Join(lines[start:end], "\n")
	}

	// Trim very long outputs to prevent context overflow
	const maxReadChars = 200_000
	truncated := false
	if len(text) > maxReadChars {
		text = text[:maxReadChars] + "\n\n[... truncated ...]"
		truncated = true
	}

	details := map[string]any{
		"filePath":  absPath,
		"bytes":     len(data),
		"offset":    args.Offset,
		"limit":     args.Limit,
		"truncated": truncated,
	}
	detailJSON, _ := json.Marshal(details)
	return agent.AgentToolResult{
		Content: textBlock(text),
		Details: detailJSON,
	}, nil
}
