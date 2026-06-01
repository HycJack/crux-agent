package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"crux-agent-runtime/agent"
)

// BashTool executes shell commands.
var BashTool = ToolDef{
	Name:        "bash",
	Description: "Execute a shell command and return its output. Use for running code, installing packages, git operations, etc.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"The shell command to execute"},"timeout":{"type":"integer","description":"Timeout in seconds (default: 60)"}},"required":["command"]}`),
	Execute:     executeBash,
}

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

	cmd := exec.CommandContext(ctx, "bash", "-c", args.Command)
	cmd.Dir, _ = os.Getwd() // Fix #3: use actual working directory

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

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
		return toolResult(fmt.Sprintf("Exit code: %d\n%s", exitCode, outputStr)), nil
	}

	if truncated {
		return toolResult(outputStr + "\n(truncated)"), nil
	}
	return toolResult(strings.TrimRight(outputStr, "\n")), nil
}
