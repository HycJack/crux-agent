package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"crux-agent-tui/internal/agent"
)

// toolResult creates a successful tool result.
func toolResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: text,
	}
}

// toolError creates an error tool result.
func toolError(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: text,
		IsError: true,
	}
}

// allTools returns all available coding tools.
func allTools() []agent.AgentTool {
	defs := []struct {
		name        string
		description string
		params      string
		exec        func(context.Context, string, json.RawMessage, func(json.RawMessage)) (agent.AgentToolResult, error)
	}{
		{
			name:        "bash",
			description: "Execute a shell command and return its output. On Windows, runs PowerShell. Use for running code, installing packages, git operations, etc.",
			params:      `{"type":"object","properties":{"command":{"type":"string","description":"The shell command to execute"},"timeout":{"type":"integer","description":"Timeout in seconds (default: 60)"}},"required":["command"]}`,
			exec:        executeBash,
		},
		{
			name:        "read_file",
			description: "Read the contents of a file. Returns file content with line numbers for easy reference.",
			params:      `{"type":"object","properties":{"path":{"type":"string","description":"Path to the file to read"},"offset":{"type":"integer","description":"Start from this line number (1-based, default: 1)"},"limit":{"type":"integer","description":"Max lines to read (default: all)"}},"required":["path"]}`,
			exec:        executeReadFile,
		},
		{
			name:        "write_file",
			description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Creates parent directories as needed.",
			params:      `{"type":"object","properties":{"path":{"type":"string","description":"Path to the file"},"content":{"type":"string","description":"Content to write"}},"required":["path","content"]}`,
			exec:        executeWriteFile,
		},
		{
			name:        "list_files",
			description: "List files and directories in a path. Returns names with / suffix for directories.",
			params:      `{"type":"object","properties":{"path":{"type":"string","description":"Directory path to list (default: current directory)"},"recursive":{"type":"boolean","description":"List recursively (default: false)"},"show_hidden":{"type":"boolean","description":"Show hidden files (default: false)"}}}`,
			exec:        executeListFiles,
		},
		{
			name:        "edit_file",
			description: "Edit a file by replacing a specific text with new text. The search text must be unique in the file.",
			params:      `{"type":"object","properties":{"path":{"type":"string","description":"Path to the file"},"old_text":{"type":"string","description":"Text to search for (must be unique in file)"},"new_text":{"type":"string","description":"Text to replace with"}},"required":["path","old_text","new_text"]}`,
			exec:        executeEditFile,
		},
	}

	tools := make([]agent.AgentTool, len(defs))
	for i, d := range defs {
		tools[i] = agent.AgentTool{
			Name:        d.name,
			Description: d.description,
			Parameters:  json.RawMessage(d.params),
			Execute:     d.exec,
		}
	}
	return tools
}

// ── bash ─────────────────────────────────────────────────────────────────────

func executeBash(ctx context.Context, id string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return toolError("invalid parameters: " + err.Error()), nil
	}

	timeout := 60 * time.Second
	if args.Timeout > 0 {
		timeout = time.Duration(args.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell, shellArgs, err := resolveShell(args.Command)
	if err != nil {
		return toolError(err.Error()), nil
	}

	cmd := exec.CommandContext(ctx, shell, shellArgs...)
	cmd.Dir, _ = os.Getwd()
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return toolError("failed to create stdout pipe: " + err.Error()), nil
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return toolError("failed to start command: " + err.Error()), nil
	}

	var outputBuf strings.Builder
	var mu sync.Mutex
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				mu.Lock()
				outputBuf.Write(chunk)
				mu.Unlock()
				if onUpdate != nil {
					onUpdate(json.RawMessage(chunk))
				}
			}
			if err != nil {
				return
			}
		}
	}()

	err = cmd.Wait()
	<-doneCh
	mu.Lock()
	outputStr := outputBuf.String()
	mu.Unlock()

	const maxOutput = 50000
	if len(outputStr) > maxOutput {
		outputStr = outputStr[:maxOutput] + "\n... (output truncated)"
	}

	if err != nil {
		exitCode := -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return toolError(fmt.Sprintf("Exit code: %d\n%s", exitCode, outputStr)), nil
	}
	return toolResult(strings.TrimRight(outputStr, "\n")), nil
}

func resolveShell(command string) (string, []string, error) {
	switch runtime.GOOS {
	case "windows":
		if path, prefix := findPowerShell(); path != "" {
			return path, append(prefix, command), nil
		}
		return "cmd.exe", []string{"/C", command}, nil
	default:
		if path, _ := exec.LookPath("bash"); path != "" {
			return path, []string{"-c", command}, nil
		}
		return "sh", []string{"-c", command}, nil
	}
}

func findPowerShell() (string, []string) {
	candidates := []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell"}
	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path, []string{"-NoProfile", "-NonInteractive", "-Command"}
		}
	}
	return "", nil
}

// ── read_file ────────────────────────────────────────────────────────────────

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

// ── write_file ───────────────────────────────────────────────────────────────

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

// ── list_files ───────────────────────────────────────────────────────────────

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

	entries, err := os.ReadDir(args.Path)
	if err != nil {
		return toolError(fmt.Sprintf("failed to list %s: %v", args.Path, err)), nil
	}

	var lines []string
	for _, e := range entries {
		name := e.Name()
		if !args.ShowHidden && strings.HasPrefix(name, ".") {
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

// ── edit_file ────────────────────────────────────────────────────────────────

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
		return toolError(fmt.Sprintf("old_text not found in %s. Make sure the text matches exactly.", args.Path)), nil
	}
	if count > 1 {
		idx := strings.Index(content, args.OldText)
		lineNum := strings.Count(content[:idx], "\n") + 1
		return toolError(fmt.Sprintf("old_text found %d times in %s (must be unique). First match at line %d.", count, args.Path, lineNum)), nil
	}

	newContent := strings.Replace(content, args.OldText, args.NewText, 1)
	if err := os.WriteFile(args.Path, []byte(newContent), 0644); err != nil {
		return toolError(fmt.Sprintf("failed to write %s: %v", args.Path, err)), nil
	}

	added := strings.Count(args.NewText, "\n") - strings.Count(args.OldText, "\n")
	idx := strings.Index(content, args.OldText)
	lineNum := strings.Count(content[:idx], "\n") + 1
	return toolResult(fmt.Sprintf("Successfully edited %s at line %d (%+d lines)", args.Path, lineNum, added)), nil
}
