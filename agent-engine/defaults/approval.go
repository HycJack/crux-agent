package defaults

import (
	"context"
	"strings"
	"sync"

	"github.com/hycjack/crux-kernel/plugin"
)

// ─── ApprovalGate ───────────────────────────────────────────────────────────

// Rule is a predicate that matches tool calls.
type Rule struct {
	Name   string
	Match  func(toolName, toolID string, args []byte) bool
	Result plugin.ApprovalResult
	Reason string
}

// ApprovalGate is a rule-based tool approval gate.
type ApprovalGate struct {
	mu         sync.RWMutex
	rules      []Rule
	onAsk      func(ctx context.Context, toolName, toolID string, args []byte) (plugin.ApprovalResult, string, error)
	defaultDec plugin.ApprovalResult
}

// NewApprovalGate creates a gate with default=allow.
func NewApprovalGate() *ApprovalGate {
	return &ApprovalGate{defaultDec: plugin.ApprovalAllow}
}

// NewStrictApprovalGate creates a gate that blocks everything by default.
func NewStrictApprovalGate() *ApprovalGate {
	return &ApprovalGate{defaultDec: plugin.ApprovalBlock}
}

// SetAskHandler sets the callback for DecisionAsk outcomes.
func (g *ApprovalGate) SetAskHandler(fn func(ctx context.Context, toolName, toolID string, args []byte) (plugin.ApprovalResult, string, error)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.onAsk = fn
}

// AddRule adds a rule. Rules are evaluated in order; first match wins.
func (g *ApprovalGate) AddRule(rule Rule) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rules = append(g.rules, rule)
}

// Evaluate checks a tool call against all rules and returns the decision.
func (g *ApprovalGate) Evaluate(ctx context.Context, toolName string, toolID string, args []byte) (plugin.ApprovalResult, string, error) {
	g.mu.RLock()
	rules := make([]Rule, len(g.rules))
	copy(rules, g.rules)
	onAsk := g.onAsk
	defaultDec := g.defaultDec
	g.mu.RUnlock()

	for _, rule := range rules {
		if rule.Match(toolName, toolID, args) {
			switch rule.Result {
			case plugin.ApprovalAsk:
				if onAsk != nil {
					return onAsk(ctx, toolName, toolID, args)
				}
				return plugin.ApprovalBlock, "ask handler not set", nil
			default:
				return rule.Result, rule.Reason, nil
			}
		}
	}

	return defaultDec, "default", nil
}

// ─── Pre-built rules ────────────────────────────────────────────────────────

// RuleNameContains returns a rule that matches when toolName contains substr.
func RuleNameContains(substr string, result plugin.ApprovalResult, reason string) Rule {
	return Rule{
		Name:   "name_contains:" + substr,
		Match:  func(name, _ string, _ []byte) bool { return jsonContains(name, substr) },
		Result: result,
		Reason: reason,
	}
}

// RuleToolIDContains returns a rule that matches when toolID contains substr.
func RuleToolIDContains(substr string, result plugin.ApprovalResult, reason string) Rule {
	return Rule{
		Name:   "id_contains:" + substr,
		Match:  func(_, id string, _ []byte) bool { return jsonContains(id, substr) },
		Result: result,
		Reason: reason,
	}
}

func jsonContains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// compile-time assertion
var _ plugin.ApprovalPlugin = (*ApprovalGate)(nil)
