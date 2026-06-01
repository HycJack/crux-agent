// Package prompt provides system prompt construction and skill formatting.
package prompt

import (
	"strings"
)

// Config configures the system prompt builder.
type Config struct {
	BasePrompt     string
	Skills         []SkillSection
	CustomSections []string
}

// SkillSection is a skill visible to the model in the system prompt.
type SkillSection struct {
	Name        string
	Description string
	FilePath    string
}

// Build constructs the full system prompt from configuration.
func Build(config Config) string {
	var parts []string

	if config.BasePrompt != "" {
		parts = append(parts, config.BasePrompt)
	}

	if section := FormatSkillsSection(config.Skills); section != "" {
		parts = append(parts, section)
	}

	parts = append(parts, config.CustomSections...)

	return strings.Join(parts, "\n\n")
}

// FormatSkillsSection formats skills into an XML section for the system prompt.
func FormatSkillsSection(skills []SkillSection) string {
	if len(skills) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, "The following skills provide specialized instructions for specific tasks.")
	lines = append(lines, "Read the full skill file when the task matches its description.")
	lines = append(lines, "")
	lines = append(lines, "<available_skills>")

	for _, s := range skills {
		lines = append(lines, "  <skill>")
		lines = append(lines, "    <name>"+escapeXML(s.Name)+"</name>")
		lines = append(lines, "    <description>"+escapeXML(s.Description)+"</description>")
		lines = append(lines, "    <location>"+escapeXML(s.FilePath)+"</location>")
		lines = append(lines, "  </skill>")
	}

	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

// FormatTemplatesSection formats prompt templates into an XML section.
func FormatTemplatesSection(templates []TemplateSection) string {
	if len(templates) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, "The following prompt templates are available:")
	lines = append(lines, "")
	lines = append(lines, "<available_templates>")

	for _, t := range templates {
		lines = append(lines, "  <template>")
		lines = append(lines, "    <name>"+escapeXML(t.Name)+"</name>")
		if t.Description != "" {
			lines = append(lines, "    <description>"+escapeXML(t.Description)+"</description>")
		}
		lines = append(lines, "  </template>")
	}

	lines = append(lines, "</available_templates>")
	return strings.Join(lines, "\n")
}

// TemplateSection is a prompt template visible to the model.
type TemplateSection struct {
	Name        string
	Description string
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}
