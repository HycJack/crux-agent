package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"crux-agent-runtime/agent"
)

const grepSchema = `{
	"type": "object",
	"properties": {
		"pattern": { "type": "string", "description": "Search pattern (substring or regex)." },
		"path":    { "type": "string", "description": "Directory or file to search in (default: current directory)." },
		"include": { "type": "string", "description": "File pattern to include (e.g., '*.go')." },
		"regex":   { "type": "boolean", "description": "If true, treat pattern as regex (default false)." }
	},
	"required": ["pattern"]
}`

// Grep returns the grep tool.
func Grep() agent.AgentTool {
	return agent.AgentTool{
		Name:        "grep",
		Description: "Search file contents for a pattern (substring or regex).",
		Parameters:  mustSchema(grepSchema),
		Execute:     executeGrep,
	}
}

type grepArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Include string `json:"include"`
	Regex   bool   `json:"regex"`
}

func executeGrep(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args grepArgs
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
		return errResult(fmt.Sprintf("grep: %v", err)), nil
	}

	// Compile regex if needed
	var re *regexp.Regexp
	if args.Regex {
		re, err = regexp.Compile(args.Pattern)
		if err != nil {
			return errResult(fmt.Sprintf("grep: invalid regex: %v", err)), nil
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

		// Check include pattern
		if args.Include != "" {
			matched, _ := filepath.Match(args.Include, info.Name())
			if !matched {
				return nil
			}
		}

		// Search file
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
		return errResult(fmt.Sprintf("grep: %v", err)), nil
	}

	result := strings.Join(results, "\n")
	if len(results) == 0 {
		result = "No matches found for pattern: " + args.Pattern
	} else if len(results) >= maxResults {
		result += fmt.Sprintf("\n[... truncated at %d results ...]", maxResults)
	}

	details, _ := json.Marshal(map[string]any{
		"pattern": args.Pattern,
		"path":    absRoot,
		"matches": len(results),
		"regex":   args.Regex,
	})
	return agent.AgentToolResult{
		Content: textBlock(result),
		Details: details,
	}, nil
}
