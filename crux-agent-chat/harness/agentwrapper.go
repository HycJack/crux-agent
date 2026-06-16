package harness

import (
	agentruntime "crux-agent-runtime/agent"
	"github.com/hycjack/crux-ai/core"
)

// AgentToolsToCore converts runtime AgentTool definitions into the
// core.Tool values needed by the context pipeline and session helpers.
func AgentToolsToCore(tools []agentruntime.AgentTool) []core.Tool {
	out := make([]core.Tool, len(tools))
	for i, t := range tools {
		out[i] = core.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}
	return out
}
