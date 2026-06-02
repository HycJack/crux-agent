package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"crux-agent-runtime/agent"
)

// BashTool executes shell commands in a cross-platform way.
// On Windows, it uses PowerShell (pwsh or powershell.exe) by default;
// set CRUX_SHELL=cmd to use cmd.exe instead.
var BashTool = ToolDef{
	Name:        "bash",
	Description: "Execute a shell command and return its output. On Windows, runs PowerShell (or cmd if CRUX_SHELL=cmd). Use for running code, installing packages, git operations, etc.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"The shell command to execute"},"timeout":{"type":"integer","description":"Timeout in seconds (default: 60)"},"shell":{"type":"string","description":"Override shell: 'powershell' (default on Windows), 'cmd', or 'bash' (default on Unix)"}},"required":["command"]}`),
	Execute:     executeBash,
}

func executeBash(ctx context.Context, id string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
		Shell   string `json:"shell"`
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

	shell, shellArgs, err := resolveShell(args.Shell, args.Command)
	if err != nil {
		return toolError(err.Error()), nil
	}

	cmd := exec.CommandContext(ctx, shell, shellArgs...)
	cmd.Dir, _ = os.Getwd()
	cmd.Env = os.Environ()

	// Capture output line-by-line so long-running commands can emit
	// partial updates through onUpdate instead of appearing to hang.
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
					// Best-effort partial update; ignore failures.
					onUpdate(json.RawMessage(chunk))
				}
			}
			if err != nil {
				return
			}
		}
	}()

	err = cmd.Wait()
	<-doneCh // wait for the reader goroutine to finish
	mu.Lock()
	outputStr := outputBuf.String()
	mu.Unlock()

	const maxOutput = 50000
	truncated := false
	if len(outputStr) > maxOutput {
		outputStr = outputStr[:maxOutput] + "\n... (output truncated)"
		truncated = true
	}

	if err != nil {
		exitCode := -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return toolError(fmt.Sprintf("Exit code: %d\n%s", exitCode, outputStr)), nil
	}

	if truncated {
		return toolResult(outputStr + "\n(truncated)"), nil
	}
	return toolResult(strings.TrimRight(outputStr, "\n")), nil
}

// resolveShell chooses the shell to run the command in.
// override: optional explicit override; empty means pick default for the OS.
func resolveShell(override, command string) (string, []string, error) {
	choice := strings.ToLower(strings.TrimSpace(override))
	if choice == "" {
		if v := strings.ToLower(strings.TrimSpace(os.Getenv("CRUX_SHELL"))); v != "" {
			choice = v
		}
	}

	switch runtime.GOOS {
	case "windows":
		switch choice {
		case "", "powershell", "pwsh":
			if path, prefix := findPowerShell(); path != "" {
				args := append(prefix, command)
				return path, args, nil
			}
			return "", nil, fmt.Errorf("powershell not found on PATH; install PowerShell or set CRUX_SHELL=cmd")
		case "cmd", "cmd.exe":
			return "cmd.exe", []string{"/C", command}, nil
		case "bash", "sh":
			if path, _ := exec.LookPath("bash"); path != "" {
				return path, []string{"-c", command}, nil
			}
			return "", nil, fmt.Errorf("bash not found on PATH; install Git Bash or WSL, or set CRUX_SHELL=cmd")
		default:
			return "", nil, fmt.Errorf("unsupported shell %q on Windows (use powershell, cmd, or bash)", override)
		}
	default:
		switch choice {
		case "", "bash", "sh":
			if path, _ := exec.LookPath("bash"); path != "" {
				return path, []string{"-c", command}, nil
			}
			return "sh", []string{"-c", command}, nil
		case "zsh":
			if path, _ := exec.LookPath("zsh"); path != "" {
				return path, []string{"-c", command}, nil
			}
			return "", nil, fmt.Errorf("zsh not found on PATH")
		default:
			return "", nil, fmt.Errorf("unsupported shell %q (use bash or sh)", override)
		}
	}
}

// findPowerShell locates PowerShell, preferring pwsh (PowerShell 7+) over
// the legacy Windows PowerShell 5.x (powershell.exe). It returns the
// executable path and a base argv prefix to which the caller should append
// the actual command to run.
func findPowerShell() (string, []string) {
	candidates := []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell"}
	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path, []string{"-NoProfile", "-NonInteractive", "-Command"}
		}
	}
	return "", nil
}
