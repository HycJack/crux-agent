// Package skillutil loads skills from SKILL.md files and registers them as
// agent tools. Skills are stored in <workingDir>/skills/<skill-name>/SKILL.md.
//
// Each SKILL.md is scanned and loaded as an agent tool that, when invoked,
// returns the SKILL.md content as a reference so the LLM can follow the SOP.
package skillutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"crux-agent-runtime/agent"
	"github.com/hycjack/crux-ai/core"
)

// Skill represents a loaded skill.
type Skill struct {
	Name        string
	Description string
	Content     string
	Path        string
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

// LoadAll scans the skills directory and loads all SKILL.md files.
// The skills directory is expected to be at <baseDir>/skills.
// Each subdirectory under skills/ should contain a SKILL.md file.
func (l *Loader) LoadAll(baseDir string) error {
	if baseDir == "" {
		return nil
	}

	skillsDir := filepath.Join(baseDir, "skills")
	info, err := os.Stat(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No skills directory, that's fine
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return fmt.Errorf("read skills dir: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	loaded := 0
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
		}
		loaded++
	}

	if loaded > 0 {
		fmt.Printf("[skillutil] loaded %d skill(s) from %s\n", loaded, skillsDir)
	}
	return nil
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
