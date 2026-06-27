package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// shutdownTimeout is the deadline for graceful plugin shutdown.
const shutdownTimeout = 5 * time.Second

// defaultChatSendDelay is applied before pushing a plugin's chat.send into
// the outbound bus — keeps the main response ordered before follow-up
// messages (TTS, translations) the plugin might push.
const defaultChatSendDelay = 50 * time.Millisecond

// chatSendDelay returns the configured chat.send delay, defaulting to
// defaultChatSendDelay. Override via CRUX_PLUGIN_CHAT_SEND_DELAY_MS env.
func chatSendDelay() time.Duration {
	v := os.Getenv("CRUX_PLUGIN_CHAT_SEND_DELAY_MS")
	if v == "" {
		return defaultChatSendDelay
	}
	var ms int
	if _, err := fmt.Sscanf(v, "%d", &ms); err != nil || ms < 0 {
		return defaultChatSendDelay
	}
	return time.Duration(ms) * time.Millisecond
}

// Manifest is the plugin.json descriptor loaded from disk.
type Manifest struct {
	ID           string                       `json:"id"`
	Name         string                       `json:"name"`
	Version      string                       `json:"version"`
	Description  string                       `json:"description,omitempty"`
	Type         string                       `json:"type"` // channel/tool/hook
	Command      string                       `json:"command"`
	Capabilities []string                     `json:"capabilities,omitempty"`
	ConfigSchema map[string]ManifestConfigDef `json:"config,omitempty"`

	// Dir is the absolute path to the plugin's directory. Set by Discover.
	Dir string `json:"-"`
}

// ManifestConfigDef describes one config field in plugin.json.
type ManifestConfigDef struct {
	Type      string `json:"type"` // string/number/bool
	Required  bool   `json:"required,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"` // hide from logs
	Default   string `json:"default,omitempty"`
}

// PluginInstance holds a loaded plugin's manifest + process + runtime config.
type PluginInstance struct {
	Manifest *Manifest
	Process  *Process
	Config   map[string]interface{}
	Enabled  bool
	LastErr  string
}

// Manager discovers, loads, starts, and stops plugins.
//
// Concurrency: Plugin discovery/start is sequential. Once started, Call/Notify
// on individual Processes are concurrent-safe.
type Manager struct {
	plugins map[string]*PluginInstance
	logger  *slog.Logger

	mu sync.RWMutex
}

// NewManager creates a plugin manager.
func NewManager(logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		plugins: make(map[string]*PluginInstance),
		logger:  logger,
	}
}

// Discover scans the given directories for plugin.json files and loads their
// manifests. Missing directories are skipped silently. Invalid plugins are
// logged and skipped.
func (m *Manager) Discover(paths []string) error {
	for _, dir := range paths {
		dir = expandHome(dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			m.logger.Warn("plugin: cannot read directory", "path", dir, "error", err)
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			pluginDir := filepath.Join(dir, entry.Name())
			manifest, err := loadManifest(pluginDir)
			if err != nil {
				m.logger.Warn("plugin: skip directory", "path", pluginDir, "error", err)
				continue
			}

			m.mu.Lock()
			m.plugins[manifest.ID] = &PluginInstance{
				Manifest: manifest,
				Enabled:  true,
				Config:   make(map[string]interface{}),
			}
			m.mu.Unlock()

			m.logger.Info("plugin: discovered",
				"id", manifest.ID,
				"type", manifest.Type,
				"version", manifest.Version,
				"dir", manifest.Dir,
			)
		}
	}
	return nil
}

// ApplyConfig merges per-plugin config from the user's main config.
// Disabled plugins keep their manifest but Process stays nil until StartAll.
func (m *Manager) ApplyConfig(entries map[string]PluginConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, entry := range entries {
		inst, ok := m.plugins[id]
		if !ok {
			m.logger.Warn("plugin: configured but not discovered", "id", id)
			continue
		}
		inst.Enabled = entry.Enabled
		inst.Config = entry.Config
		if inst.Config == nil {
			inst.Config = make(map[string]interface{})
		}
	}
}

// PluginConfig is the user-facing plugin entry from main config (e.g. cruxd.yaml).
type PluginConfig struct {
	Enabled bool                   `yaml:"enabled"`
	Config  map[string]interface{} `yaml:"config,omitempty"`
}

// StartAll starts every enabled plugin (fork subprocess + send initialize).
// Failures are logged; the manager continues with remaining plugins.
func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	instances := make([]*PluginInstance, 0, len(m.plugins))
	for _, inst := range m.plugins {
		instances = append(instances, inst)
	}
	m.mu.RUnlock()

	for _, inst := range instances {
		if !inst.Enabled {
			m.logger.Info("plugin: skipping disabled", "id", inst.Manifest.ID)
			continue
		}

		proc := NewProcess(inst.Manifest, m.logger)
		inst.Process = proc

		proc.SetNotifyHandler(func(n Notification) {
			m.handleNotification(inst.Manifest.ID, n)
		})

		if err := proc.Start(ctx); err != nil {
			m.logger.Error("plugin: failed to start", "id", inst.Manifest.ID, "error", err)
			inst.LastErr = err.Error()
			continue
		}

		cfg := inst.Config
		if cfg == nil {
			cfg = map[string]interface{}{}
		}
		initParams := InitializeParams{Config: cfg}
		if _, err := proc.Call(ctx, MethodInitialize, initParams); err != nil {
			m.logger.Error("plugin: initialize failed", "id", inst.Manifest.ID, "error", err)
			inst.LastErr = err.Error()
			proc.Stop(shutdownTimeout)
			continue
		}

		m.logger.Info("plugin: started", "id", inst.Manifest.ID)
	}
	return nil
}

// StopAll gracefully stops every running plugin.
func (m *Manager) StopAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for id, inst := range m.plugins {
		if inst.Process != nil && inst.Process.IsRunning() {
			m.logger.Info("plugin: stopping", "id", id)
			inst.Process.Stop(shutdownTimeout)
		}
	}
}

// Plugins returns a snapshot of all discovered instances.
func (m *Manager) Plugins() []*PluginInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*PluginInstance, 0, len(m.plugins))
	for _, inst := range m.plugins {
		out = append(out, inst)
	}
	return out
}

// Plugin returns a specific plugin by ID, or nil if not discovered.
func (m *Manager) Plugin(id string) *PluginInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.plugins[id]
}

// Logger returns the manager's logger. Useful for adapter packages that
// need to log under the plugin namespace without re-creating one.
func (m *Manager) Logger() *slog.Logger {
	return m.logger
}

// ToolPlugins returns plugins that declared "tool" in their capabilities.
func (m *Manager) ToolPlugins() []*PluginInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*PluginInstance, 0)
	for _, inst := range m.plugins {
		if inst.Manifest == nil {
			continue
		}
		for _, cap := range inst.Manifest.Capabilities {
			if cap == "tool" {
				out = append(out, inst)
				break
			}
		}
	}
	return out
}

// HookPlugins returns plugins that declared "hook" in their capabilities.
func (m *Manager) HookPlugins() []*PluginInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*PluginInstance, 0)
	for _, inst := range m.plugins {
		if inst.Manifest == nil {
			continue
		}
		for _, cap := range inst.Manifest.Capabilities {
			if cap == "hook" {
				out = append(out, inst)
				break
			}
		}
	}
	return out
}

// handleNotification dispatches plugin→host notifications.
// v1 implementation: just log. Future: route message.inbound/chat.send
// to the chat bus.
func (m *Manager) handleNotification(pluginID string, n Notification) {
	m.logger.Info("plugin notification",
		"plugin", pluginID,
		"method", n.Method,
		"params", truncate(string(n.Params), 200),
	)
}

// --- helpers ---

// loadManifest reads plugin.json from the given directory and returns the
// parsed manifest. Validates required fields.
func loadManifest(pluginDir string) (*Manifest, error) {
	path := filepath.Join(pluginDir, "plugin.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin.json: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse plugin.json: %w", err)
	}

	if m.ID == "" {
		return nil, fmt.Errorf("plugin.json: missing required field 'id'")
	}
	if m.Command == "" {
		return nil, fmt.Errorf("plugin.json: missing required field 'command'")
	}

	absDir, err := filepath.Abs(pluginDir)
	if err != nil {
		return nil, fmt.Errorf("plugin dir abs: %w", err)
	}
	m.Dir = absDir

	return &m, nil
}

// expandHome expands a leading "~" to the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	return path
}