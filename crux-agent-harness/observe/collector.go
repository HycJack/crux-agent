// Package observe provides structured logging, tracing, and run-level
// statistics for agent operations.
package observe

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/hycjack/crux-ai/core"
)

// RunSummary is a per-run rollup of the agent loop execution.
type RunSummary struct {
	StepCount       int             // Total LLM turns
	ToolCallCount   int             // Total tool calls executed
	ErrorCount      int             // Total errors (tool calls / LLM calls)
	StartedAt       time.Time       // Wall-clock run start
	EndedAt         time.Time       // Wall-clock run end
	Duration        time.Duration   // EndedAt - StartedAt
	TotalUsage      core.Usage      // Aggregate input/output/cache
	TotalCost       float64         // Aggregate cost
	ErrorsByKind    map[string]int  // Bucket counts: "auth" / "rate_limit" / "server" / "overflow" / "tool" / "abort" / "other"
	Providers       map[string]int  // Provider hit count
	StopReasonFinal core.StopReason // Final assistant StopReason
}

// RunCoverage is per-provider / per-model / per-tool coverage stats.
type RunCoverage struct {
	ProviderHits map[string]int // provider id -> step count
	ModelHits    map[string]int // model id -> step count
	ToolsByName  map[string]int // tool name -> call count
	TotalUsage   core.Usage
	TotalCost    float64
}

// RunCollector accumulates per-run stats in a lock-free / mostly-atomic way.
// Use it from the harness event subscriber to populate RunSummary and
// RunCoverage at agent_end time.
type RunCollector struct {
	startedAt time.Time
	endedAt   atomic.Int64 // unix-nano; 0 = not ended yet
	mu        sync.Mutex

	stepCount    atomic.Int32
	toolCount    atomic.Int32
	errorCount   atomic.Int32
	errorsByKind map[string]int
	providerHits map[string]int
	modelHits    map[string]int
	toolsByName  map[string]int
	usage        core.Usage
	cost         atomic.Uint64 // float64 bits

	stopReasonFinal atomic.Uint32
}

// NewRunCollector creates a collector with a startedAt timestamp.
func NewRunCollector() *RunCollector {
	return &RunCollector{
		startedAt:    time.Now(),
		errorsByKind: make(map[string]int),
		providerHits: make(map[string]int),
		modelHits:    make(map[string]int),
		toolsByName:  make(map[string]int),
	}
}

// RecordStep records one LLM turn.
func (r *RunCollector) RecordStep(model core.Model) {
	r.stepCount.Add(1)
	r.mu.Lock()
	r.providerHits[string(model.Provider)]++
	r.modelHits[model.ID]++
	r.mu.Unlock()
}

// RecordToolCall records one tool-call result.
func (r *RunCollector) RecordToolCall(name string, isError bool) {
	r.toolCount.Add(1)
	if isError {
		r.errorCount.Add(1)
	}
	r.mu.Lock()
	r.toolsByName[name]++
	r.mu.Unlock()
}

// RecordError categorizes an error into the per-kind counter.
func (r *RunCollector) RecordError(err error) {
	if err == nil {
		return
	}
	r.errorCount.Add(1)
	kind := classifyError(err)
	r.mu.Lock()
	r.errorsByKind[kind]++
	r.mu.Unlock()
}

// RecordUsage accumulates usage into the per-run totals.
func (r *RunCollector) RecordUsage(u core.Usage) {
	r.mu.Lock()
	r.usage.Input += u.Input
	r.usage.Output += u.Output
	r.usage.CacheRead += u.CacheRead
	r.usage.CacheWrite += u.CacheWrite
	r.usage.Cost.Input += u.Cost.Input
	r.usage.Cost.Output += u.Cost.Output
	r.usage.Cost.CacheRead += u.Cost.CacheRead
	r.usage.Cost.CacheWrite += u.Cost.CacheWrite
	r.usage.Cost.Total += u.Cost.Total
	r.cost.Store(math.Float64bits(r.costBits() + u.Cost.Total))
	r.mu.Unlock()
}

// SetStopReason records the final stop reason (last one wins).
func (r *RunCollector) SetStopReason(sr core.StopReason) {
	r.stopReasonFinal.Store(uint32(stopReasonIndex(sr)))
}

// MarkRunEnded finalizes the run. Returns true exactly once.
func (r *RunCollector) MarkRunEnded() bool {
	return r.endedAt.CompareAndSwap(0, time.Now().UnixNano())
}

// Snapshot returns a finalized RunSummary and RunCoverage.
func (r *RunCollector) Snapshot() (RunSummary, RunCoverage) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var endedTime time.Time
	if nano := r.endedAt.Load(); nano != 0 {
		endedTime = time.Unix(0, nano)
	}

	var dur time.Duration
	if !endedTime.IsZero() {
		dur = endedTime.Sub(r.startedAt)
	}

	sum := RunSummary{
		StepCount:       int(r.stepCount.Load()),
		ToolCallCount:   int(r.toolCount.Load()),
		ErrorCount:      int(r.errorCount.Load()),
		StartedAt:       r.startedAt,
		EndedAt:         endedTime,
		Duration:        dur,
		TotalUsage:      r.usage,
		TotalCost:       math.Float64frombits(r.cost.Load()),
		ErrorsByKind:    copyMap(r.errorsByKind),
		Providers:       copyMap(r.providerHits),
		StopReasonFinal: stopReasonFromIndex(uint8(r.stopReasonFinal.Load())),
	}

	cov := RunCoverage{
		ProviderHits: copyMap(r.providerHits),
		ModelHits:    copyMap(r.modelHits),
		ToolsByName:  copyMap(r.toolsByName),
		TotalUsage:   r.usage,
		TotalCost:    math.Float64frombits(r.cost.Load()),
	}

	return sum, cov
}

// Reset clears all counters so the collector can be reused for a new run.
func (r *RunCollector) Reset() {
	r.stepCount.Store(0)
	r.toolCount.Store(0)
	r.errorCount.Store(0)
	r.endedAt.Store(0)
	r.cost.Store(0)
	r.stopReasonFinal.Store(0)
	r.mu.Lock()
	r.startedAt = time.Now()
	r.errorsByKind = make(map[string]int)
	r.providerHits = make(map[string]int)
	r.modelHits = make(map[string]int)
	r.toolsByName = make(map[string]int)
	r.usage = core.Usage{}
	r.mu.Unlock()
}

// --- private helpers ---

func (r *RunCollector) costBits() float64 {
	return math.Float64frombits(r.cost.Load())
}

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	if core.IsAuthError(err) {
		return "auth"
	}
	var rl *core.RateLimitError
	if errors.As(err, &rl) {
		return "rate_limit"
	}
	var srv *core.ServerError
	if errors.As(err, &srv) {
		return "server"
	}
	if core.IsContextOverflow(err) {
		return "overflow"
	}
	return "other"
}

func stopReasonIndex(sr core.StopReason) uint8 {
	switch sr {
	case core.StopStop:
		return 1
	case core.StopLength:
		return 2
	case core.StopToolUse:
		return 3
	case core.StopError:
		return 4
	case core.StopAborted:
		return 5
	}
	return 0
}

func stopReasonFromIndex(i uint8) core.StopReason {
	switch i {
	case 1:
		return core.StopStop
	case 2:
		return core.StopLength
	case 3:
		return core.StopToolUse
	case 4:
		return core.StopError
	case 5:
		return core.StopAborted
	}
	return ""
}

func copyMap(m map[string]int) map[string]int {
	if m == nil {
		return nil
	}
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
