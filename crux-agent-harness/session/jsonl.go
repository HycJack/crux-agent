package session

import (
	"encoding/json"
	"os"
	"sync"

	core "crux-ai/core"
)

// JSONLStorage persists session entries as newline-delimited JSON.
type JSONLStorage struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// NewJSONLStorage opens or creates a JSONL file for session persistence.
func NewJSONLStorage(path string) (*JSONLStorage, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, &HarnessError{Code: ErrStorage, Message: "failed to open file", Path: path, Err: err}
	}
	return &JSONLStorage{file: f, path: path}, nil
}

func (j *JSONLStorage) Append(entries []SessionTreeEntry) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if _, err := j.file.Seek(0, 2); err != nil {
		return &HarnessError{Code: ErrStorage, Message: "seek failed", Err: err}
	}

	enc := json.NewEncoder(j.file)
	for _, entry := range entries {
		raw := entryToRaw(entry)
		if err := enc.Encode(raw); err != nil {
			return &HarnessError{Code: ErrStorage, Message: "write failed", Err: err}
		}
	}
	return j.file.Sync()
}

func (j *JSONLStorage) ReadAll() ([]SessionTreeEntry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if _, err := j.file.Seek(0, 0); err != nil {
		return nil, &HarnessError{Code: ErrStorage, Message: "seek failed", Err: err}
	}

	var entries []SessionTreeEntry
	dec := json.NewDecoder(j.file)
	for dec.More() {
		var raw map[string]json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			continue
		}
		entry, err := rawToEntry(raw)
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (j *JSONLStorage) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.file.Close()
}

// --- JSON serialization helpers ---

func entryToRaw(entry SessionTreeEntry) map[string]any {
	m := map[string]any{
		"id":        entry.ID,
		"type":      entry.Type,
		"timestamp": entry.Timestamp,
	}
	// Always include sessionId if present
	if entry.SessionID != "" {
		m["sessionId"] = entry.SessionID
	}
	switch entry.Type {
	case EntryMessage:
		if entry.Message != nil {
			m["messageRole"] = getRole(entry.Message)
			m["message"] = entry.Message
		}
	case EntryCustomMessage:
		m["customType"] = entry.CustomType
		m["content"] = entry.Content
		m["display"] = entry.Display
		m["details"] = entry.Details
	case EntryBranchSummary:
		m["summary"] = entry.Summary
		m["fromId"] = entry.FromID
	case EntryCompaction:
		m["compactionSummary"] = entry.CompactionSummary
		m["tokensBefore"] = entry.TokensBefore
		m["firstKeptEntryId"] = entry.FirstKeptEntryID
	case EntryModelChange:
		m["provider"] = entry.Provider
		m["modelId"] = entry.ModelID
	case EntryThinkingChange:
		m["thinkingLevel"] = entry.ThinkingLevel
	case EntrySessionInfo:
		m["sessionId"] = entry.SessionID
		m["description"] = entry.Description
	case EntryLabel:
		m["summary"] = entry.Summary
	}
	return m
}

func getRole(msg core.Message) string {
	switch msg.(type) {
	case core.UserMessage:
		return "user"
	case core.AssistantMessage:
		return "assistant"
	case core.ToolResultMessage:
		return "toolResult"
	default:
		return "unknown"
	}
}

func rawToEntry(raw map[string]json.RawMessage) (SessionTreeEntry, error) {
	var entry SessionTreeEntry
	if v, ok := raw["id"]; ok {
		json.Unmarshal(v, &entry.ID)
	}
	if v, ok := raw["type"]; ok {
		json.Unmarshal(v, &entry.Type)
	}
	if v, ok := raw["timestamp"]; ok {
		json.Unmarshal(v, &entry.Timestamp)
	}
	switch entry.Type {
	case EntryMessage:
		var role string
		if v, ok := raw["messageRole"]; ok {
			json.Unmarshal(v, &role)
		}
		if msgRaw, hasMsg := raw["message"]; hasMsg {
			switch role {
			case "user":
				var m core.UserMessage
				json.Unmarshal(msgRaw, &m)
				entry.Message = m
			case "assistant":
				var m core.AssistantMessage
				if err := json.Unmarshal(msgRaw, &m); err != nil {
					return entry, err
				}
				// Manually parse Content since json.Unmarshal can't handle interface slices
				var rawMsg map[string]json.RawMessage
				if err := json.Unmarshal(msgRaw, &rawMsg); err == nil {
					if contentRaw, ok := rawMsg["content"]; ok {
						var contentBlocks []core.ContentBlock
						if err := unmarshalContentBlocks(contentRaw, &contentBlocks); err == nil {
							m.Content = contentBlocks
						}
					}
				}
				entry.Message = m
			case "toolResult":
				var m core.ToolResultMessage
				json.Unmarshal(msgRaw, &m)
				entry.Message = m
			}
		}
	case EntryCustomMessage:
		if v, ok := raw["customType"]; ok {
			json.Unmarshal(v, &entry.CustomType)
		}
		if v, ok := raw["content"]; ok {
			json.Unmarshal(v, &entry.Content)
		}
		if v, ok := raw["display"]; ok {
			json.Unmarshal(v, &entry.Display)
		}
	case EntryBranchSummary:
		if v, ok := raw["summary"]; ok {
			json.Unmarshal(v, &entry.Summary)
		}
		if v, ok := raw["fromId"]; ok {
			json.Unmarshal(v, &entry.FromID)
		}
	case EntryCompaction:
		if v, ok := raw["compactionSummary"]; ok {
			json.Unmarshal(v, &entry.CompactionSummary)
		}
		if v, ok := raw["tokensBefore"]; ok {
			json.Unmarshal(v, &entry.TokensBefore)
		}
		if v, ok := raw["firstKeptEntryId"]; ok {
			json.Unmarshal(v, &entry.FirstKeptEntryID)
		}
	case EntryModelChange:
		if v, ok := raw["provider"]; ok {
			json.Unmarshal(v, &entry.Provider)
		}
		if v, ok := raw["modelId"]; ok {
			json.Unmarshal(v, &entry.ModelID)
		}
	case EntryThinkingChange:
		if v, ok := raw["thinkingLevel"]; ok {
			json.Unmarshal(v, &entry.ThinkingLevel)
		}
	case EntrySessionInfo:
		if v, ok := raw["sessionId"]; ok {
			json.Unmarshal(v, &entry.SessionID)
		}
		if v, ok := raw["description"]; ok {
			json.Unmarshal(v, &entry.Description)
		}
	}
	return entry, nil
}

// unmarshalContentBlocks handles unmarshaling of []core.ContentBlock since
// standard json.Unmarshal cannot handle interface slices properly.
func unmarshalContentBlocks(data json.RawMessage, blocks *[]core.ContentBlock) error {
	var rawBlocks []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawBlocks); err != nil {
		return err
	}

	for _, rawBlock := range rawBlocks {
		var blockType string
		if v, ok := rawBlock["type"]; ok {
			if err := json.Unmarshal(v, &blockType); err != nil {
				return err
			}
		}

		switch blockType {
		case "text":
			var tb core.TextContent
			if err := json.Unmarshal(dataForBlock(rawBlock), &tb); err != nil {
				return err
			}
			*blocks = append(*blocks, tb)
		case "thinking":
			var tb core.ThinkingContent
			if err := json.Unmarshal(dataForBlock(rawBlock), &tb); err != nil {
				return err
			}
			*blocks = append(*blocks, tb)
		case "toolCall":
			var tc core.ToolCall
			if err := json.Unmarshal(dataForBlock(rawBlock), &tc); err != nil {
				return err
			}
			*blocks = append(*blocks, tc)
		default:
			// Handle unknown content types
			var text string
			if v, ok := rawBlock["text"]; ok {
				if err := json.Unmarshal(v, &text); err != nil {
					return err
				}
			}
			if text != "" {
				*blocks = append(*blocks, core.TextContent{Type: "text", Text: text})
			}
		}
	}
	return nil
}

// dataForBlock converts a map of raw messages back to JSON for unmarshaling.
func dataForBlock(raw map[string]json.RawMessage) json.RawMessage {
	data, _ := json.Marshal(raw)
	return data
}
