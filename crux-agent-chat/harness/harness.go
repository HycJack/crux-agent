// Package harness provides a unified interface for managing agent sessions.
// It bundles several cross-cutting concerns:
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
	"time"

	"crux-agent-harness/approval"
	hcontext "crux-agent-harness/context"
	"crux-agent-harness/session"
	"crux-agent-harness/skills"
	"github.com/hycjack/crux-ai/core"
)

// Harness provides session management, approval gating, and context management.
type Harness struct {
	mu         sync.Mutex
	gate       *approval.Gate
	pipeline   *hcontext.Pipeline
	contextDir string
	skillDiags []skills.Skill

	session  *session.Session
	sessDir  string
	sessFile string
	metadata session.SessionMetadata
	sessMgr  *session.SessionManager
}

// TokenTotals tracks token consumption.
type TokenTotals struct {
	Input       int64
	Output      int64
	CacheRead   int64
	CacheWrite  int64
	Total       int64
	Compactions int
}

// SkillSummary describes a loaded skill.
type SkillSummary struct {
	Name        string
	Description string
	FilePath    string
}

// Tokens returns a snapshot of current token totals.
func (h *Harness) Tokens() TokenTotals {
	h.mu.Lock()
	defer h.mu.Unlock()
	return TokenTotals{
		Input:       h.metadata.TotalInputTokens,
		Output:      h.metadata.TotalOutputTokens,
		Total:       h.metadata.TotalTokens,
		CacheRead:   0,
		CacheWrite:  0,
		Compactions: h.metadata.CompactionCount,
	}
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

	sessDir := filepath.Join(cfg.WorkDir, ".crux", "sessions")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		return nil, fmt.Errorf("harness: cannot create session dir: %w", err)
	}

	sessMgr, err := session.NewSessionManager(sessDir)
	if err != nil {
		return nil, fmt.Errorf("harness: create session manager: %w", err)
	}

	var sess *session.Session
	var sessID string
	var sessFile string

	if cfg.SessionPath != "" {
		if !filepath.IsAbs(cfg.SessionPath) {
			cfg.SessionPath = filepath.Join(cfg.WorkDir, cfg.SessionPath)
		}
		sessID = filepath.Base(cfg.SessionPath)
		if strings.HasSuffix(sessID, ".jsonl") {
			sessID = sessID[:len(sessID)-6]
		}
		sessFile = cfg.SessionPath
		store, err := session.NewJSONLStorage(sessFile)
		if err != nil {
			return nil, fmt.Errorf("harness: create storage: %w", err)
		}
		sess, err = session.NewSession(store)
		if err != nil {
			return nil, fmt.Errorf("harness: create session: %w", err)
		}
	} else {
		sess, err = sessMgr.NewSession("")
		if err != nil {
			return nil, fmt.Errorf("harness: create session: %w", err)
		}
		sessID = sess.ID()
		sessFile = sessMgr.SessionPath(sessID)
	}

	metadata := session.SessionMetadata{
		SessionID:         sessID,
		CreatedAt:         time.Now(),
		LastActiveAt:      time.Now(),
		TotalInputTokens:  0,
		TotalOutputTokens: 0,
		TotalTokens:       0,
		MessageCount:      0,
		CompactionCount:   0,
	}

	gate := approval.NewStrict()
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
	gate.AddRule(approval.Rule{
		Name:    "ask_default",
		Match:   func(req approval.Request) bool { return true },
		Approve: approval.DecisionAsk,
		Reason:  "unlisted tool - requires user approval",
	})
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

	pipe, err := hcontext.NewPipeline(hcontext.PipelineConfig{
		Model:               cfg.Model,
		Budget:              hcontext.DefaultBudget(ctxWin),
		CompactionThreshold: cfg.Threshold,
		MinMessagesToKeep:   cfg.MinKeep,
	})
	if err != nil {
		return nil, fmt.Errorf("harness: create context pipeline: %w", err)
	}

	var skillDiags []skills.Skill
	if len(cfg.SkillDirs) > 0 {
		skillDiags, _ = skills.LoadSkills(cfg.SkillDirs[0])
	}

	return &Harness{
		gate:       gate,
		pipeline:   pipe,
		contextDir: cfg.WorkDir,
		skillDiags: skillDiags,
		session:    sess,
		sessDir:    sessDir,
		sessFile:   sessFile,
		metadata:   metadata,
		sessMgr:    sessMgr,
	}, nil
}

// SessionID returns the current session identifier.
func (h *Harness) SessionID() string {
	return h.session.ID()
}

// SessionPath returns the current session file path.
func (h *Harness) SessionPath() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessFile
}

// LoadedSkillsCount returns the number of loaded skills.
func (h *Harness) LoadedSkillsCount() int {
	return len(h.skillDiags)
}

// LoadedSkills returns the list of loaded skills.
func (h *Harness) LoadedSkills() []SkillSummary {
	result := make([]SkillSummary, len(h.skillDiags))
	for i, skill := range h.skillDiags {
		result[i] = SkillSummary{
			Name:        skill.Name,
			Description: skill.Description,
			FilePath:    skill.FilePath,
		}
	}
	return result
}

// ListSessions returns a list of available session IDs.
func (h *Harness) ListSessions() ([]string, error) {
	if h.sessMgr == nil {
		return nil, fmt.Errorf("session manager not initialized")
	}
	metas, err := h.sessMgr.ListSessions()
	if err != nil {
		return nil, err
	}
	result := make([]string, len(metas))
	for i, meta := range metas {
		result[i] = meta.ID
	}
	return result, nil
}

// Snapshot returns a snapshot of current token totals.
func (h *Harness) Snapshot() TokenTotals {
	return h.Tokens()
}

// EstimatedCost returns an estimated cost in USD.
func (h *Harness) EstimatedCost() float64 {
	t := h.Tokens()
	// Rough estimate: $0.003 per 1K input tokens, $0.015 per 1K output tokens
	return float64(t.Input)*0.000003 + float64(t.Output)*0.000015
}

// Close releases resources.
func (h *Harness) Close() error {
	return h.session.Close()
}

// BuildContext returns the current conversation context.
func (h *Harness) BuildContext() []core.Message {
	ctx := h.session.BuildContext()
	return ctx.Messages
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
		h.gate.AddRule(approval.Rule{
			Name:    "promoted_" + req.ToolName,
			Match:   approval.MatchByName(req.ToolName),
			Approve: approval.DecisionAllow,
		})
	}
	return approved
}

// askOnStdin prints the tool call and reads a y/n/a response.
func askOnStdin(req approval.Request) bool {
	ok, _ := askOnStdinWithPromote(req)
	return ok
}

func askOnStdinWithPromote(req approval.Request) (approved, promote bool) {
	// ANSI color codes for nice UI display
	const (
		reset  = "\033[0m"
		bold   = "\033[1m"
		dim    = "\033[2m"
		cyan   = "\033[36m"
		yellow = "\033[33m"
	)

	fmt.Println()
	fmt.Printf("%s╔════════════════════════════════════════════════════════════╗%s\n", yellow, reset)
	fmt.Printf("%s║  ⚠  Approval needed                                        ║%s\n", yellow, reset)
	fmt.Printf("%s╚════════════════════════════════════════════════════════════╝%s\n", yellow, reset)
	fmt.Printf("\n%s  Tool: %s%s%s\n", bold, cyan, req.ToolName, reset)
	if len(req.Args) > 0 {
		var pretty any
		if err := json.Unmarshal(req.Args, &pretty); err == nil {
			if b, err := json.MarshalIndent(pretty, "  ", "  "); err == nil {
				s := string(b)
				if len(s) > 600 {
					s = s[:600] + "\n  ...(truncated)"
				}
				fmt.Printf("  Args:\n%s%s%s\n", dim, s, reset)
			}
		}
	}
	fmt.Printf("\n%s  Approve? [y=allow / n=deny / a=always allow this tool]:%s ", yellow, reset)

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	choice := strings.ToLower(strings.TrimSpace(line))
	fmt.Println()
	switch choice {
	case "y", "yes":
		return true, false
	case "a", "always":
		return true, true
	default:
		return false, false
	}
}

// --- Skills ---

// AppendSkillsToPrompt adds skill instructions to the system prompt.
func (h *Harness) AppendSkillsToPrompt(base string) string {
	if len(h.skillDiags) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n## Available Skills\n\n")
	for _, skill := range h.skillDiags {
		b.WriteString("- ")
		b.WriteString(skill.Name)
		if skill.Description != "" {
			b.WriteString(": ")
			b.WriteString(skill.Description)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// --- Session Persistence ---

// RecordMessage persists a message to the session file.
func (h *Harness) RecordMessage(msg core.Message) error {
	entry := session.SessionTreeEntry{
		ID:        session.GenerateID(),
		Type:      session.EntryMessage,
		Timestamp: time.Now(),
		Message:   msg,
	}
	return h.session.Append(entry)
}

// RecordMessages persists multiple messages to the session file.
func (h *Harness) RecordMessages(msgs []core.Message) error {
	entries := make([]session.SessionTreeEntry, len(msgs))
	for i, msg := range msgs {
		entries[i] = session.SessionTreeEntry{
			ID:        session.GenerateID(),
			Type:      session.EntryMessage,
			Timestamp: time.Now(),
			Message:   msg,
		}
	}
	return h.session.Append(entries...)
}

// LoadMessages loads all messages from the session file.
func (h *Harness) LoadMessages() ([]core.Message, error) {
	ctx := h.session.BuildContext()
	return ctx.Messages, nil
}

// --- Metadata ---

// Metadata returns the current session metadata.
func (h *Harness) Metadata() session.SessionMetadata {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.metadata
}

// persistMetadata writes metadata to disk.
func (h *Harness) persistMetadata() error {
	h.mu.Lock()
	meta := h.metadata
	h.mu.Unlock()

	if h.sessMgr != nil {
		return h.sessMgr.UpdateMeta(h.session.ID(), func(m *session.SessionMeta) {
			m.LastActiveAt = meta.LastActiveAt
			m.MessageCount = meta.MessageCount
			m.TokenCount = meta.TotalTokens
		})
	}
	return nil
}

// --- Token Tracking ---

// AccumulateUsage adds to the token totals.
func (h *Harness) AccumulateUsage(u core.Usage) {
	h.mu.Lock()
	h.metadata.TotalInputTokens += int64(u.Input)
	h.metadata.TotalOutputTokens += int64(u.Output)
	h.metadata.TotalTokens += int64(u.TotalTokens)
	h.metadata.LastActiveAt = time.Now()
	h.metadata.MessageCount++
	h.mu.Unlock()

	go h.persistMetadata()
}

// --- Context Management ---

// EstimateUsage estimates token usage for the given context.
func (h *Harness) EstimateUsage(systemPrompt string, messages []core.Message, tools []core.Tool) hcontext.Status {
	return h.pipeline.Check(systemPrompt, messages, tools)
}

// ShouldCompact returns true if compaction is needed based on the threshold.
func (h *Harness) ShouldCompact(systemPrompt string, messages []core.Message, tools []core.Tool) bool {
	return h.pipeline.ShouldCompact(systemPrompt, messages, tools)
}

// Compact performs compaction if needed. Returns the compacted messages and result.
func (h *Harness) Compact(ctx context.Context, systemPrompt string, messages []core.Message, tools []core.Tool) ([]core.Message, *hcontext.CompactionResult, error) {
	return h.pipeline.Compact(ctx, systemPrompt, messages, tools)
}

// RecordCompaction records a compaction event in the session.
func (h *Harness) RecordCompaction(summary string, tokensBefore int) error {
	entry := session.SessionTreeEntry{
		ID:                session.GenerateID(),
		Type:              session.EntryCompaction,
		Timestamp:         time.Now(),
		CompactionSummary: summary,
		TokensBefore:      tokensBefore,
		FirstKeptEntryID:  "",
	}
	return h.session.Append(entry)
}

// CompactContext attempts to compact the conversation context.
func (h *Harness) CompactContext(systemPrompt string, messages []core.Message, tools []core.Tool) ([]core.Message, error) {
	ctx := context.Background()
	result, _, err := h.pipeline.Compact(ctx, systemPrompt, messages, tools)
	return result, err
}

// NeedsCompaction checks if the context needs compaction.
func (h *Harness) NeedsCompaction(systemPrompt string, messages []core.Message, tools []core.Tool) bool {
	return h.pipeline.ShouldCompact(systemPrompt, messages, tools)
}

// TokenCount returns the token count for the given context.
func (h *Harness) TokenCount(systemPrompt string, messages []core.Message, tools []core.Tool) int {
	status := h.pipeline.Check(systemPrompt, messages, tools)
	return status.Used
}

// --- ReopenSession reopens an existing session ---

func (h *Harness) ReopenSession(sessionPath string) error {
	if !filepath.IsAbs(sessionPath) {
		sessionPath = filepath.Join(h.contextDir, sessionPath)
	}

	sessID := filepath.Base(sessionPath)
	if strings.HasSuffix(sessID, ".jsonl") {
		sessID = sessID[:len(sessID)-6]
	}

	store, err := session.NewJSONLStorage(sessionPath)
	if err != nil {
		return fmt.Errorf("harness: reopen session: %w", err)
	}

	newSess, err := session.NewSession(store)
	if err != nil {
		return fmt.Errorf("harness: reopen session: %w", err)
	}

	h.mu.Lock()
	h.session = newSess
	h.sessFile = sessionPath
	h.sessDir = filepath.Dir(sessionPath)
	h.metadata.SessionID = sessID
	h.metadata.LastActiveAt = time.Now()
	h.mu.Unlock()

	return h.persistMetadata()
}

// --- CreateNewSession creates a new session ---

func CreateNewSession(cfg Config) (*Harness, error) {
	return New(cfg)
}

// --- RestoredSession creates a harness from an existing session file ---

func RestoredSession(cfg Config, sessionPath string) (*Harness, error) {
	cfg.SessionPath = sessionPath
	return New(cfg)
}

// RestoreSession restores a session by ID and returns a new harness.
func RestoreSession(cfg Config, sessionID string) (*Harness, error) {
	cfg.SessionPath = sessionID
	return New(cfg)
}

// CompactAndPersist performs compaction and persists the result.
func (h *Harness) CompactAndPersist(summary string, tokensBefore int, newMsgs []core.Message) error {
	if err := h.RecordCompaction(summary, tokensBefore); err != nil {
		return err
	}
	return h.RecordMessages(newMsgs)
}
