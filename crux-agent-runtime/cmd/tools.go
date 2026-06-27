package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hycjack/crux-ai/core"

	"crux-agent-runtime/agent"
	"crux-agent-runtime/memory"
)

// textToolParams is the parameter schema every demo text tool uses.
// Kept open (no required fields) so individual tools can extend it via
// a generic-typed extension without re-declaring boilerplate.
type textToolParams map[string]any

// textTool builds a single-input text tool whose Execute function:
//  1. unmarshals params into T
//  2. calls parse(T) to produce a result string
//  3. wraps the result into a ToolResultContent block
//
// This eliminates the ~10 lines of `json.Unmarshal + err wrap` boilerplate
// that each tool definition used to repeat.
func textTool[T any](name, description, paramsJSON string, parse func(T) (string, error)) agent.AgentTool {
	return agent.AgentTool{
		Name:        name,
		Description: description,
		Parameters:  json.RawMessage(paramsJSON),
		Execute: func(_ context.Context, _ string, params json.RawMessage, _ func(json.RawMessage)) (agent.AgentToolResult, error) {
			var args T
			if err := json.Unmarshal(params, &args); err != nil {
				return toolError("invalid args: " + err.Error()), nil
			}
			out, err := parse(args)
			if err != nil {
				return toolError(name+" error: "+err.Error()), nil
			}
			return toolOK(out), nil
		},
	}
}

// toolOK wraps a successful text result into an AgentToolResult.
func toolOK(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: text}},
	}
}

// toolError wraps an error message into an AgentToolResult with IsError=true.
func toolError(msg string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: msg}},
		IsError: true,
	}
}

// echoTool returns a tool that echoes its input text back to the user.
func echoTool() agent.AgentTool {
	return textTool(
		"echo",
		"Echoes back the input text. Use this when the user asks to echo or repeat something.",
		`{"type":"object","properties":{"text":{"type":"string","description":"The text to echo"}},"required":["text"]}`,
		func(args struct {
			Text string `json:"text"`
		}) (string, error) {
			return "Echo: " + args.Text, nil
		},
	)
}

// calculatorTool returns a tool that evaluates a basic arithmetic
// expression (numbers separated by + - * /).
func calculatorTool() agent.AgentTool {
	return textTool(
		"calculator",
		"Performs basic arithmetic. Use this for math questions.",
		`{"type":"object","properties":{"expression":{"type":"string","description":"Math expression, e.g. 2+2 or 10*5"}},"required":["expression"]}`,
		func(args struct {
			Expression string `json:"expression"`
		}) (string, error) {
			v, err := safeEval(args.Expression)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s = %g", args.Expression, v), nil
		},
	)
}

// getTimeTool returns a tool that reports the current local time in RFC3339.
func getTimeTool() agent.AgentTool {
	return textTool(
		"get_time",
		"Returns the current local time in RFC3339 format. Use this when the user asks for the current time or date.",
		`{"type":"object","properties":{}}`,
		func(_ struct{}) (string, error) {
			return "Current time: " + time.Now().Format(time.RFC3339), nil
		},
	)
}

// rememberTool returns a tool that stores a key=value pair into long-term
// memory. The memory reference is closed over so the tool writes directly
// to the same backing store the autolearner reads from.
func rememberTool(mem *memory.Memory) agent.AgentTool {
	return textTool(
		"remember",
		"Explicitly store a key-value pair into long-term memory. Use this when the user asks to remember something or wants to save a preference.",
		`{"type":"object","properties":{"key":{"type":"string","description":"Memory key, e.g. user.name"},"value":{"type":"string","description":"Value to store"}},"required":["key","value"]}`,
		func(args struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}) (string, error) {
			if args.Key == "" {
				return "", fmt.Errorf("key is required")
			}
			mem.Set(args.Key, args.Value)
			return fmt.Sprintf("Remembered: %s=%s", args.Key, args.Value), nil
		},
	)
}

// safeEval evaluates a simple arithmetic expression. It supports
// + - * / and decimal numbers. Intended for the demo calculator tool
// only — DO NOT use in production.
func safeEval(expr string) (float64, error) {
	pos := 0

	nextNum := func() (float64, error) {
		for pos < len(expr) && expr[pos] == ' ' {
			pos++
		}
		if pos >= len(expr) {
			return 0, fmt.Errorf("unexpected end")
		}
		start := pos
		hasDigit := false
		for pos < len(expr) && ((expr[pos] >= '0' && expr[pos] <= '9') || expr[pos] == '.') {
			if expr[pos] >= '0' && expr[pos] <= '9' {
				hasDigit = true
			}
			pos++
		}
		if !hasDigit {
			return 0, fmt.Errorf("expected number at pos %d", start)
		}
		var n float64
		fmt.Sscanf(expr[start:pos], "%f", &n)
		return n, nil
	}

	result, err := nextNum()
	if err != nil {
		return 0, err
	}

	for pos < len(expr) {
		for pos < len(expr) && expr[pos] == ' ' {
			pos++
		}
		if pos >= len(expr) {
			break
		}
		op := expr[pos]
		if op != '+' && op != '-' && op != '*' && op != '/' {
			return 0, fmt.Errorf("unexpected operator %c at pos %d", op, pos)
		}
		pos++
		n, err := nextNum()
		if err != nil {
			return 0, err
		}
		switch op {
		case '+':
			result += n
		case '-':
			result -= n
		case '*':
			result *= n
		case '/':
			result /= n
		}
	}
	return result, nil
}