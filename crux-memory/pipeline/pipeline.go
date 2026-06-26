// Package pipeline orchestrates the L0→L1→L2→L3 flow.
//
// The pipeline is event-driven: a session accumulates L0 messages, and when
// a trigger fires (debounced by message count / time), the pipeline runs:
//   L1 (LLM extraction + dedup) → L2 (scene aggregation) → L3 (persona update)
//
// All three LLM stages are async and write to disk before returning, so
// crashes never lose data — the worst case is a partial persona update.
package pipeline

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/crux-memory/crux-memory/l0"
	"github.com/crux-memory/crux-memory/l1"
	"github.com/crux-memory/crux-memory/l2"
	"github.com/crux-memory/crux-memory/l3"
	"github.com/crux-memory/crux-memory/llm"
)

// Config controls when the pipeline triggers.
type Config struct {
	// MessagesPerTick: run L1/L2/L3 after this many new messages accumulate.
	MessagesPerTick int
	// MinInterval: minimum time between pipeline runs.
	MinInterval time.Duration
	// MaxInterval: maximum time without a pipeline run even if messages count is low.
	MaxInterval time.Duration
}

// DefaultConfig returns sane defaults.
func DefaultConfig() Config {
	return Config{
		MessagesPerTick: 6,
		MinInterval:     30 * time.Second,
		MaxInterval:     5 * time.Minute,
	}
}

// Pipeline owns all stores + LLM client.
type Pipeline struct {
	cfg      Config
	l0Rec    *l0.Recorder
	l1Writer *l1.Writer
	l1Ext    *l1.Extractor
	l2Store  *l2.Store
	l2Ext    *l2.Extractor
	l3Store  *l3.Store
	l3Gen    *l3.Generator
	llm      *llm.Client

	mu        sync.Mutex
	pending   map[string]int    // sessionID → unprocessed message count
	lastRun   map[string]time.Time // sessionID → last pipeline run time
	lastSeen  map[string]time.Time // sessionID → last message received time
}

// New constructs a pipeline rooted at baseDir.
func New(baseDir string, llmClient *llm.Client, cfg Config) (*Pipeline, error) {
	l0Rec, err := l0.NewRecorder(baseDir)
	if err != nil {
		return nil, fmt.Errorf("pipeline: l0: %w", err)
	}
	l1Writer, err := l1.NewWriter(baseDir)
	if err != nil {
		return nil, fmt.Errorf("pipeline: l1: %w", err)
	}
	l1Ext := l1.NewExtractor(llmClient, l1Writer)
	l2Store, err := l2.NewStore(baseDir)
	if err != nil {
		return nil, fmt.Errorf("pipeline: l2: %w", err)
	}
	l2Ext := l2.NewExtractor(l2Store, llmClient)
	l3Store, err := l3.NewStore(baseDir)
	if err != nil {
		return nil, fmt.Errorf("pipeline: l3: %w", err)
	}
	l3Gen := l3.NewGenerator(llmClient, l3Store)

	return &Pipeline{
		cfg:      cfg,
		l0Rec:    l0Rec,
		l1Writer: l1Writer,
		l1Ext:    l1Ext,
		l2Store:  l2Store,
		l2Ext:    l2Ext,
		l3Store:  l3Store,
		l3Gen:    l3Gen,
		llm:      llmClient,
		pending:  make(map[string]int),
		lastRun:  make(map[string]time.Time),
		lastSeen: make(map[string]time.Time),
	}, nil
}

// L0 exposes the L0 recorder so callers can write raw messages directly.
func (p *Pipeline) L0() *l0.Recorder { return p.l0Rec }

// Capture writes one message to L0 and increments the session's pending count.
func (p *Pipeline) Capture(ctx context.Context, sessionID string, role l0.Role, content string) error {
	if _, err := p.l0Rec.Record(sessionID, role, content); err != nil {
		return err
	}
	p.mu.Lock()
	p.pending[sessionID]++
	p.lastSeen[sessionID] = time.Now()
	p.mu.Unlock()
	return nil
}

// MaybeTick evaluates trigger conditions for all sessions and runs the
// pipeline for any session that has accumulated enough messages or has gone
// too long without a run.
func (p *Pipeline) MaybeTick(ctx context.Context) error {
	p.mu.Lock()
	sessions := make([]string, 0, len(p.pending))
	for s, n := range p.pending {
		if n >= p.cfg.MessagesPerTick ||
			(time.Since(p.lastRun[s]) > p.cfg.MaxInterval && n > 0) {
			sessions = append(sessions, s)
		}
	}
	p.mu.Unlock()

	var errs []error
	for _, s := range sessions {
		if err := p.Run(ctx, s); err != nil {
			errs = append(errs, fmt.Errorf("session %s: %w", s, err))
			log.Printf("[pipeline] session=%s run error: %v", s, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("pipeline: %d session errors", len(errs))
	}
	return nil
}

// Run forces the pipeline to run for a session (used by tests + ForceTick).
func (p *Pipeline) Run(ctx context.Context, sessionID string) error {
	p.mu.Lock()
	p.pending[sessionID] = 0
	p.lastRun[sessionID] = time.Now()
	p.mu.Unlock()

	// 1. L1 extraction on recent messages.
	recent, err := p.l0Rec.ReadRecent(sessionID, p.cfg.MessagesPerTick*4)
	if err != nil {
		return fmt.Errorf("pipeline: read L0: %w", err)
	}
	if len(recent) == 0 {
		return nil
	}

	l1Input := l1.ExtractInput{
		Messages: convertMessages(recent),
	}
	if existing, err := p.l1Writer.ReadAll(); err == nil {
		l1Input.ExistingRecords = existing
	}
	extRes, err := p.l1Ext.Extract(ctx, l1Input)
	if err != nil {
		return fmt.Errorf("pipeline: L1: %w", err)
	}
	log.Printf("[pipeline] session=%s L1 extracted=%d stored=%d scenes=%v",
		sessionID, extRes.ExtractedCount, extRes.StoredCount, extRes.SceneNames)

	// 2. L2 scene aggregation across all L1 records.
	allL1, err := p.l1Writer.ReadAll()
	if err != nil {
		return fmt.Errorf("pipeline: read L1: %w", err)
	}
	l2Input := l2.PersistInput{
		Records: allL1,
		Changed: extRes.RecordsAsIDs(),
	}
	l2Res, err := p.l2Ext.Persist(ctx, l2Input)
	if err != nil {
		return fmt.Errorf("pipeline: L2: %w", err)
	}
	log.Printf("[pipeline] session=%s L2 touched=%v created=%v linked=%d",
		sessionID, l2Res.ScenesTouched, l2Res.ScenesCreated, l2Res.RecordsLinked)

	// 3. L3 persona update.
	if err := p.updatePersona(ctx, sessionID, l2Res); err != nil {
		return fmt.Errorf("pipeline: L3: %w", err)
	}

	return nil
}

func (p *Pipeline) updatePersona(ctx context.Context, sessionID string, l2Res *l2.PersistResult) error {
	scenes, err := p.l2Store.ReadScenes()
	if err != nil {
		return err
	}
	existing, err := p.l3Store.Read()
	if err != nil {
		return err
	}

	changed := append([]string{}, l2Res.ScenesCreated...)
	changed = append(changed, l2Res.ScenesTouched...)

	changedContent := ""
	for _, sc := range scenes {
		if !contains(changed, sc.Filename) {
			continue
		}
		changedContent += fmt.Sprintf("\n### %s\n%s\n", sc.Filename, sc.Content)
	}

	mode := "first"
	if existing != "" {
		mode = "incremental"
	}
	totalMem := 0
	if all, err := p.l1Writer.ReadAll(); err == nil {
		totalMem = len(all)
	}

	_, err = p.l3Gen.Generate(ctx, l3.GenerateInput{
		Mode:                 mode,
		TotalMemories:        totalMem,
		SceneCount:           len(scenes),
		ChangedSceneNames:    changed,
		ChangedScenesContent: changedContent,
		ExistingPersona:      existing,
	})
	return err
}

func convertMessages(msgs []l0.Message) []l1.Message {
	out := make([]l1.Message, len(msgs))
	for i, m := range msgs {
		out[i] = l1.Message{ID: m.ID, Role: string(m.Role), Content: m.Content}
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}