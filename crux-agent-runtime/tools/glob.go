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

const globSchema = `{
	"type": "object",
	"properties": {
		"pattern": { "type": "string", "description": "Glob pattern to match files (e.g., '**/*.go', '*.txt')." },
		"path":    { "type": "string", "description": "Directory to search in (default: current directory)." }
	},
	"required": ["pattern"]
}`

// Glob returns the glob tool.
func Glob() agent.AgentTool {
	return agent.AgentTool{
		Name:        "glob",
		Description: "List files matching a glob pattern.",
		Parameters:  mustSchema(globSchema),
		Execute:     executeGlob,
	}
}

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func executeGlob(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args globArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if args.Pattern == "" {
		return errResult("pattern is required"), nil
	}

	root := "."
	if args.Path != "" {
		root = args.Path
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return errResult(fmt.Sprintf("glob: %v", err)), nil
	}

	var matches []string
	const maxMatches = 1000

	err = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(matches) >= maxMatches {
			return filepath.SkipDir
		}

		// Get relative path
		relPath, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}

		// Match pattern
		matched, err := filepath.Match(args.Pattern, info.Name())
		if err != nil {
			return nil
		}
		if matched {
			matches = append(matches, relPath)
		}

		// Also check with ** pattern
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
		return errResult(fmt.Sprintf("glob: %v", err)), nil
	}

	result := strings.Join(matches, "\n")
	if len(matches) == 0 {
		result = "No files found matching pattern: " + args.Pattern
	} else if len(matches) >= maxMatches {
		result += fmt.Sprintf("\n[... truncated at %d results ...]", maxMatches)
	}

	details, _ := json.Marshal(map[string]any{
		"pattern": args.Pattern,
		"path":    absRoot,
		"count":   len(matches),
	})
	return agent.AgentToolResult{
		Content: textBlock(result),
		Details: details,
	}, nil
}
