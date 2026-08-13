package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"crux-agent-tui/internal/agent"
)

// toolResult creates a successful tool result.
func toolResult(text string) agent.AgentToolResult {
	return agent.ToolResult(text)
}

// toolError creates an error tool result.
func toolError(text string) agent.AgentToolResult {
	return agent.ToolError(text)
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
			description: bashDescription(),
			params:      `{"type":"object","properties":{"command":{"type":"string","description":"The shell command to execute"},"timeout":{"type":"integer","description":"Timeout in seconds (default: 60)"}},"required":["command"]}`,
			exec:        executeBash,
		},
		{
			name:        "read_file",
			description: readDescription(),
			params:      `{"type":"object","properties":{"path":{"type":"string","description":"Path to the file to read"},"offset":{"type":"integer","description":"Start from this line number (1-based, default: 1)"},"limit":{"type":"integer","description":"Max lines to read (default: all)"}},"required":["path"]}`,
			exec:        executeReadFile,
		},
		{
			name:        "write_file",
			description: writeDescription(),
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
		{
			name:        "glob",
			description: globDescription(),
			params:      `{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern to match files (e.g., '**/*.go', '*.txt')."},"path":{"type":"string","description":"Directory to search in (default: current directory)."}},"required":["pattern"]}`,
			exec:        executeGlob,
		},
		{
			name:        "grep",
			description: grepDescription(),
			params:      `{"type":"object","properties":{"pattern":{"type":"string","description":"Search pattern (substring or regex)."},"path":{"type":"string","description":"Directory or file to search in (default: current directory)."},"include":{"type":"string","description":"File pattern to include (e.g., '*.go')."},"regex":{"type":"boolean","description":"If true, treat pattern as regex (default false)."}},"required":["pattern"]}`,
			exec:        executeGrep,
		},
		{
			name:        "web_fetch",
			description: webFetchDescription(),
			params:      `{"type":"object","properties":{"url":{"type":"string","description":"The URL to fetch. Must be a valid HTTP(S) URL."},"max_length":{"type":"integer","description":"Maximum number of characters to return (default 10000)."}},"required":["url"]}`,
			exec:        executeWebFetch,
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
					// Wrap the raw output in a JSON string so the partial result
					// is always valid JSON (raw command output bytes are not).
					onUpdate(json.RawMessage(`{"output":`+strconv.Quote(string(chunk))+`}`))
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

// ── Cross-platform descriptions ──────────────────────────────────────────────

func bashDescription() string {
	switch runtime.GOOS {
	case "windows":
		return `Run a shell command and return its output.

CURRENT OS: Windows.
- Default shell: PowerShell. Most Unix commands (ls=Get-ChildItem, cat=Get-Content, pwd=Get-Location, rm=Remove-Item, cp=Copy-Item, mv=Move-Item) work via PowerShell aliases.
- Use '.\' prefix for executables in the current directory.
- Environment variables: $env:VAR_NAME (PowerShell) or %VAR_NAME% (cmd).`
	default:
		return `Run a shell command and return its output.

CURRENT OS: Unix (Linux/macOS).
- Default shell: sh (POSIX).
- Standard Unix commands (ls, cat, grep, find, git, etc.) are available.
- Use ./ prefix for executables in the current directory.
- Environment variables: $VAR_NAME.`
	}
}

func readDescription() string {
	if runtime.GOOS == "windows" {
		return "Read the contents of a file. Use Windows backslash paths (e.g. C:\\Users\\file.txt). Optionally limit by offset/line."
	}
	return "Read the contents of a file. Optionally limit by offset/line."
}

func writeDescription() string {
	if runtime.GOOS == "windows" {
		return "Write content to a file. Use Windows backslash paths. Creates parent directories as needed."
	}
	return "Write content to a file. Creates parent directories as needed."
}

func globDescription() string {
	if runtime.GOOS == "windows" {
		return "List files matching a glob pattern. Use backslash paths. Supports patterns like *.go, **/*.txt."
	}
	return "List files matching a glob pattern. Supports patterns like *.go, **/*.txt."
}

func grepDescription() string {
	if runtime.GOOS == "windows" {
		return "Search file contents for a pattern (substring or regex). Uses Go built-in file search (no external grep command). Use backslash paths."
	}
	return "Search file contents for a pattern (substring or regex). Uses Go built-in file search (no external grep command)."
}

func webFetchDescription() string {
	return "Fetch a URL and return its content as plain text. Strips HTML tags and truncates long pages."
}

// ── glob ─────────────────────────────────────────────────────────────────────

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func executeGlob(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args globArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if args.Pattern == "" {
		return toolError("pattern is required"), nil
	}

	root := "."
	if args.Path != "" {
		root = args.Path
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return toolError(fmt.Sprintf("glob: %v", err)), nil
	}

	var matches []string
	const maxMatches = 1000

	err = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(matches) >= maxMatches {
			return filepath.SkipDir
		}

		relPath, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}

		matched, err := filepath.Match(args.Pattern, info.Name())
		if err != nil {
			return nil
		}
		if matched {
			matches = append(matches, relPath)
		}

		if strings.Contains(args.Pattern, "**") {
			pattern := strings.ReplaceAll(args.Pattern, "**", "*")
			matched, _ = filepath.Match(pattern, relPath)
			if matched {
				matches = append(matches, relPath)
			}
		}

		return nil
	})
	if err != nil {
		return toolError(fmt.Sprintf("glob: %v", err)), nil
	}

	result := strings.Join(matches, "\n")
	if len(matches) == 0 {
		result = "No files found matching pattern: " + args.Pattern
	} else if len(matches) >= maxMatches {
		result += fmt.Sprintf("\n[... truncated at %d results ...]", maxMatches)
	}

	return toolResult(result), nil
}

// ── grep ─────────────────────────────────────────────────────────────────────

type grepArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Include string `json:"include"`
	Regex   bool   `json:"regex"`
}

func executeGrep(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args grepArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if args.Pattern == "" {
		return toolError("pattern is required"), nil
	}

	root := "."
	if args.Path != "" {
		root = args.Path
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return toolError(fmt.Sprintf("grep: %v", err)), nil
	}

	var re *regexp.Regexp
	if args.Regex {
		re, err = regexp.Compile(args.Pattern)
		if err != nil {
			return toolError(fmt.Sprintf("grep: invalid regex: %v", err)), nil
		}
	}

	var results []string
	const maxResults = 500
	const maxLineLength = 500

	err = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(results) >= maxResults {
			return filepath.SkipDir
		}
		if info.IsDir() {
			return nil
		}

		if args.Include != "" {
			matched, _ := filepath.Match(args.Include, info.Name())
			if !matched {
				return nil
			}
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		relPath, _ := filepath.Rel(absRoot, path)
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			if len(results) >= maxResults {
				break
			}
			lineNum++
			line := scanner.Text()

			matched := false
			if args.Regex {
				matched = re.MatchString(line)
			} else {
				matched = strings.Contains(line, args.Pattern)
			}

			if matched {
				displayLine := line
				if len(displayLine) > maxLineLength {
					displayLine = displayLine[:maxLineLength] + "..."
				}
				results = append(results, fmt.Sprintf("%s:%d:%s", relPath, lineNum, displayLine))
			}
		}
		return nil
	})
	if err != nil {
		return toolError(fmt.Sprintf("grep: %v", err)), nil
	}

	result := strings.Join(results, "\n")
	if len(results) == 0 {
		result = "No matches found for pattern: " + args.Pattern
	} else if len(results) >= maxResults {
		result += fmt.Sprintf("\n[... truncated at %d results ...]", maxResults)
	}

	return toolResult(result), nil
}

// ── web_fetch ────────────────────────────────────────────────────────────────

type webFetchArgs struct {
	URL       string `json:"url"`
	MaxLength int    `json:"max_length"`
}

func executeWebFetch(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args webFetchArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if args.URL == "" {
		return toolError("url is required"), nil
	}

	maxLen := args.MaxLength
	if maxLen <= 0 {
		maxLen = 10000
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
	if err != nil {
		return toolError(fmt.Sprintf("web_fetch: %v", err)), nil
	}
	httpReq.Header.Set("User-Agent", "CruxAgent/1.0")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return toolError(fmt.Sprintf("web_fetch: HTTP request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return toolError(fmt.Sprintf("web_fetch: HTTP %d %s", resp.StatusCode, resp.Status)), nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxLen*3)))
	if err != nil {
		return toolError(fmt.Sprintf("web_fetch: read error: %v", err)), nil
	}

	text := stripHTML(string(body))
	if len(text) > maxLen {
		text = text[:maxLen] + "\n[...truncated]"
	}

	return toolResult(text), nil
}

// ── HTML helpers (for web_fetch) ──────────────────────────────────────────────

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

func stripHTML(html string) string {
	scriptRe := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = scriptRe.ReplaceAllString(html, "")
	styleRe := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = styleRe.ReplaceAllString(html, "")

	text := htmlTagRe.ReplaceAllString(html, " ")

	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")

	spaceRe := regexp.MustCompile(`[ \t]+`)
	text = spaceRe.ReplaceAllString(text, " ")
	nlRe := regexp.MustCompile(`\n{3,}`)
	text = nlRe.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}
