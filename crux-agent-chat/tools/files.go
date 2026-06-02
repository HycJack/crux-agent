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

// ReadFileTool reads the contents of a file with line numbers.
var ReadFileTool = ToolDef{
	Name:        "read_file",
	Description: "Read the contents of a file. Returns file content with line numbers for easy reference.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to the file to read"},"offset":{"type":"integer","description":"Start from this line number (1-based, default: 1)"},"limit":{"type":"integer","description":"Max lines to read (default: all)"}},"required":["path"]}`),
	Execute:     executeReadFile,
}

func executeReadFile(ctx context.Context, id string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return toolError("invalid parameters: " + err.Error()), nil
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return toolError(fmt.Sprintf("failed to read %s: %v", args.Path, err)), nil
	}

	lines := strings.Split(string(data), "\n")

	// Apply offset (1-based)
	if args.Offset < 0 {
		return toolError(fmt.Sprintf("offset must be >= 1, got %d", args.Offset)), nil
	}
	start := 0
	if args.Offset > 1 {
		start = args.Offset - 1
	}
	if start > len(lines) {
		return toolResult(fmt.Sprintf("(file has %d lines, offset %d is beyond end)", len(lines), args.Offset)), nil
	}
	if start == len(lines) {
		return toolResult(""), nil
	}

	end := len(lines)
	if args.Limit > 0 && start+args.Limit < end {
		end = start + args.Limit
	}

	// Format with line numbers: "  123| content"
	var buf strings.Builder
	for i := start; i < end; i++ {
		fmt.Fprintf(&buf, "%4d| %s\n", i+1, lines[i])
	}

	result := buf.String()
	const maxLen = 100000
	if len(result) > maxLen {
		result = result[:maxLen] + "\n... (file truncated)"
	}

	return toolResult(result), nil
}

// WriteFileTool writes content to a file.
var WriteFileTool = ToolDef{
	Name:        "write_file",
	Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Creates parent directories as needed.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to the file"},"content":{"type":"string","description":"Content to write"}},"required":["path","content"]}`),
	Execute:     executeWriteFile,
}

func executeWriteFile(ctx context.Context, id string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return toolError("invalid parameters: " + err.Error()), nil
	}

	dir := filepath.Dir(args.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return toolError(fmt.Sprintf("failed to create directory %s: %v", dir, err)), nil
	}

	if err := os.WriteFile(args.Path, []byte(args.Content), 0644); err != nil {
		return toolError(fmt.Sprintf("failed to write %s: %v", args.Path, err)), nil
	}

	return toolResult(fmt.Sprintf("Successfully wrote %d bytes to %s", len(args.Content), args.Path)), nil
}

// ListFilesTool lists directory contents.
var ListFilesTool = ToolDef{
	Name:        "list_files",
	Description: "List files and directories in a path. Returns names with / suffix for directories.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Directory path to list (default: current directory)"},"recursive":{"type":"boolean","description":"List recursively (default: false)"},"show_hidden":{"type":"boolean","description":"Show hidden files (those starting with .). Default: false"}}}`),
	Execute:     executeListFiles,
}

func executeListFiles(ctx context.Context, id string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args struct {
		Path       string `json:"path"`
		Recursive  bool   `json:"recursive"`
		ShowHidden bool   `json:"show_hidden"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return toolError("invalid parameters: " + err.Error()), nil
	}

	if args.Path == "" {
		args.Path = "."
	}

	if args.Recursive {
		return listRecursive(args.Path, args.ShowHidden)
	}
	return listFlat(args.Path, args.ShowHidden)
}

func listFlat(dir string, showHidden bool) (agent.AgentToolResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return toolError(fmt.Sprintf("failed to list %s: %v", dir, err)), nil
	}

	var lines []string
	for _, e := range entries {
		name := e.Name()
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		lines = append(lines, name)
	}
	if len(lines) == 0 {
		return toolResult("(empty directory)"), nil
	}
	return toolResult(strings.Join(lines, "\n")), nil
}

func listRecursive(dir string, showHidden bool) (agent.AgentToolResult, error) {
	var lines []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		name := info.Name()
		if !showHidden && strings.HasPrefix(name, ".") && name != "." {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			switch name {
			case "node_modules", "vendor", "__pycache__", ".git":
				return filepath.SkipDir
			}
		}

		rel, _ := filepath.Rel(dir, path)
		if rel == "." {
			return nil
		}
		if info.IsDir() {
			lines = append(lines, rel+"/")
		} else {
			lines = append(lines, rel)
		}
		return nil
	})
	if err != nil {
		return toolError(fmt.Sprintf("walk error: %v", err)), nil
	}
	if len(lines) == 0 {
		return toolResult("(empty directory)"), nil
	}
	return toolResult(strings.Join(lines, "\n")), nil
}

// EditFileTool performs a search-and-replace edit on a file.
var EditFileTool = ToolDef{
	Name:        "edit_file",
	Description: "Edit a file by replacing a specific text with new text. The search text must be unique in the file.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to the file"},"old_text":{"type":"string","description":"Text to search for (must be unique in file)"},"new_text":{"type":"string","description":"Text to replace with"}},"required":["path","old_text","new_text"]}`),
	Execute:     executeEditFile,
}

func executeEditFile(ctx context.Context, id string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return toolError("invalid parameters: " + err.Error()), nil
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return toolError(fmt.Sprintf("failed to read %s: %v", args.Path, err)), nil
	}

	content := string(data)
	count := strings.Count(content, args.OldText)
	if count == 0 {
		// Show context to help LLM fix the search text
		return toolError(fmt.Sprintf("old_text not found in %s. Make sure the text matches exactly (including whitespace and indentation).", args.Path)), nil
	}
	if count > 1 {
		// Fix #5: show first match context to help LLM narrow down
		idx := strings.Index(content, args.OldText)
		lineNum := strings.Count(content[:idx], "\n") + 1
		return toolError(fmt.Sprintf(
			"old_text found %d times in %s (must be unique). First match at line %d. Use read_file to find the exact text you want to replace.",
			count, args.Path, lineNum)), nil
	}

	newContent := strings.Replace(content, args.OldText, args.NewText, 1)
	if err := os.WriteFile(args.Path, []byte(newContent), 0644); err != nil {
		return toolError(fmt.Sprintf("failed to write %s: %v", args.Path, err)), nil
	}

	// Show diff summary. strings.Count("\n") counts newlines; a piece of text
	// with N newlines spans N lines (assuming no trailing newline) or N+1
	// (if it has a trailing newline). Use the trailing-newline aware count
	// so the diff is accurate in both cases.
	added := countLines(args.NewText) - countLines(args.OldText)
	idx := strings.Index(content, args.OldText)
	lineNum := strings.Count(content[:idx], "\n") + 1
	return toolResult(fmt.Sprintf("Successfully edited %s at line %d (%+d lines)", args.Path, lineNum, added)), nil
}

// countLines returns the number of lines in s. A trailing newline is
// counted as ending the last line but does not introduce a new empty one.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
