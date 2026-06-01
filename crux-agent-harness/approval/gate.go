// Package approval provides configurable approval gates for tool execution.
package approval

import (
	"encoding/json"
	"sync"
)

// Decision is the outcome of an approval check.
type Decision int

const (
	DecisionAllow  Decision = iota // Proceed with execution
	DecisionBlock                   // Block execution
	DecisionAsk                     // Ask the user (requires external callback)
)

// Request is sent to the approval system before tool execution.
type Request struct {
	ToolName string          `json:"toolName"`
	ToolID   string          `json:"toolId"`
	Args     json.RawMessage `json:"args"`
}

// Result is the approval system's response.
type Result struct {
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason,omitempty"`
}

// Rule is a predicate that matches tool calls.
type Rule struct {
	Name    string
	Match   func(Request) bool // Return true if this rule applies
	Approve Decision           // What to do when matched
	Reason  string
}

// Gate evaluates tool calls against a set of rules.
type Gate struct {
	mu         sync.RWMutex
	rules      []Rule
	onAsk      func(Request) Result // Called when DecisionAsk is reached
	defaultDec Decision
}

// New creates a new approval gate with default=allow.
func New() *Gate {
	return &Gate{defaultDec: DecisionAllow}
}

// NewStrict creates a gate that blocks everything by default.
func NewStrict() *Gate {
	return &Gate{defaultDec: DecisionBlock}
}

// SetAskHandler sets the callback for DecisionAsk outcomes.
func (g *Gate) SetAskHandler(fn func(Request) Result) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.onAsk = fn
}

// AddRule adds a rule. Rules are evaluated in order; first match wins.
func (g *Gate) AddRule(rule Rule) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rules = append(g.rules, rule)
}

// Evaluate checks a tool call against all rules.
func (g *Gate) Evaluate(req Request) Result {
	g.mu.RLock()
	rules := make([]Rule, len(g.rules))
	copy(rules, g.rules)
	onAsk := g.onAsk
	defaultDec := g.defaultDec
	g.mu.RUnlock()

	for _, rule := range rules {
		if rule.Match(req) {
			if rule.Approve == DecisionAsk && onAsk != nil {
				return onAsk(req)
			}
			return Result{Decision: rule.Approve, Reason: rule.Reason}
		}
	}

	return Result{Decision: defaultDec}
}

// --- Built-in rule factories ---

// MatchByName returns a rule matcher for a specific tool name.
func MatchByName(name string) func(Request) bool {
	return func(r Request) bool { return r.ToolName == name }
}

// MatchByPrefix returns a rule matcher for tool names with a given prefix.
func MatchByPrefix(prefix string) func(Request) bool {
	return func(r Request) bool {
		return len(r.ToolName) >= len(prefix) && r.ToolName[:len(prefix)] == prefix
	}
}

// Always returns a matcher that matches everything.
func Always() func(Request) bool {
	return func(r Request) bool { return true }
}

// --- Preset rule sets ---

// DangerousTools returns rules that require approval for potentially dangerous operations.
func DangerousTools() []Rule {
	return []Rule{
		{Name: "bash_execute", Match: MatchByName("bash"), Approve: DecisionAsk, Reason: "Shell execution requires approval"},
		{Name: "file_write", Match: MatchByName("write_file"), Approve: DecisionAsk, Reason: "File write requires approval"},
		{Name: "file_delete", Match: MatchByName("delete_file"), Approve: DecisionBlock, Reason: "File deletion is blocked"},
	}
}
