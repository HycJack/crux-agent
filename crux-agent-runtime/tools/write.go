package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"crux-agent-runtime/agent"
)

const writeSchema = `{
	"type": "object",
	"properties": {
		"filePath": { "type": "string", "description": "Absolute or working-directory-relative path of the file to write." },
		"content":  { "type": "string", "description": "Content to write to the file." },
		"append":   { "type": "boolean", "description": "If true, append to the file instead of overwriting (default false)." }
	},
	"required": ["filePath", "content"]
}`

// Write returns the write_file tool.
func Write() agent.AgentTool {
	return agent.AgentTool{
		Name:        "write_file",
		Description: "Create or overwrite a file. Optionally append to existing file.",
		Parameters:  mustSchema(writeSchema),
		Execute:     executeWrite,
	}
}

type writeArgs struct {
	FilePath string `json:"filePath"`
	Content  string `json:"content"`
	Append   bool   `json:"append"`
}

func executeWrite(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args writeArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if args.FilePath == "" {
		return errResult("filePath is required"), nil
	}

	absPath, err := filepath.Abs(args.FilePath)
	if err != nil {
		return errResult(fmt.Sprintf("write_file: %v", err)), nil
	}

	// Create directory if needed
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return errResult(fmt.Sprintf("write_file: failed to create directory: %v", err)), nil
	}

	// Write or append
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if args.Append {
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}

	f, err := os.OpenFile(absPath, flag, 0644)
	if err != nil {
		return errResult(fmt.Sprintf("write_file: %v", err)), nil
	}
	defer f.Close()

	n, err := f.WriteString(args.Content)
	if err != nil {
		return errResult(fmt.Sprintf("write_file: %v", err)), nil
	}

	details := map[string]any{
		"filePath": absPath,
		"bytes":    n,
		"append":   args.Append,
	}
	detailJSON, _ := json.Marshal(details)
	return agent.AgentToolResult{
		Content: textBlock(fmt.Sprintf("Successfully wrote %d bytes to %s", n, absPath)),
		Details: detailJSON,
	}, nil
}
