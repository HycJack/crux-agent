// Package l3 distills the scene catalog into a single user-persona Markdown
// file using a 4-layer deep scan prompt (Base & Facts / Interest Graph /
// Interface Protocol / Core Cognition). Mirrors the TS persona-generation.ts.
package l3

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	openai "github.com/openai/openai-go"

	"github.com/crux-memory/crux-memory/llm"
)

// Persona is the distilled user profile.
type Persona struct {
	Filename  string
	Content   string
	UpdatedAt time.Time
}

// Store writes persona.md to baseDir/l3/persona.md.
type Store struct {
	BaseDir string
}

// NewStore creates the persona directory.
func NewStore(baseDir string) (*Store, error) {
	p := filepath.Join(baseDir, "l3")
	if err := os.MkdirAll(p, 0o755); err != nil {
		return nil, fmt.Errorf("l3: create dir: %w", err)
	}
	return &Store{BaseDir: p}, nil
}

// Read loads the current persona.md.
func (s *Store) Read() (string, error) {
	path := filepath.Join(s.BaseDir, "persona.md")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

// Write persists persona.md.
func (s *Store) Write(content string) error {
	return os.WriteFile(filepath.Join(s.BaseDir, "persona.md"), []byte(content), 0o644)
}

// Generator holds the LLM client.
type Generator struct {
	llm   *llm.Client
	store *Store
}

// GenerateInput is the data passed to Generate.
type GenerateInput struct {
	Mode            string   // "first" | "incremental"
	TotalMemories   int      // total L1 record count
	SceneCount      int      // total scene count
	ChangedSceneNames []string // scenes changed since last update
	ChangedScenesContent string // joined content of changed scenes
	ExistingPersona string   // current persona.md content (may be empty)
}

// NewGenerator wires a persona generator.
func NewGenerator(llmClient *llm.Client, store *Store) *Generator {
	return &Generator{llm: llmClient, store: store}
}

// Generate produces updated persona content and writes it to disk.
func (g *Generator) Generate(ctx context.Context, in GenerateInput) (string, error) {
	if g.llm == nil {
		return "", fmt.Errorf("l3: generator requires an LLM client")
	}
	system := systemPrompt
	user, err := buildUserPrompt(in)
	if err != nil {
		return "", err
	}

	var resp struct {
		Persona string `json:"persona"`
	}
	if err := g.llm.ChatJSON(ctx, system, []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage(user),
	}, &resp); err != nil {
		return "", fmt.Errorf("l3: llm: %w", err)
	}
	if resp.Persona == "" {
		return "", fmt.Errorf("l3: empty persona response")
	}
	// Append scene navigation footer for continuity.
	footer := buildSceneFooter(in.ChangedSceneNames, in.SceneCount)
	final := resp.Persona + footer

	if g.store != nil {
		if err := g.store.Write(final); err != nil {
			return "", fmt.Errorf("l3: write: %w", err)
		}
	}
	return final, nil
}

func buildSceneFooter(changed []string, totalScenes int) string {
	if len(changed) == 0 {
		return fmt.Sprintf("\n\n---\n\n_Scenes: %d total, 0 changed since last update_\n", totalScenes)
	}
	return fmt.Sprintf("\n\n---\n\n_Scenes: %d total, %d changed: %s_\n",
		totalScenes, len(changed), strings.Join(changed, ", "))
}

func buildUserPrompt(in GenerateInput) (string, error) {
	mode := "incremental"
	if in.Mode == "first" {
		mode = "first"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Mode: %s\n", mode))
	sb.WriteString(fmt.Sprintf("Total L1 memories: %d\n", in.TotalMemories))
	sb.WriteString(fmt.Sprintf("Scene count: %d\n", in.SceneCount))
	sb.WriteString(fmt.Sprintf("Changed scenes: %d\n\n", len(in.ChangedSceneNames)))

	if in.ChangedScenesContent != "" {
		sb.WriteString("## Changed Scene Content\n\n")
		sb.WriteString(in.ChangedScenesContent)
		sb.WriteString("\n\n")
	}
	if in.ExistingPersona != "" {
		sb.WriteString(fmt.Sprintf("## Current Persona (%d chars)\n\n", len(in.ExistingPersona)))
		sb.WriteString(in.ExistingPersona)
		sb.WriteString("\n\n")
	}
	return sb.String(), nil
}

const systemPrompt = `# Persona Architect — Incremental Evolution Protocol

You synthesize a user persona from atomic memories and scene content. The
persona is the "narrative profile" the agent uses to anticipate user needs.

## Output format (strict JSON)

{"persona": "<full Markdown document, ≤ 2000 chars>"}

## 4-Layer Deep Scan

Scan ALL the provided changed scene content and produce:

### 🟢 Layer 1: Base & Facts
- Concrete facts: name fragments, demographic data, current status
- Useful for: ice-breakers and context-aware replies

### 🔵 Layer 2: Interest Graph
- Things the user spends time/money/attention on
- Distinguish ACTIVE (currently practicing) vs PASSIVE (consuming) vs DORMANT
- Useful for: chit-chat and lifestyle recommendations

### 🟡 Layer 3: Interaction Protocol
- Communication habits, pet peeves, workflow preferences
- Useful for: HOW the agent should speak and deliver

### 🔴 Layer 4: Core Cognition
- Decision logic, contradictions, ultimate drivers
- Useful for: the agent making decisions on behalf of the user

## Output Template (Markdown)

` + "```markdown" + `
# User Narrative Profile

> **Archetype**: [One-sentence core narrative archetype]

> **Basic Information**
- [concise facts]

> **Long-term Preferences**
- [stable preferences]

## 📖 Chapter 1: Context & Current State
[Coherent paragraph, merge facts + state]

## 🎨 Chapter 2: The Texture of Life
[Coherent paragraph, connect interests + habits]

## 🤖 Chapter 3: Interaction & Cognitive Protocol

### 3.1 How to Speak
### 3.2 How to Think

## 🧩 Chapter 4: Deep Insights & Evolution

- **Productive Contradictions**: ...
- **Evolution Trajectory**: dated changes
- **Emergent Traits**: 3-7 tags, each with a short note
` + "```" + `

## Hard constraints

1. Total length ≤ 2000 characters.
2. NO fabrication — only what is supported by the provided scene content.
3. NEVER invent facts that aren't in the data.
4. Skip empty sections entirely (don't write "(none)").
5. Cold-start persona can be minimal — better to be brief than hallucinate.
6. Output language: detect the dominant language in the scene content and use it.
7. Return ONLY the JSON object {"persona": "..."}.`