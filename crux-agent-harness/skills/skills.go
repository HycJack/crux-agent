// Package skills loads and manages SKILL.md files from directories.
package skills

import (
	"os"
	"path/filepath"
	"strings"
)

// Skill represents a skill loaded from a SKILL.md file.
type Skill struct {
	Name                   string
	Description            string
	Content                string
	FilePath               string
	DisableModelInvocation bool
}

// Diagnostic is a warning produced while loading skills.
type Diagnostic struct {
	Type    string
	Message string
	Path    string
}

// LoadSkills loads skills from one or more directories.
// It traverses directories recursively, loads SKILL.md files,
// and returns diagnostics for invalid skill files.
func LoadSkills(dirs ...string) ([]Skill, []Diagnostic) {
	var skills []Skill
	var diagnostics []Diagnostic

	for _, dir := range dirs {
		loaded, diags := loadSkillsFromDir(dir, dir)
		skills = append(skills, loaded...)
		diagnostics = append(diagnostics, diags...)
	}

	return skills, diagnostics
}

func loadSkillsFromDir(dir, rootDir string) ([]Skill, []Diagnostic) {
	var skills []Skill
	var diagnostics []Diagnostic

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		diagnostics = append(diagnostics, Diagnostic{
			Type: "warning", Message: err.Error(), Path: dir,
		})
		return nil, diagnostics
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())

		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			sub, subDiags := loadSkillsFromDir(path, rootDir)
			skills = append(skills, sub...)
			diagnostics = append(diagnostics, subDiags...)
			continue
		}

		if strings.EqualFold(entry.Name(), "SKILL.md") {
			skill, err := loadSkillFile(path)
			if err != nil {
				diagnostics = append(diagnostics, Diagnostic{
					Type: "warning", Message: err.Error(), Path: path,
				})
				continue
			}
			skills = append(skills, skill)
		}
	}

	return skills, diagnostics
}

// loadSkillFile parses a SKILL.md file with optional YAML frontmatter.
func loadSkillFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	content := string(data)
	skill := Skill{FilePath: path}

	if strings.HasPrefix(content, "---") {
		endIdx := strings.Index(content[3:], "---")
		if endIdx >= 0 {
			frontmatter := content[3 : endIdx+3]
			content = content[endIdx+6:]

			for _, line := range strings.Split(frontmatter, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				parts := strings.SplitN(line, ":", 2)
				if len(parts) != 2 {
					continue
				}
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				value = strings.Trim(value, `"'`)

				switch key {
				case "name":
					skill.Name = value
				case "description":
					skill.Description = value
				case "disable-model-invocation":
					skill.DisableModelInvocation = value == "true"
				}
			}
		}
	}

	skill.Content = strings.TrimSpace(content)

	if skill.Name == "" {
		skill.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	return skill, nil
}

// FormatInvocation formats a skill invocation prompt.
func FormatInvocation(skill Skill, additionalInstructions string) string {
	s := "<skill name=\"" + skill.Name + "\" location=\"" + skill.FilePath + "\">\n" +
		"References are relative to " + dirName(skill.FilePath) + ".\n\n" +
		skill.Content + "\n</skill>"
	if additionalInstructions != "" {
		return s + "\n\n" + additionalInstructions
	}
	return s
}

func dirName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
