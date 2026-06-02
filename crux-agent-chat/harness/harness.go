// Package harness wires crux-agent-harness into crux-agent-chat.
//
// It glues together:
//   - approval gate (BeforeToolCall hook in the agent)
//   - context compaction pipeline
//   - skill loading (embedded into SystemPrompt)
//   - session persistence (JSONL)
//   - token usage tracking (subscribes to assistant messages)
//
// The public surface is a single Harness struct that the agent and
// the REPL both depend on.
package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"crux-agent-harness/approval"
	hcontext "crux-agent-harness/context"
	"crux-agent-harness/observe"
	"crux-agent-harness/session"
	"crux-agent-harness/skills"
	"crux-agent-harness/token"

	"crux-ai/core"
)

// Harness is the chat-side facade for crux-agent-harness.
type Harness struct {
	mu sync.RWMutex

	gate       *approval.Gate
	pipeline   *hcontext.Pipeline
	contextDir string
	skillDiags []skills.Diagnostic

	session  *session.Session
	sessDir  string
	sessFile string
	metadata session.SessionMetadata

	tokens TokenTotals

	logger *observe.Logger
	model  core.Model
}

// TokenTotals accumulates token usage across the whole session.
type TokenTotals struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheWrite  int64 `json:"cacheWrite"`
	Total       int64 `json:"total"`
	Compactions int64 `json:"compactions"`
}

// Snapshot returns a copy of the current token totals.
func (h *Harness) Snapshot() TokenTotals {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.tokens
}

// Config is the input to New.
type Config struct {
	Model         core.Model
	APIKey        string
	WorkDir       string
	SessionPath   string
	SkillDirs     []string
	AutoApprove   []string
	AlwaysDeny    []string
	MaxContextWin int
	Threshold     float64
	MinKeep       int
}

// New constructs a Harness and wires up all sub-systems.
func New(cfg Config) (*Harness, error) {
	if cfg.WorkDir == "" {
		wd, _ := os.Getwd()
		cfg.WorkDir = wd
	}

	// Create session directory
	sessDir := filepath.Join(cfg.WorkDir, ".crux", "sessions")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		return nil, fmt.Errorf("harness: cannot create session dir: %w", err)
	}

	// Generate session ID and create session file
	sessID := generateSessionID()
	sessFile := filepath.Join(sessDir, sessID+".jsonl")

	store, err := session.NewJSONLStorage(sessFile)
	if err != nil {
		return nil, fmt.Errorf("harness: open session: %w", err)
	}
	sess, err := session.NewSession(store)
	if err != nil {
		return nil, fmt.Errorf("harness: load session: %w", err)
	}

	// Write session info entry
	err = sess.Append(session.SessionTreeEntry{
		ID:        session.GenerateID(),
		Type:      session.EntrySessionInfo,
		Timestamp: time.Now(),
		SessionID: sessID,
	})
	if err != nil {
		return nil, fmt.Errorf("harness: write session info: %w", err)
	}

	gate := approval.New()
	for _, name := range cfg.AutoApprove {
		gate.AddRule(approval.Rule{
			Name:    "auto_" + name,
			Match:   approval.MatchByName(name),
			Approve: approval.DecisionAllow,
		})
	}
	for _, name := range cfg.AlwaysDeny {
		gate.AddRule(approval.Rule{
			Name:    "deny_" + name,
			Match:   approval.MatchByName(name),
			Approve: approval.DecisionBlock,
			Reason:  "blocked by harness policy",
		})
	}
	gate.SetAskHandler(func(req approval.Request) approval.Result {
		if !askOnStdin(req) {
			return approval.Result{Decision: approval.DecisionBlock, Reason: "user denied"}
		}
		return approval.Result{Decision: approval.DecisionAllow}
	})

	ctxWin := cfg.MaxContextWin
	if ctxWin <= 0 {
		ctxWin = cfg.Model.ContextWindow
	}
	if ctxWin <= 0 {
		ctxWin = 128000
	}
	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = 0.9
	}
	minKeep := cfg.MinKeep
	if minKeep <= 0 {
		minKeep = 10
	}
	// The compactor reads model only for token counting + completion
	// API selection; the API key is plumbed in via the model.Headers
	// or via StreamOptions.APIKey at the call site.
	pipe, err := hcontext.NewPipeline(hcontext.PipelineConfig{
		Model:               cfg.Model,
		Budget:              hcontext.DefaultBudget(ctxWin),
		CompactionThreshold: threshold,
		MinMessagesToKeep:   minKeep,
		Compactor:           hcontext.NewHybridCompactor(cfg.Model),
	})
	if err != nil {
		return nil, fmt.Errorf("harness: context pipeline: %w", err)
	}

	var diags []skills.Diagnostic
	if len(cfg.SkillDirs) == 0 {
		cfg.SkillDirs = defaultSkillDirs(cfg.WorkDir)
	}
	_, diags = skills.LoadSkills(cfg.SkillDirs...)

	logger := observe.New("chat")
	logger.SetWriter(os.Stderr)

	return &Harness{
		gate:       gate,
		pipeline:   pipe,
		contextDir: cfg.WorkDir,
		skillDiags: diags,
		session:    sess,
		sessDir:    sessDir,
		sessFile:   sessFile,
		metadata: session.SessionMetadata{
			SessionID:    sessID,
			CreatedAt:    time.Now(),
			LastActiveAt: time.Now(),
		},
		logger: logger,
		model:  cfg.Model,
	}, nil
}

// generateSessionID generates a unique session ID based on timestamp.
func generateSessionID() string {
	return time.Now().Format("20060102_150405")
}

func defaultSkillDirs(workDir string) []string {
	dirs := []string{filepath.Join(workDir, ".crux", "skills")}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".crux", "skills"))
	}
	return dirs
}

// --- Approval ---

// EvaluateApproval evaluates a tool call against the gate. This is
// the synchronous "do not prompt" path — AskUser must be called
// separately when the result is DecisionAsk.
func (h *Harness) EvaluateApproval(req approval.Request) approval.Result {
	return h.gate.Evaluate(req)
}

// AskUser prompts the user on stdin. Returns true if approved.
func (h *Harness) AskUser(req approval.Request) bool {
	approved, promote := askOnStdinWithPromote(req)
	if approved && promote {
		// Promote to auto-allow for the rest of the session.
		h.gate.AddRule(approval.Rule{
			Name:    "promoted_" + req.ToolName,
			Match:   approval.MatchByName(req.ToolName),
			Approve: approval.DecisionAllow,
		})
	}
	return approved
}

// askOnStdin prints the tool call and reads a y/n/a response.
// The "a" branch returns promote=true so the caller can install
// an always-allow rule on its own gate.
func askOnStdin(req approval.Request) bool {
	ok, _ := askOnStdinWithPromote(req)
	return ok
}

func askOnStdinWithPromote(req approval.Request) (approved, promote bool) {
	fmt.Printf("\n\033[33m⚠ Approval needed\033[0m\n")
	fmt.Printf("  Tool: \033[1m%s\033[0m\n", req.ToolName)
	if len(req.Args) > 0 {
		var pretty any
		if err := json.Unmarshal(req.Args, &pretty); err == nil {
			if b, err := json.MarshalIndent(pretty, "  ", "  "); err == nil {
				s := string(b)
				if len(s) > 400 {
					s = s[:400] + "\n  ...(truncated)"
				}
				fmt.Printf("  Args: %s\n", s)
			}
		}
	}
	fmt.Printf("  Approve? [y=allow / n=deny / a=always allow this tool]: ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	switch line {
	case "y", "yes":
		return true, false
	case "a", "always":
		return true, true
	default:
		return false, false
	}
}

// --- Skills ---

// SkillSummary is a compact view of a loaded skill.
type SkillSummary struct {
	Name        string
	Description string
	FilePath    string
}

// LoadedSkills returns the names/descriptions/paths of the loaded skills.
func (h *Harness) LoadedSkills() []SkillSummary {
	return h.loadSkillsInternal()
}

// LoadedSkillsCount returns the number of skills the harness discovered.
func (h *Harness) LoadedSkillsCount() int {
	return len(h.loadSkillsInternal())
}

func (h *Harness) loadSkillsInternal() []SkillSummary {
	all, _ := skills.LoadSkills(defaultSkillDirs(h.contextDir)...)
	out := make([]SkillSummary, len(all))
	for i, s := range all {
		out[i] = SkillSummary{Name: s.Name, Description: s.Description, FilePath: s.FilePath}
	}
	return out
}

// AppendSkillsToPrompt appends a <available_skills> block to the base
// system prompt, listing the loaded skills so the model knows they exist.
func (h *Harness) AppendSkillsToPrompt(base string) string {
	loaded, _ := skills.LoadSkills(defaultSkillDirs(h.contextDir)...)
	if len(loaded) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString("The following skills provide specialized instructions for specific tasks. ")
	b.WriteString("Read the full skill file when the task matches its description.\n\n")
	b.WriteString("<available_skills>\n")
	for _, s := range loaded {
		fmt.Fprintf(&b, "  <skill>\n")
		fmt.Fprintf(&b, "    <name>%s</name>\n", xmlEscape(s.Name))
		fmt.Fprintf(&b, "    <description>%s</description>\n", xmlEscape(s.Description))
		fmt.Fprintf(&b, "    <location>%s</location>\n", xmlEscape(s.FilePath))
		fmt.Fprintf(&b, "  </skill>\n")
	}
	b.WriteString("</available_skills>")
	return base + b.String()
}

func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		`'`, "&apos;",
	)
	return r.Replace(s)
}

// --- Context compaction ---

// EstimateUsage returns the current token usage of (system, messages, tools).
func (h *Harness) EstimateUsage(systemPrompt string, messages []core.Message, tools []core.Tool) token.RequestTokenEstimate {
	mc := h.pipeline.MessageCounter()
	return mc.EstimateRequestTokens(systemPrompt, messages, tools)
}

// ShouldCompact returns true if the current context exceeds the threshold.
func (h *Harness) ShouldCompact(systemPrompt string, messages []core.Message, tools []core.Tool) bool {
	return h.pipeline.ShouldCompact(systemPrompt, messages, tools)
}

// Compact runs the compaction pipeline and returns the new messages
// and the result (with token savings).
func (h *Harness) Compact(ctx context.Context, systemPrompt string, messages []core.Message, tools []core.Tool) ([]core.Message, *hcontext.CompactionResult, error) {
	newMsgs, res, err := h.pipeline.Compact(ctx, systemPrompt, messages, tools)
	if err == nil && res != nil {
		atomic.AddInt64(&h.tokens.Compactions, 1)
		h.logger.Info("context compacted", map[string]any{
			"tokensBefore": res.TokensBefore,
			"tokensAfter":  res.TokensAfter,
			"saved":        res.TokensSaved,
		})
	}
	return newMsgs, res, err
}

// --- Session ---

// SessionID returns the current session id.
func (h *Harness) SessionID() string { return h.session.ID() }

// SessionPath returns the JSONL file path used for persistence.
func (h *Harness) SessionPath() string { return h.sessFile }

// RecordMessage appends a user/assistant/tool message to the session.
func (h *Harness) RecordMessage(msg core.Message) error {
	entry := session.SessionTreeEntry{
		ID:        session.GenerateID(),
		Type:      session.EntryMessage,
		Timestamp: time.Now(),
		Message:   msg,
		SessionID: h.metadata.SessionID, // Add session ID to each message
	}

	// Update session metadata
	h.mu.Lock()
	h.metadata.MessageCount++
	h.metadata.LastActiveAt = time.Now()
	h.mu.Unlock()

	// Persist metadata after each message to prevent data loss
	go h.persistMetadata()

	return h.session.Append(entry)
}

// RecordCompaction appends a compaction entry.
func (h *Harness) RecordCompaction(summary string, tokensBefore int) error {
	// Update session metadata
	h.mu.Lock()
	h.metadata.CompactionCount++
	h.metadata.LastActiveAt = time.Now()
	h.mu.Unlock()

	return h.session.Append(session.SessionTreeEntry{
		ID:                session.GenerateID(),
		Type:              session.EntryCompaction,
		Timestamp:         time.Now(),
		CompactionSummary: summary,
		TokensBefore:      tokensBefore,
	})
}

// CompactAndPersist records compaction and persists the compacted messages.
// This ensures that after compaction, the session file reflects the new compacted state.
func (h *Harness) CompactAndPersist(summary string, tokensBefore int, compactedMessages []core.Message) error {
	// Update session metadata
	h.mu.Lock()
	h.metadata.CompactionCount++
	h.metadata.LastActiveAt = time.Now()
	h.mu.Unlock()

	// Append compaction entry
	if err := h.session.Append(session.SessionTreeEntry{
		ID:                session.GenerateID(),
		Type:              session.EntryCompaction,
		Timestamp:         time.Now(),
		CompactionSummary: summary,
		TokensBefore:      tokensBefore,
		SessionID:         h.metadata.SessionID,
	}); err != nil {
		return fmt.Errorf("harness: append compaction entry: %w", err)
	}

	// Append compacted messages (only user and assistant messages, no tool results)
	for _, msg := range compactedMessages {
		// Skip tool result messages as they cause issues when restored
		if _, isToolResult := msg.(core.ToolResultMessage); isToolResult {
			continue
		}
		if err := h.session.Append(session.SessionTreeEntry{
			ID:        session.GenerateID(),
			Type:      session.EntryMessage,
			Timestamp: time.Now(),
			Message:   msg,
			SessionID: h.metadata.SessionID,
		}); err != nil {
			return fmt.Errorf("harness: append compacted message: %w", err)
		}
	}

	return nil
}

// BuildContext rebuilds the LLM context from the session tree.
func (h *Harness) BuildContext() session.SessionContext {
	return h.session.BuildContext()
}

// Close flushes and closes the session.
func (h *Harness) Close() error {
	// Persist session metadata before closing
	if err := h.persistMetadata(); err != nil {
		return err
	}
	return h.session.Close()
}

// Metadata returns the current session metadata.
func (h *Harness) Metadata() session.SessionMetadata {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.metadata
}

// persistMetadata saves session metadata to a JSON file.
func (h *Harness) persistMetadata() error {
	h.mu.RLock()
	metadata := h.metadata
	h.mu.RUnlock()

	metaFile := filepath.Join(h.sessDir, metadata.SessionID+".meta.json")
	f, err := os.Create(metaFile)
	if err != nil {
		return fmt.Errorf("harness: cannot create metadata file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(metadata)
}

// --- Token accounting ---

// AccumulateUsage adds the usage from an AssistantMessage to the totals.
// Called by the REPL subscriber on EventMessageEnd / EventDone.
func (h *Harness) AccumulateUsage(u core.Usage) {
	atomic.AddInt64(&h.tokens.Input, int64(u.Input))
	atomic.AddInt64(&h.tokens.Output, int64(u.Output))
	atomic.AddInt64(&h.tokens.CacheRead, int64(u.CacheRead))
	atomic.AddInt64(&h.tokens.CacheWrite, int64(u.CacheWrite))
	atomic.AddInt64(&h.tokens.Total, int64(u.TotalTokens))

	// Update session metadata
	h.mu.Lock()
	h.metadata.TotalInputTokens += int64(u.Input)
	h.metadata.TotalOutputTokens += int64(u.Output)
	h.metadata.TotalTokens += int64(u.TotalTokens)
	h.metadata.LastActiveAt = time.Now()
	h.mu.Unlock()
}

// EstimatedCost returns the running cost (in USD) of this session.
func (h *Harness) EstimatedCost() float64 {
	totals := h.Snapshot()
	return float64(totals.Input)*h.model.Cost.Input/1_000_000 +
		float64(totals.Output)*h.model.Cost.Output/1_000_000 +
		float64(totals.CacheRead)*h.model.Cost.CacheRead/1_000_000 +
		float64(totals.CacheWrite)*h.model.Cost.CacheWrite/1_000_000
}

// SkillsDiagnostics returns any warnings produced while loading skills.
func (h *Harness) SkillsDiagnostics() []skills.Diagnostic { return h.skillDiags }

// Model returns the model used by this harness.
func (h *Harness) Model() core.Model { return h.model }

// RestoreSession creates a new Harness from an existing session file.
func RestoreSession(cfg Config, sessionID string) (*Harness, error) {
	if cfg.WorkDir == "" {
		wd, _ := os.Getwd()
		cfg.WorkDir = wd
	}

	// Session directory
	sessDir := filepath.Join(cfg.WorkDir, ".crux", "sessions")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		return nil, fmt.Errorf("harness: cannot create session dir: %w", err)
	}

	// Open existing session file
	sessFile := filepath.Join(sessDir, sessionID+".jsonl")
	store, err := session.NewJSONLStorage(sessFile)
	if err != nil {
		return nil, fmt.Errorf("harness: open session: %w", err)
	}
	sess, err := session.NewSession(store)
	if err != nil {
		return nil, fmt.Errorf("harness: load session: %w", err)
	}

	// Load metadata
	var metadata session.SessionMetadata
	metaFile := filepath.Join(sessDir, sessionID+".meta.json")
	if data, err := os.ReadFile(metaFile); err == nil {
		if err := json.Unmarshal(data, &metadata); err != nil {
			fmt.Fprintf(os.Stderr, "harness: warn: failed to load metadata: %v\n", err)
		}
	} else {
		// If metadata file doesn't exist, create default
		metadata = session.SessionMetadata{
			SessionID:    sessionID,
			CreatedAt:    time.Now(),
			LastActiveAt: time.Now(),
		}
	}

	gate := approval.New()
	for _, name := range cfg.AutoApprove {
		gate.AddRule(approval.Rule{
			Name:    "auto_" + name,
			Match:   approval.MatchByName(name),
			Approve: approval.DecisionAllow,
		})
	}
	for _, name := range cfg.AlwaysDeny {
		gate.AddRule(approval.Rule{
			Name:    "deny_" + name,
			Match:   approval.MatchByName(name),
			Approve: approval.DecisionBlock,
			Reason:  "blocked by harness policy",
		})
	}
	gate.SetAskHandler(func(req approval.Request) approval.Result {
		if !askOnStdin(req) {
			return approval.Result{Decision: approval.DecisionBlock, Reason: "user denied"}
		}
		return approval.Result{Decision: approval.DecisionAllow}
	})

	ctxWin := cfg.MaxContextWin
	if ctxWin <= 0 {
		ctxWin = cfg.Model.ContextWindow
	}
	if ctxWin <= 0 {
		ctxWin = 128000
	}
	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = 0.9
	}
	minKeep := cfg.MinKeep
	if minKeep <= 0 {
		minKeep = 10
	}

	pipe, err := hcontext.NewPipeline(hcontext.PipelineConfig{
		Model:               cfg.Model,
		Budget:              hcontext.DefaultBudget(ctxWin),
		CompactionThreshold: threshold,
		MinMessagesToKeep:   minKeep,
		Compactor:           hcontext.NewHybridCompactor(cfg.Model),
	})
	if err != nil {
		return nil, fmt.Errorf("harness: context pipeline: %w", err)
	}

	var diags []skills.Diagnostic
	if len(cfg.SkillDirs) == 0 {
		cfg.SkillDirs = defaultSkillDirs(cfg.WorkDir)
	}
	_, diags = skills.LoadSkills(cfg.SkillDirs...)

	logger := observe.New("chat")
	logger.SetWriter(os.Stderr)

	return &Harness{
		gate:       gate,
		pipeline:   pipe,
		contextDir: cfg.WorkDir,
		skillDiags: diags,
		session:    sess,
		sessDir:    sessDir,
		sessFile:   sessFile,
		metadata:   metadata,
		logger:     logger,
		model:      cfg.Model,
	}, nil
}

// ListSessions returns a list of all available session IDs.
func ListSessions(workDir string) ([]string, error) {
	if workDir == "" {
		wd, _ := os.Getwd()
		workDir = wd
	}
	sessDir := filepath.Join(workDir, ".crux", "sessions")
	files, err := os.ReadDir(sessDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []string
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		if strings.HasSuffix(name, ".jsonl") {
			sessions = append(sessions, name[:len(name)-6])
		}
	}
	return sessions, nil
}
