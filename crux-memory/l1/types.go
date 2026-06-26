// Package l1 extracts atomic memories from L0 conversation using a single
// LLM call with JSON-mode structured output, then deduplicates and writes
// them as JSONL records.
package l1

import (
	"encoding/json"
	"time"
)

// MemoryType classifies an atomic memory. Mirrors the TypeScript taxonomy
// from l1-extractor.ts.
type MemoryType string

const (
	TypeFact       MemoryType = "fact"        // objective fact about the world / user
	TypePreference MemoryType = "preference"  // user's preference / taste
	TypeConstraint MemoryType = "constraint"  // a rule the user imposes
	TypeConclusion MemoryType = "conclusion"  // decision or conclusion reached
	TypePersona    MemoryType = "persona"     // stable personality trait
)

// Record is one atomic memory written to L1 JSONL.
type Record struct {
	ID              string                 `json:"id"`
	Content         string                 `json:"content"`
	Type            MemoryType             `json:"type"`
	Priority        int                    `json:"priority"`         // 0-10, higher = more important
	SceneName       string                 `json:"scene_name"`       // L2 scene this belongs to
	SourceMessageIDs []string              `json:"source_message_ids"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at,omitempty"`
	SupersededBy    string                 `json:"superseded_by,omitempty"` // ID of newer record that replaces this
}

// ExtractInput is the data passed to the LLM extractor.
type ExtractInput struct {
	Messages          []Message
	ExistingRecords   []Record // for dedup / conflict detection
	LastSceneName     string   // for continuity across calls
	PreviousExtracted []string // IDs of records already extracted in prior runs
}

// Message is the minimal message shape the LLM extractor needs. The
// extractor only cares about ID + role + content.
type Message struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

// SceneSegment is the LLM's structured output: each scene with its memories.
type SceneSegment struct {
	SceneName  string           `json:"scene_name"`
	MessageIDs []string         `json:"message_ids"`
	Memories   []ExtractedMemory `json:"memories"`
}

// ExtractedMemory is a single memory as returned by the LLM.
type ExtractedMemory struct {
	Content         string                 `json:"content"`
	Type            string                 `json:"type"`
	Priority        int                    `json:"priority"`
	SourceMessageIDs []string              `json:"source_message_ids"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// ExtractionResult is what the extractor returns.
type ExtractionResult struct {
	Success        bool          `json:"success"`
	ExtractedCount int           `json:"extracted_count"`
	StoredCount    int           `json:"stored_count"`
	Records        []Record      `json:"records"`
	SceneNames     []string      `json:"scene_names"`
	LastSceneName  string        `json:"last_scene_name,omitempty"`
	SceneSegments  []SceneSegment `json:"scene_segments"`
}

// RecordsAsIDs returns just the IDs of the records in this result.
// Used by L2 to know which records are "changed" since last persist.
func (r *ExtractionResult) RecordsAsIDs() []string {
	out := make([]string, len(r.Records))
	for i, rec := range r.Records {
		out[i] = rec.ID
	}
	return out
}

// MarshalRecord is a helper for human-readable single-record JSON output.
func (r Record) MarshalRecord() ([]byte, error) {
	return json.Marshal(r)
}