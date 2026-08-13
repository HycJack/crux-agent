// Package defaults — compile-time interface satisfaction checks
//
// These checks verify that the default implementations satisfy the plugin
// interfaces. Go's structural typing means they already do if the method
// signatures match — but these explicit assertions serve as documentation
// and prevent silent breakage during refactoring.
package defaults

import (
	"testing"

	"github.com/hycjack/agent-engine/plugin"
)

// Compile-time interface checks (zero-cost, removed by compiler inlining).
var (
	_ plugin.SessionPlugin    = (*JSONLSession)(nil)
	_ plugin.ContextPlugin    = (*ContextPipeline)(nil)
	_ plugin.MemoryPlugin     = (*Memory)(nil)
	_ plugin.AutoLearnPlugin  = (*AutoLearner)(nil)
	// _ plugin.ToolPlugin    = (*???)(nil) // No default ToolPlugin yet
	_ plugin.ApprovalPlugin   = (*ApprovalGate)(nil)
	_ plugin.CheckpointPlugin = (*CheckpointStore)(nil)
	_ plugin.ObservePlugin    = (*Logger)(nil)
)

// TestInterfaceSatisfaction is a runtime verification (trivially passes).
func TestInterfaceSatisfaction(t *testing.T) {
	// If the code compiles, all the _ assignments above hold.
	// This test exists for visibility in test reports.
	t.Log("All plugin interfaces satisfied by defaults")
}
