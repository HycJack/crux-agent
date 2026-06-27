package plugin

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolAdapter describes a tool that lives in a plugin subprocess.
//
// The Execute closure round-trips a JSON-RPC tool.execute call to the
// plugin and returns the result string. Concurrent-safe (Process.Call is).
type ToolAdapter struct {
	Name        string
	Description string
	Parameters  interface{}
	PluginID    string
	Execute     func(ctx context.Context, args json.RawMessage) (string, error)
}

// RegisterPluginTools asks every running tool plugin for its tool list
// and returns the adapter closures, keyed by qualified name
// (`<pluginID>.<toolName>`).
//
// The caller decides where to register these (skill registry, dispatch,
// etc.); this package does not impose a registration target to avoid
// import cycles with specific host frameworks.
func (m *Manager) RegisterPluginTools(ctx context.Context) ([]ToolAdapter, error) {
	var out []ToolAdapter
	for _, inst := range m.ToolPlugins() {
		if inst.Process == nil || !inst.Process.IsRunning() {
			continue
		}
		raw, err := inst.Process.Call(ctx, MethodToolList, nil)
		if err != nil {
			m.logger.Warn("plugin: tool.list failed", "id", inst.Manifest.ID, "error", err)
			continue
		}
		var list ToolListResult
		if err := json.Unmarshal(raw, &list); err != nil {
			m.logger.Warn("plugin: tool.list parse failed", "id", inst.Manifest.ID, "error", err)
			continue
		}

		for _, td := range list.Tools {
			pluginID := inst.Manifest.ID
			toolName := td.Name
			qualified := pluginID + "." + toolName

			capturedProc := inst.Process
			capturedPluginID := pluginID
			capturedToolName := toolName

			exec := func(ctx context.Context, args json.RawMessage) (string, error) {
				var argsMap map[string]interface{}
				if len(args) > 0 {
					if err := json.Unmarshal(args, &argsMap); err != nil {
						return "", fmt.Errorf("parse tool args: %w", err)
					}
				}
				if argsMap == nil {
					argsMap = make(map[string]interface{})
				}
				execParams := ToolExecuteParams{Name: capturedToolName, Args: argsMap}
				callCtx, cancel := context.WithTimeout(ctx, HookCallTimeout)
				defer cancel()

				raw, err := capturedProc.Call(callCtx, MethodToolExecute, execParams)
				if err != nil {
					return "", err
				}
				var r ToolExecuteResult
				if err := json.Unmarshal(raw, &r); err != nil {
					return "", fmt.Errorf("parse tool.execute result: %w", err)
				}
				return r.Result, nil
			}

			out = append(out, ToolAdapter{
				Name:        qualified,
				Description: td.Description,
				Parameters:  td.Parameters,
				PluginID:    capturedPluginID,
				Execute:     exec,
			})
		}
	}
	return out, nil
}