// Package defaults provides default implementations of all plugin interfaces.
//
// These implementations are kept deliberately simple and self-contained.
// For production use cases (e.g., SQLite-backed sessions), users should
// provide their own implementations tuned to their infrastructure.
package defaults

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/hycjack/crux-kernel/plugin"
	core "github.com/hycjack/crux-ai/core"
)

// ─── JSONL Session (with tree/branch/migration support) ─────────────────────

// JSONLSession is a JSONL-backed session implementation with tree structure,
// branch support, and version migration.
//
// Reference: tau_agent/session (entries.py, tree.py, storage.py, memory.py)
type JSONLSession struct {
	mu      sync.RWMutex
	file    *os.File
	path    string
	entries []plugin.SessionTreeEntry
	id      string
}

// NewJSONLSession opens or creates a JSONL file for session storage.
func NewJSONLSession(path string) (*JSONLSession, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	s := &JSONLSession{file: f, path: path}

	// Read existing entries (with migration support)
	dec := json.NewDecoder(f)
	for dec.More() {
		var raw map[string]json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			log.Printf("warning: skipping malformed JSONL line in %s: %v", path, err)
			continue
		}
		// Apply migration before parsing
		raw = migrateSessionEntry(raw)

		if t, ok := raw["type"]; ok {
			var typeStr string
			if err := json.Unmarshal(t, &typeStr); err != nil {
				continue
			}
			if typeStr == string(plugin.EntrySessionInfo) {
				if sid, ok := raw["sessionId"]; ok {
					json.Unmarshal(sid, &s.id)
				}
			}
		}
		entry := rawToEntry(raw)
		s.entries = append(s.entries, entry)
	}

	// Generate ID if none exists
	if s.id == "" {
		s.id = time.Now().Format("20060102-150405")
	}

	return s, nil
}

// ID returns the session ID.
func (s *JSONLSession) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// Append adds entries to the session and persists them.
func (s *JSONLSession) Append(entries ...plugin.SessionTreeEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	enc := json.NewEncoder(s.file)
	for _, e := range entries {
		raw := entryToRaw(e)
		if err := enc.Encode(raw); err != nil {
			return err
		}
	}
	s.entries = append(s.entries, entries...)
	return s.file.Sync()
}

// Entries returns all session entries.
func (s *JSONLSession) Entries() []plugin.SessionTreeEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]plugin.SessionTreeEntry, len(s.entries))
	copy(result, s.entries)
	return result
}

// PathToEntry returns the root-to-leaf path for the given entry ID.
// Reference: tau_agent/session/tree.py path_to_entry()
func (s *JSONLSession) PathToEntry(leafID string) ([]plugin.SessionTreeEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byID := make(map[string]plugin.SessionTreeEntry, len(s.entries))
	for _, e := range s.entries {
		if _, exists := byID[e.ID]; exists {
			return nil, fmt.Errorf("duplicate session entry id: %s", e.ID)
		}
		byID[e.ID] = e
	}

	path := make([]plugin.SessionTreeEntry, 0)
	seen := make(map[string]bool)
	currentID := leafID

	for currentID != "" {
		if seen[currentID] {
			return nil, fmt.Errorf("cycle detected at session entry: %s", currentID)
		}
		seen[currentID] = true
		entry, ok := byID[currentID]
		if !ok {
			return nil, fmt.Errorf("missing session entry: %s", currentID)
		}
		path = append(path, entry)
		currentID = entry.ParentID
	}

	// Reverse to get root-to-leaf
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, nil
}

// BuildContext rebuilds the conversation context from session entries,
// supporting tree-path traversal and compaction replay.
//
// Reference: tau_agent/session/memory.py SessionState.from_entries()
func (s *JSONLSession) BuildContext() plugin.SessionContext {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.buildContextLocked("")
}

// BuildContextFromLeaf rebuilds context following the root-to-leaf path.
func (s *JSONLSession) BuildContextFromLeaf(leafID string) (plugin.SessionContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path, err := s.PathToEntry(leafID)
	if err != nil {
		return plugin.SessionContext{}, err
	}

	return s.replayEntries(path), nil
}

func (s *JSONLSession) buildContextLocked(leafID string) plugin.SessionContext {
	if leafID != "" {
		path, err := s.PathToEntry(leafID)
		if err == nil {
			return s.replayEntries(path)
		}
	}
	return s.replayEntries(s.entries)
}

// replayEntries replays session entries into a SessionContext.
// Handles compaction entries by replacing replaced entry IDs with summary messages.
func (s *JSONLSession) replayEntries(entries []plugin.SessionTreeEntry) plugin.SessionContext {
	var thinkingLevel string
	var model *plugin.SessionModel
	var systemPrompt string
	type messageRow struct {
		id   string
		msg  core.Message
	}
	var messageRows []messageRow

	for _, entry := range entries {
		switch entry.Type {
		case plugin.EntryMessage:
			if entry.Message != nil {
				messageRows = append(messageRows, messageRow{id: entry.ID, msg: entry.Message})
			}
		case plugin.EntryModelChange:
			if entry.Provider != "" || entry.ModelID != "" {
				model = &plugin.SessionModel{Provider: entry.Provider, ModelID: entry.ModelID}
			}
		case plugin.EntrySystemPrompt:
			if p, ok := entry.Metadata["prompt"].(string); ok {
				systemPrompt = p
			}
		case plugin.EntryBranchSummary:
			summary := fmt.Sprintf(
				"The following is a summary of a branch that this conversation came back from:\n%s",
				entry.Summary,
			)
			messageRows = append(messageRows, messageRow{
				id: entry.ID,
				msg: core.UserMessage{Role: "user", Content: summary},
			})
		case plugin.EntryCompaction:
			// Apply compaction: replace the referenced entries with a summary
			// message carrying the raw compaction summary (aligned with
			// crux-agent-runtime/session).
			replacedIDs := entry.ReplacedEntryIDs
			replacedSet := make(map[string]bool, len(replacedIDs))
			for _, rid := range replacedIDs {
				replacedSet[rid] = true
			}
			retained := make([]messageRow, 0, len(messageRows))
			insertedSummary := false
			for _, row := range messageRows {
				if !replacedSet[row.id] {
					retained = append(retained, row)
					continue
				}
				if !insertedSummary {
					retained = append(retained, messageRow{
						id: entry.ID,
						msg: core.UserMessage{Role: "user", Content: entry.CompactionSummary},
					})
					insertedSummary = true
				}
			}
			if !insertedSummary {
				retained = append(retained, messageRow{
					id: entry.ID,
					msg: core.UserMessage{Role: "user", Content: entry.CompactionSummary},
				})
			}
			messageRows = retained
		case plugin.EntryThinkingChange:
			thinkingLevel = entry.ThinkingLevel
		}
	}

	messages := make([]core.Message, len(messageRows))
	for i, row := range messageRows {
		messages[i] = row.msg
	}

	return plugin.SessionContext{
		Messages:      messages,
		ThinkingLevel: thinkingLevel,
		Model:         model,
		SystemPrompt:  systemPrompt,
	}
}

// Close closes the session file.
func (s *JSONLSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}

// ─── Version migration ───────────────────────────────────────────────────────
//
// migrateSessionEntry handles backward-compatible migration of persisted
// session entries. As the API evolves, old format entries are upgraded
// in-place during deserialization so historical sessions remain readable.
//
// Reference: tau_agent/session/jsonl.py _migrate_session_entry()

func migrateSessionEntry(raw map[string]json.RawMessage) map[string]json.RawMessage {
	var typeStr string
	if t, ok := raw["type"]; ok {
		json.Unmarshal(t, &typeStr)
	}
	if typeStr != "message" {
		return raw
	}

	msgRaw, ok := raw["message"]
	if !ok {
		return raw
	}

	msgMap := make(map[string]json.RawMessage)
	if err := json.Unmarshal(msgRaw, &msgMap); err != nil {
		return raw
	}

	migrated := migrateMessage(msgMap)
	if migrated != nil {
		migratedBytes, err := json.Marshal(migrated)
		if err == nil {
			raw["message"] = migratedBytes
		}
	}

	return raw
}

func migrateMessage(raw map[string]json.RawMessage) map[string]json.RawMessage {
	if raw == nil {
		return nil
	}

	var role string
	if r, ok := raw["role"]; ok {
		json.Unmarshal(r, &role)
	}

	switch role {
	case "tool":
		// Migration: old role="tool" → role="toolResult"
		raw["role"] = mustMarshal("toolResult")
		if name, ok := raw["name"]; ok {
			raw["toolName"] = name
			delete(raw, "name")
		} else if _, ok := raw["toolName"]; !ok {
			raw["toolName"] = mustMarshal("unknown")
		}
		if id, ok := raw["tool_call_id"]; ok {
			raw["toolCallId"] = id
			delete(raw, "tool_call_id")
		}
		// Convert ok field to isError
		if okVal, ok := raw["ok"]; ok {
			var okBool bool
			if err := json.Unmarshal(okVal, &okBool); err == nil {
				raw["isError"] = mustMarshal(!okBool)
			}
			delete(raw, "ok")
		}
		// Merge details + data
		dataBytes, hasData := raw["data"]
		detailsBytes, hasDetails := raw["details"]
		if hasData && hasDetails {
			dataMap := make(map[string]json.RawMessage)
			detailsMap := make(map[string]json.RawMessage)
			json.Unmarshal(dataBytes, &dataMap)
			json.Unmarshal(detailsBytes, &detailsMap)
			merged := make(map[string]json.RawMessage)
			for k, v := range dataMap {
				merged[k] = v
			}
			for k, v := range detailsMap {
				merged[k] = v
			}
			if len(merged) > 0 {
				mergedBytes, _ := json.Marshal(merged)
				raw["details"] = mergedBytes
			}
		} else if hasData && !hasDetails {
			raw["details"] = dataBytes
		}
		delete(raw, "data")
		// Normalize content to block form
		raw = migrateContentToBlocks(raw)

	case "assistant":
		// Migrate assistant message: convert tool_calls content to blocks
		usageRaw, hasUsage := raw["usage"]
		if hasUsage {
			usageMap := make(map[string]json.RawMessage)
			if err := json.Unmarshal(usageRaw, &usageMap); err == nil {
				if _, hasCost := usageMap["cost"]; !hasCost {
					usageMap["cost"] = mustMarshal(map[string]float64{})
					costBytes, _ := json.Marshal(usageMap)
					raw["usage"] = costBytes
				}
			}
		}
		raw = migrateContentToBlocks(raw)

	case "user":
		// Migrate user message with custom_type → custom message
		if _, hasCustomType := raw["customType"]; hasCustomType {
			raw["role"] = mustMarshal("custom")
			raw["display"] = mustMarshal(true)
		}
		raw = migrateContentToBlocks(raw)
	}

	return raw
}

func migrateContentToBlocks(raw map[string]json.RawMessage) map[string]json.RawMessage {
	contentRaw, hasContent := raw["content"]
	if !hasContent {
		return raw
	}

	// Check if content is already blocks (the first byte determines)
	var contentStr string
	if err := json.Unmarshal(contentRaw, &contentStr); err == nil {
		// Content is a plain string, convert to block form
		blocks := make([]map[string]json.RawMessage, 0)
		if contentStr != "" {
			blocks = append(blocks, map[string]json.RawMessage{
				"type": mustMarshal("text"),
				"text": mustMarshal(contentStr),
			})
		}

		// Append tool_calls content if present
		if tcRaw, hasTC := raw["toolCalls"]; hasTC {
			var tcList []json.RawMessage
			if err := json.Unmarshal(tcRaw, &tcList); err == nil {
				for _, tc := range tcList {
					blocks = append(blocks, map[string]json.RawMessage{
						"type": mustMarshal("toolCall"),
						"id":   mustMarshal(""),
						"name": mustMarshal(""),
						"arguments": json.RawMessage("{}"),
					})
					// Merge tc fields
					last := blocks[len(blocks)-1]
					var tcMap map[string]json.RawMessage
					json.Unmarshal(tc, &tcMap)
					for k, v := range tcMap {
						last[k] = v
					}
					blocks[len(blocks)-1] = last
				}
			}
			delete(raw, "toolCalls")
			delete(raw, "tool_calls")
		}

		blocksBytes, _ := json.Marshal(blocks)
		raw["content"] = blocksBytes
	}

	return raw
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(b)
}

// ParentID returns the parent entry ID if set.
// New field on plugin.SessionTreeEntry — use helper for now.
const sessionEntryParentIDField = "parentId"

// ─── Serialization helpers ──────────────────────────────────────────────────

func entryToRaw(entry plugin.SessionTreeEntry) map[string]any {
	m := map[string]any{
		"id":        entry.ID,
		"type":      entry.Type,
		"timestamp": entry.Timestamp,
	}
	if entry.ParentID != "" {
		m["parentId"] = entry.ParentID
	}
	if entry.SessionID != "" {
		m["sessionId"] = entry.SessionID
	}
	switch entry.Type {
	case plugin.EntryMessage:
		if entry.Message != nil {
			m["messageRole"] = msgRole(entry.Message)
			m["message"] = entry.Message
		}
	case plugin.EntryCustomMessage:
		m["customType"] = entry.CustomType
		m["content"] = entry.Content
		m["display"] = entry.Display
		m["details"] = entry.Details
	case plugin.EntryCompaction:
		m["compactionSummary"] = entry.CompactionSummary
		m["tokensBefore"] = entry.TokensBefore
		m["firstKeptEntryId"] = entry.FirstKeptEntryID
		if len(entry.ReplacedEntryIDs) > 0 {
			m["replacedEntryIds"] = entry.ReplacedEntryIDs
		}
	case plugin.EntryModelChange:
		m["provider"] = entry.Provider
		m["modelId"] = entry.ModelID
	case plugin.EntryThinkingChange:
		m["thinkingLevel"] = entry.ThinkingLevel
	case plugin.EntrySessionInfo:
		m["sessionId"] = entry.SessionID
		m["description"] = entry.Description
	case plugin.EntrySystemPrompt:
		if len(entry.Metadata) > 0 {
			m["metadata"] = entry.Metadata
		}
	case plugin.EntryLabel, plugin.EntryBranchSummary:
		m["summary"] = entry.Summary
		m["fromId"] = entry.FromID
	}
	return m
}

func rawToEntry(raw map[string]json.RawMessage) plugin.SessionTreeEntry {
	var e plugin.SessionTreeEntry
	if v, ok := raw["id"]; ok {
		json.Unmarshal(v, &e.ID)
	}
	if v, ok := raw["type"]; ok {
		var typeStr string
		json.Unmarshal(v, &typeStr)
		e.Type = plugin.EntryType(typeStr)
	}
	if v, ok := raw["parentId"]; ok {
		json.Unmarshal(v, &e.ParentID)
	}
	if v, ok := raw["timestamp"]; ok {
		json.Unmarshal(v, &e.Timestamp)
	}
	if v, ok := raw["sessionId"]; ok {
		json.Unmarshal(v, &e.SessionID)
	}
	if v, ok := raw["description"]; ok {
		json.Unmarshal(v, &e.Description)
	}
	if v, ok := raw["summary"]; ok {
		json.Unmarshal(v, &e.Summary)
	}
	if v, ok := raw["fromId"]; ok {
		json.Unmarshal(v, &e.FromID)
	}
	if v, ok := raw["compactionSummary"]; ok {
		json.Unmarshal(v, &e.CompactionSummary)
	}
	if v, ok := raw["tokensBefore"]; ok {
		json.Unmarshal(v, &e.TokensBefore)
	}
	if v, ok := raw["firstKeptEntryId"]; ok {
		json.Unmarshal(v, &e.FirstKeptEntryID)
	}
	if v, ok := raw["replacedEntryIds"]; ok {
		json.Unmarshal(v, &e.ReplacedEntryIDs)
	}
	if v, ok := raw["provider"]; ok {
		json.Unmarshal(v, &e.Provider)
	}
	if v, ok := raw["modelId"]; ok {
		json.Unmarshal(v, &e.ModelID)
	}
	if v, ok := raw["thinkingLevel"]; ok {
		json.Unmarshal(v, &e.ThinkingLevel)
	}
	if v, ok := raw["customType"]; ok {
		json.Unmarshal(v, &e.CustomType)
	}
	if v, ok := raw["content"]; ok {
		json.Unmarshal(v, &e.Content)
	}
	if v, ok := raw["display"]; ok {
		json.Unmarshal(v, &e.Display)
	}
	if v, ok := raw["details"]; ok {
		json.Unmarshal(v, &e.Details)
	}
	if v, ok := raw["metadata"]; ok {
		json.Unmarshal(v, &e.Metadata)
	}
	if msg := rawToMessage(raw); msg != nil {
		e.Message = msg
	}
	return e
}

// rawToMessage reconstructs a typed core.Message from a persisted "message"
// entry, dispatching on the stored messageRole. Returns nil if no usable
// message payload is present.
func rawToMessage(raw map[string]json.RawMessage) core.Message {
	msgRaw, ok := raw["message"]
	if !ok {
		return nil
	}
	var role string
	if r, ok := raw["messageRole"]; ok {
		json.Unmarshal(r, &role)
	}
	switch role {
	case "assistant":
		var m core.AssistantMessage
		if err := json.Unmarshal(msgRaw, &m); err != nil {
			return nil
		}
		return m
	case "toolResult":
		var m core.ToolResultMessage
		if err := json.Unmarshal(msgRaw, &m); err != nil {
			return nil
		}
		return m
	default: // "user" or unknown
		var m core.UserMessage
		if err := json.Unmarshal(msgRaw, &m); err != nil {
			return nil
		}
		m.Content = userContentFromRaw(msgRaw)
		return m
	}
}

// userContentFromRaw decodes a user message's "content" into either a string
// or []ContentBlock, matching the shape used when the message was persisted.
func userContentFromRaw(msgRaw json.RawMessage) any {
	var probe struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(msgRaw, &probe); err != nil {
		return nil
	}
	trimmed := bytes.TrimSpace(probe.Content)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '[' {
		if blocks, err := core.UnmarshalContentBlocks(trimmed); err == nil {
			return blocks
		}
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		return s
	}
	return nil
}

func entryToMessage(entry plugin.SessionTreeEntry) core.Message {
	switch entry.Type {
	case plugin.EntryMessage:
		return entry.Message
	}
	return nil
}

func msgRole(msg core.Message) string {
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

// compile-time interface check
var _ plugin.SessionPlugin = (*JSONLSession)(nil)

// GenerateEntryID returns a unique session entry ID.
func GenerateEntryID(prefix string) string {
	if prefix == "" {
		prefix = "e"
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// NewMessageEntry creates a message entry with the given ID (unified
// "message" entry type; role is carried by the core.Message itself).
func NewMessageEntry(id string, msg core.Message) plugin.SessionTreeEntry {
	if id == "" {
		id = GenerateEntryID("msg")
	}
	return plugin.SessionTreeEntry{
		ID:        id,
		Type:      plugin.EntryMessage,
		Timestamp: time.Now(),
		Message:   msg,
	}
}

// NewSystemPromptEntry creates a system prompt entry whose prompt is stored
// in Metadata["prompt"] (aligned with crux-agent-runtime/session).
func NewSystemPromptEntry(id string, prompt string) plugin.SessionTreeEntry {
	if id == "" {
		id = GenerateEntryID("sys")
	}
	return plugin.SessionTreeEntry{
		ID:        id,
		Type:      plugin.EntrySystemPrompt,
		Timestamp: time.Now(),
		Metadata:  map[string]any{"prompt": prompt},
	}
}

// NewModelChangeEntry records a model switch in the session.
func NewModelChangeEntry(id, provider, modelID string) plugin.SessionTreeEntry {
	if id == "" {
		id = GenerateEntryID("model")
	}
	return plugin.SessionTreeEntry{
		ID:        id,
		Type:      plugin.EntryModelChange,
		Timestamp: time.Now(),
		Provider:  provider,
		ModelID:   modelID,
	}
}

// NewCompactionEntry records a compaction in the session.
func NewCompactionEntry(id string, summary string, replacedIDs []string, tokensBefore int) plugin.SessionTreeEntry {
	if id == "" {
		id = GenerateEntryID("cmp")
	}
	return plugin.SessionTreeEntry{
		ID:                id,
		Type:              plugin.EntryCompaction,
		Timestamp:         time.Now(),
		CompactionSummary: summary,
		TokensBefore:      tokensBefore,
		ReplacedEntryIDs:  replacedIDs,
	}
}
