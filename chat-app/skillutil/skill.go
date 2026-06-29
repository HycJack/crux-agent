// Package skillutil loads skills from SKILL.md files and registers them as
// agent tools. Skills are stored in <workingDir>/skills/<skill-name>/SKILL.md.
//
// Each SKILL.md is scanned and loaded as an agent tool that, when invoked,
// returns the SKILL.md content as a reference so the LLM can follow the SOP.
//
// Two source directories are merged at load time:
//
//  1. The user-defined directory (typically <workingDir>/skills)
//  2. A set of bundled, always-available skills embedded into the binary
//
// Bundled skills have the lowest priority, so a user with a same-named
// skill in their working directory will always win.
package skillutil

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"crux-agent-runtime/agent"

	"github.com/hycjack/crux-ai/core"
)

// bundledFS holds the embedded copy of the always-available example
// skills. Layout matches the on-disk format exactly: each skill is its
// own directory containing SKILL.md. A fresh checkout therefore ships
// with working examples that behave identically to user-authored skills.
//
//go:embed bundled/*/SKILL.md
var bundledFS embed.FS

// bundledDir is the in-memory directory the embedded files live under.
const bundledDir = "bundled"

// Skill represents a loaded skill.
type Skill struct {
	Name        string
	Description string
	Content     string
	Path        string
	// Source is "user" for files loaded from disk (workingDir/skills)
	// and "bundled" for skills compiled into the binary. Useful for
	// the UI to badge the origin and for tests to assert priority.
	Source string
}

// Loader scans and loads skills from a directory.
type Loader struct {
	mu     sync.RWMutex
	skills map[string]*Skill
}

// NewLoader creates a new skill loader.
func NewLoader() *Loader {
	return &Loader{
		skills: make(map[string]*Skill),
	}
}

// LoadAll scans both the embedded bundled skills and the user's
// <baseDir>/skills directory on disk, then merges them into the in-memory
// map. User-defined skills take precedence over bundled ones when they
// share a name — they overwrite (don't merge) so a local fork completely
// replaces the upstream example.
func (l *Loader) LoadAll(baseDir string) error {
	// Wipe the previous set so a removed user file doesn't linger after a
	// reload. We can't selectively diff cheaply without keeping track of
	// which entries came from where, so a full reset is the simplest
	// correct behavior.
	l.mu.Lock()
	l.skills = make(map[string]*Skill)
	l.mu.Unlock()

	// Bundled skills first so user files overwrite them later.
	bundledLoaded, err := l.loadBundled()
	if err != nil {
		return fmt.Errorf("load bundled: %w", err)
	}

	userLoaded, err := l.loadFromDisk(baseDir)
	if err != nil {
		return fmt.Errorf("load from disk: %w", err)
	}

	total := bundledLoaded + userLoaded
	if total > 0 {
		fmt.Printf("[skillutil] loaded %d skill(s) (%d bundled, %d user)\n", total, bundledLoaded, userLoaded)
	}
	return nil
}

// loadFromDisk reads skills from <baseDir>/skills/<name>/SKILL.md.
func (l *Loader) loadFromDisk(baseDir string) (int, error) {
	if baseDir == "" {
		return 0, nil
	}
	skillsDir := filepath.Join(baseDir, "skills")
	info, err := os.Stat(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // No skills directory, that's fine
		}
		return 0, err
	}
	if !info.IsDir() {
		return 0, nil
	}

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return 0, fmt.Errorf("read skills dir: %w", err)
	}

	loaded := 0
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(skillsDir, entry.Name())
		skillPath := filepath.Join(skillDir, "SKILL.md")

		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue // No SKILL.md in this subdirectory
		}

		name := entry.Name()
		desc := extractDescription(string(data), name)

		l.skills[name] = &Skill{
			Name:        name,
			Description: desc,
			Content:     string(data),
			Path:        skillPath,
			Source:      "user",
		}
		loaded++
	}
	return loaded, nil
}

// loadBundled reads skills compiled into the binary. The layout matches
// the on-disk format exactly: <bundled>/<name>/SKILL.md, so the same
// directory-walking logic applies to both sources.
func (l *Loader) loadBundled() (int, error) {
	entries, err := fs.ReadDir(bundledFS, bundledDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	loaded := 0
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		skillPath := name + "/SKILL.md"

		data, err := fs.ReadFile(bundledFS, filepath.Join(bundledDir, skillPath))
		if err != nil {
			continue
		}

		desc := extractDescription(string(data), name)
		l.skills[name] = &Skill{
			Name:        name,
			Description: desc,
			Content:     string(data),
			Path:        "<bundled>/" + skillPath,
			Source:      "bundled",
		}
		loaded++
	}
	return loaded, nil
}

// Reload rescans the skills directory. Useful for hot-reloading.
func (l *Loader) Reload(baseDir string) error {
	l.mu.Lock()
	l.skills = make(map[string]*Skill)
	l.mu.Unlock()
	return l.LoadAll(baseDir)
}

// List returns all loaded skill names.
func (l *Loader) List() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	names := make([]string, 0, len(l.skills))
	for name := range l.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Get returns a skill by name.
func (l *Loader) Get(name string) (*Skill, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.skills[name]
	return s, ok
}

// All returns a snapshot of every loaded skill, sorted by name. The
// snapshot is cheap to copy and safe to iterate without holding the lock.
func (l *Loader) All() []*Skill {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]*Skill, 0, len(l.skills))
	for _, s := range l.skills {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Count returns the number of loaded skills.
func (l *Loader) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.skills)
}

// AsAgentTools converts all loaded skills into agent tools.
// Each skill becomes a tool that returns the SKILL.md content.
func (l *Loader) AsAgentTools() []agent.AgentTool {
	l.mu.RLock()
	defer l.mu.RUnlock()

	tools := make([]agent.AgentTool, 0, len(l.skills))
	for name, skill := range l.skills {
		t := skillToTool(name, skill)
		tools = append(tools, t)
	}

	// Sort for deterministic order
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	return tools
}

// skillToTool converts a Skill into an agent tool.
func skillToTool(name string, skill *Skill) agent.AgentTool {
	return agent.AgentTool{
		Name:        "skill_" + name,
		Description: skill.Description,
		Parameters:  mustSchema(`{"type":"object","properties":{},"additionalProperties":false}`),
		Execute: func(_ context.Context, _ string, _ json.RawMessage, _ func(json.RawMessage)) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{
				Content: []core.ContentBlock{
					core.TextContent{
						Type: "text",
						Text: fmt.Sprintf("Skill: %s\n\n%s\n\nFollow the steps in this skill to complete the task.", name, skill.Content),
					},
				},
			}, nil
		},
	}
}

// mustSchema returns a json.RawMessage for the given literal JSON schema.
func mustSchema(s string) json.RawMessage {
	if !json.Valid([]byte(s)) {
		panic(fmt.Sprintf("skillutil: invalid schema: %s", s))
	}
	return json.RawMessage(s)
}

// extractDescription tries to extract a description from SKILL.md content.
// First checks YAML frontmatter, then falls back to the first heading or line.
func extractDescription(content, fallback string) string {
	// Try YAML frontmatter (---\n...\n---)
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		rest := strings.TrimSpace(content)[3:]
		endIdx := strings.Index(rest, "---")
		if endIdx > 0 {
			frontmatter := rest[:endIdx]
			for _, line := range strings.Split(frontmatter, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "description:") {
					desc := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
					desc = strings.Trim(desc, "\"'")
					if desc != "" {
						return desc
					}
				}
			}
		}
	}

	return fallback
}
