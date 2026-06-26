package l1

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	openai "github.com/openai/openai-go"

	"github.com/crux-memory/crux-memory/llm"
)

// Extractor uses an LLM with JSON-mode structured output to extract atomic
// memories from a slice of conversation messages.
type Extractor struct {
	llm    *llm.Client
	writer *Writer
}

// NewExtractor wires an extractor. llm may be nil if you only call Write helpers.
func NewExtractor(llmClient *llm.Client, writer *Writer) *Extractor {
	return &Extractor{llm: llmClient, writer: writer}
}

// Extract runs LLM extraction on the given input and writes the resulting
// records via the writer.
func (e *Extractor) Extract(ctx context.Context, in ExtractInput) (*ExtractionResult, error) {
	if e.llm == nil {
		return nil, fmt.Errorf("l1: extractor requires an LLM client")
	}

	systemPrompt := systemPrompt
	userPrompt, err := buildUserPrompt(in)
	if err != nil {
		return nil, err
	}

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage(userPrompt),
	}

	var resp struct {
		Scenes        []SceneSegment `json:"scenes"`
		LastSceneName string         `json:"last_scene_name,omitempty"`
	}
	if err := e.llm.ChatJSON(ctx, systemPrompt, messages, &resp); err != nil {
		return nil, fmt.Errorf("l1: extract: %w", err)
	}

	// Convert ExtractedMemory → Record, generate IDs, deduplicate.
	now := time.Now().UTC()
	records := make([]Record, 0, len(resp.Scenes)*3)
	sceneNames := make([]string, 0, len(resp.Scenes))
	existing := indexExisting(in.ExistingRecords)

	for _, seg := range resp.Scenes {
		sceneNames = append(sceneNames, seg.SceneName)
		for _, em := range seg.Memories {
			// Dedup: skip if there's a near-duplicate existing record.
			if exists, _ := isDuplicate(em, existing); exists {
				continue
			}
			rec := Record{
				ID:               GenerateID(),
				Content:          em.Content,
				Type:             MemoryType(em.Type),
				Priority:         em.Priority,
				SceneName:        seg.SceneName,
				SourceMessageIDs: em.SourceMessageIDs,
				Metadata:         em.Metadata,
				CreatedAt:        now,
			}
			if rec.Type == "" {
				rec.Type = TypeFact
			}
			if rec.Priority == 0 {
				rec.Priority = 5
			}
			records = append(records, rec)
		}
	}

	// Write to L1 store.
	if e.writer != nil {
		if err := e.writer.WriteMany(records); err != nil {
			return nil, fmt.Errorf("l1: write: %w", err)
		}
	}

	return &ExtractionResult{
		Success:        true,
		ExtractedCount: totalMemories(resp.Scenes),
		StoredCount:    len(records),
		Records:        records,
		SceneNames:     sceneNames,
		LastSceneName:  resp.LastSceneName,
		SceneSegments:  resp.Scenes,
	}, nil
}

func totalMemories(scenes []SceneSegment) int {
	n := 0
	for _, s := range scenes {
		n += len(s.Memories)
	}
	return n
}

// ---------- dedup (simplified from l1-dedup.ts) ----------

type existingIndex struct {
	byContent map[string]string // normalized content → record ID
}

func indexExisting(records []Record) *existingIndex {
	idx := &existingIndex{byContent: make(map[string]string, len(records))}
	for _, r := range records {
		idx.byContent[normalizeContent(r.Content)] = r.ID
	}
	return idx
}

// isDuplicate returns true if em.Content is near-identical (≥ 0.85 token-overlap)
// to any existing record. Threshold keeps the rule simple — production systems
// use embeddings + cosine; this is good enough for v1.
func isDuplicate(em ExtractedMemory, idx *existingIndex) (bool, string) {
	cand := normalizeContent(em.Content)
	for existing, id := range idx.byContent {
		if tokenOverlap(cand, existing) >= 0.85 {
			return true, id
		}
	}
	return false, ""
}

func normalizeContent(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimSpace(s)
	// Collapse whitespace runs.
	return strings.Join(strings.Fields(s), " ")
}

func tokenOverlap(a, b string) float64 {
	at := strings.Fields(a)
	bt := strings.Fields(b)
	if len(at) == 0 || len(bt) == 0 {
		return 0
	}
	bset := make(map[string]struct{}, len(bt))
	for _, t := range bt {
		bset[t] = struct{}{}
	}
	intersect := 0
	for _, t := range at {
		if _, ok := bset[t]; ok {
			intersect++
		}
	}
	// Jaccard-ish: intersect / max(|a|, |b|) so we favor "is a subset" matches.
	maxLen := len(at)
	if len(bt) > maxLen {
		maxLen = len(bt)
	}
	return float64(intersect) / float64(maxLen)
}

// ---------- prompt ----------

func buildUserPrompt(in ExtractInput) (string, error) {
	type msg struct {
		ID      string `json:"id"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	msgs := make([]msg, len(in.Messages))
	for i, m := range in.Messages {
		msgs[i] = msg{ID: m.ID, Role: m.Role, Content: m.Content}
	}
	type payload struct {
		Messages        []msg    `json:"messages"`
		Existing        []Record `json:"existing_records,omitempty"`
		LastSceneName   string   `json:"last_scene_name,omitempty"`
		PreviousIDs     []string `json:"previous_extracted_ids,omitempty"`
	}
	p := payload{
		Messages:      msgs,
		LastSceneName: in.LastSceneName,
		PreviousIDs:   in.PreviousExtracted,
	}
	if len(in.ExistingRecords) > 0 {
		p.Existing = in.ExistingRecords
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

const systemPrompt = `# L1 Atomic Memory Extractor

You are an expert at extracting structured atomic memories from conversations.
Each memory should be a small, self-contained, factual unit.

## Output format (strict JSON)

{
  "scenes": [
    {
      "scene_name": "<short scene label, e.g. 'preference-discovery'>",
      "message_ids": ["l0_xxx", "l0_yyy"],
      "memories": [
        {
          "content": "<one clear sentence>",
          "type": "fact|preference|constraint|conclusion|persona",
          "priority": <integer 0-10, higher = more important>,
          "source_message_ids": ["l0_xxx"],
          "metadata": {}
        }
      ]
    }
  ],
  "last_scene_name": "<scene_name of the final scene, for continuity>"
}

## Rules

1. Each memory is ONE fact, ONE preference, or ONE constraint — never compound.
2. Use verbatim or near-verbatim phrasing from the user's words when possible.
3. Skip greetings, filler, and meta-conversation ("thanks", "ok", "let me think").
4. Type taxonomy:
   - fact: objective fact about user/world (e.g. "User lives in Beijing")
   - preference: taste/choice (e.g. "Prefers dark mode")
   - constraint: rule imposed by user (e.g. "Never use bullet points")
   - conclusion: decision reached in this exchange
   - persona: stable personality trait (e.g. "Speaks concisely")
5. Priority: 8-10 = critical/long-lived, 4-7 = useful context, 0-3 = ephemeral.
6. Skip memories that already exist in existing_records (avoid duplicates).
7. If no new memory is present, return {"scenes": [], "last_scene_name": "<last>"}.

## Scene grouping

Group consecutive messages that share a topic into one scene. A scene ends
when the topic shifts. If the whole exchange is one topic, use a single scene.

Return ONLY valid JSON. No prose. No markdown fences.`