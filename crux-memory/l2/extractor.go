package l2

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	openai "github.com/openai/openai-go"

	"github.com/crux-memory/crux-memory/l1"
	"github.com/crux-memory/crux-memory/llm"
)

// Extractor takes a batch of L1 records and assigns each one to a scene,
// creating new scenes as needed. The actual LLM summarization runs in
// Persist() — here we just group + name scenes.
type Extractor struct {
	store *Store
	llm   *llm.Client
}

// PersistInput is the data passed to Persist.
type PersistInput struct {
	Records []l1.Record // all L1 records to be re-organized
	Changed []string    // IDs of records that changed since last persist
}

// PersistResult is the summary.
type PersistResult struct {
	ScenesTouched []string
	ScenesCreated []string
	RecordsLinked int
}

// NewExtractor wires an extractor.
func NewExtractor(store *Store, llmClient *llm.Client) *Extractor {
	return &Extractor{store: store, llm: llmClient}
}

// Persist groups L1 records by scene name, creates new scene files for
// unseen scenes, and updates existing scene files with new records.
func (e *Extractor) Persist(ctx context.Context, in PersistInput) (*PersistResult, error) {
	// Group records by scene name.
	byScene := make(map[string][]l1.Record)
	for _, r := range in.Records {
		if r.SceneName == "" {
			r.SceneName = "uncategorized"
		}
		byScene[r.SceneName] = append(byScene[r.SceneName], r)
	}

	// Existing scenes.
	existing, err := e.store.List()
	if err != nil {
		return nil, fmt.Errorf("l2: list: %w", err)
	}
	existingSet := make(map[string]bool, len(existing))
	for _, n := range existing {
		existingSet[n] = true
	}

	res := &PersistResult{}
	// Sort scene names for stable output.
	sceneNames := make([]string, 0, len(byScene))
	for n := range byScene {
		sceneNames = append(sceneNames, n)
	}
	sort.Strings(sceneNames)

	for _, name := range sceneNames {
		records := byScene[name]
		wasExisting := existingSet[name]
		if !wasExisting {
			res.ScenesCreated = append(res.ScenesCreated, name)
		} else {
			res.ScenesTouched = append(res.ScenesTouched, name)
		}

		// Build or update scene file.
		scene := &Scene{
			Filename: name + ".md",
		}
		if wasExisting {
			existingScene, err := e.store.Read(name)
			if err != nil {
				return nil, fmt.Errorf("l2: read existing %s: %w", name, err)
			}
			scene.Meta = existingScene.Meta
		} else {
			scene.Meta.Created = time.Now().UTC().Format(time.RFC3339)
			scene.Meta.Heat = 0
		}
		scene.Meta.Updated = time.Now().UTC().Format(time.RFC3339)

		// If LLM is wired, generate a one-line summary.
		if e.llm != nil {
			summary, err := e.summarizeScene(ctx, name, records)
			if err != nil {
				// Non-fatal: keep existing summary on failure.
				if scene.Meta.Summary == "" {
					scene.Meta.Summary = name
				}
			} else {
				scene.Meta.Summary = summary
			}
		} else if scene.Meta.Summary == "" {
			scene.Meta.Summary = name
		}

		// Render scene body.
		scene.Content = renderSceneBody(name, records, wasExisting)

		if err := e.store.Write(scene); err != nil {
			return nil, fmt.Errorf("l2: write %s: %w", name, err)
		}
		res.RecordsLinked += len(records)
	}

	return res, nil
}

func (e *Extractor) summarizeScene(ctx context.Context, name string, records []l1.Record) (string, error) {
	if e.llm == nil {
		return name, nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Scene: %s\n\nMemories:\n", name))
	for _, r := range records {
		sb.WriteString(fmt.Sprintf("- [%s p%d] %s\n", r.Type, r.Priority, r.Content))
	}

	var resp struct {
		Summary string `json:"summary"`
	}
	if err := e.llm.ChatJSON(ctx, summarizeSystemPrompt, []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage(sb.String()),
	}, &resp); err != nil {
		return "", err
	}
	return resp.Summary, nil
}

func renderSceneBody(name string, records []l1.Record, wasExisting bool) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", humanize(name)))

	// Group records by type for readability.
	byType := make(map[l1.MemoryType][]l1.Record)
	for _, r := range records {
		byType[r.Type] = append(byType[r.Type], r)
	}

	typeOrder := []l1.MemoryType{l1.TypeConclusion, l1.TypeConstraint, l1.TypePreference, l1.TypeFact, l1.TypePersona}
	for _, t := range typeOrder {
		recs, ok := byType[t]
		if !ok {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n## %s (%d)\n\n", humanType(t), len(recs)))
		// Sort by priority desc.
		sort.Slice(recs, func(i, j int) bool { return recs[i].Priority > recs[j].Priority })
		for _, r := range recs {
			sb.WriteString(fmt.Sprintf("- **p%d** %s _(%s)_\n", r.Priority, r.Content, r.CreatedAt.Format("2006-01-02")))
		}
	}
	return sb.String()
}

func humanize(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_'
	})
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func humanType(t l1.MemoryType) string {
	switch t {
	case l1.TypeFact:
		return "Facts"
	case l1.TypePreference:
		return "Preferences"
	case l1.TypeConstraint:
		return "Constraints"
	case l1.TypeConclusion:
		return "Conclusions"
	case l1.TypePersona:
		return "Persona"
	default:
		return string(t)
	}
}

const summarizeSystemPrompt = `# Scene Summarizer

You summarize a set of atomic memories that share a scene/topic into a single
descriptive sentence (≤ 80 chars, English). Focus on the dominant theme.

Output strictly: {"summary": "..."}`