package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"crux-agent-runtime/agent"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// sysProcAttrHide returns a *syscall.SysProcAttr that hides the console
// window on Windows. On other platforms it returns nil.
func sysProcAttrHide() *syscall.SysProcAttr {
	if runtime.GOOS != "windows" {
		return nil
	}
	return &syscall.SysProcAttr{HideWindow: true}
}

// decodeWindowsOutput decodes a byte slice from the Windows active code
// page (usually GBK) into UTF-8. On non-Windows it's a no-op.
func decodeWindowsOutput(b []byte) string {
	if runtime.GOOS != "windows" || len(b) == 0 {
		return string(b)
	}
	// Try GBK first (most common on Chinese Windows).
	if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(b); err == nil {
		return string(decoded)
	}
	// Fallback: assume it's already UTF-8.
	return string(b)
}

const bashSchema = `{
	"type": "object",
	"properties": {
		"command": { "type": "string", "description": "Shell command to run." },
		"timeout": { "type": "integer", "description": "Maximum runtime in milliseconds (default 30000)." },
		"shell":   { "type": "string", "description": "Override the shell binary. 'auto' picks cmd.exe on Windows and sh elsewhere.", "enum": ["auto", "cmd", "powershell", "bash", "sh"] }
	},
	"required": ["command"]
}`

// DefaultBashTimeout is the default max-runtime for a single bash call.
const DefaultBashTimeout = 30 * time.Second

// Bash returns the bash tool.
func Bash() agent.AgentTool {
	return agent.AgentTool{
		Name:        "bash",
		Description: "Run a shell command and return stdout, stderr, and exit code.",
		Parameters:  mustSchema(bashSchema),
		Execute:     executeBash,
	}
}

type bashArgs struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"` // ms
	Shell   string `json:"shell"`
}

func executeBash(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args bashArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if args.Command == "" {
		return errResult("command is required"), nil
	}

	timeout := DefaultBashTimeout
	if args.Timeout > 0 {
		timeout = time.Duration(args.Timeout) * time.Millisecond
	}

	shell, shellArgs := pickShell(args.Shell, runtime.GOOS)

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, shell, shellArgs...)
	cmd.Args = append(cmd.Args, args.Command)
	cmd.SysProcAttr = sysProcAttrHide()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else if errors.Is(err, context.DeadlineExceeded) {
			exitCode = 124 // Standard timeout exit code
		} else {
			return errResult(fmt.Sprintf("bash: %v", err)), nil
		}
	}

	stdoutStr := decodeWindowsOutput(stdout.Bytes())
	stderrStr := decodeWindowsOutput(stderr.Bytes())
	combined := stdoutStr
	if stderrStr != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += "[stderr]\n" + stderrStr
	}

	// Cap output to prevent context overflow
	const maxOut = 100_000
	truncated := false
	if len(combined) > maxOut {
		combined = combined[:maxOut] + "\n[... truncated ...]"
		truncated = true
	}

	isErr := exitCode != 0
	details, _ := json.Marshal(map[string]any{
		"command":   args.Command,
		"exitCode":  exitCode,
		"shell":     shell,
		"truncated": truncated,
		"timedOut":  runCtx.Err() == context.DeadlineExceeded,
	})
	return agent.AgentToolResult{
		Content: textBlock(combined),
		Details: details,
		IsError: isErr,
	}, nil
}

// pickShell chooses the shell binary and its prefix arguments.
func pickShell(name, goos string) (binary string, prefix []string) {
	if name == "" || name == "auto" {
		if goos == "windows" {
			return "cmd.exe", []string{"/c"}
		}
		return "sh", []string{"-c"}
	}
	switch name {
	case "cmd":
		return "cmd.exe", []string{"/c"}
	case "powershell":
		return "powershell", []string{"-NoProfile", "-Command"}
	case "bash":
		return "bash", []string{"-c"}
	case "sh":
		return "sh", []string{"-c"}
	}
	// Fallback: treat the value as a binary name
	if goos == "windows" {
		return name, []string{"/c"}
	}
	return name, []string{"-c"}
}
