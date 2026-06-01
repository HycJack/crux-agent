// Package observe provides structured logging and tracing for agent operations.
package observe

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Level is the log level.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// Event is a structured log event.
type Event struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Component string         `json:"component"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// Logger writes structured log events.
type Logger struct {
	mu      sync.Mutex
	level   Level
	component string
	writer  io.Writer
}

// New creates a new Logger.
func New(component string) *Logger {
	return &Logger{
		level:     LevelInfo,
		component: component,
		writer:    os.Stderr,
	}
}

// SetLevel sets the minimum log level.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// SetWriter sets the output writer.
func (l *Logger) SetWriter(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writer = w
}

// Debug logs a debug message.
func (l *Logger) Debug(msg string, fields ...map[string]any) {
	l.log(LevelDebug, msg, fields...)
}

// Info logs an info message.
func (l *Logger) Info(msg string, fields ...map[string]any) {
	l.log(LevelInfo, msg, fields...)
}

// Warn logs a warning message.
func (l *Logger) Warn(msg string, fields ...map[string]any) {
	l.log(LevelWarn, msg, fields...)
}

// Error logs an error message.
func (l *Logger) Error(msg string, fields ...map[string]any) {
	l.log(LevelError, msg, fields...)
}

func (l *Logger) log(level Level, msg string, fields ...map[string]any) {
	l.mu.Lock()
	if level < l.level {
		l.mu.Unlock()
		return
	}
	w := l.writer
	comp := l.component
	l.mu.Unlock()

	levelStr := "info"
	switch level {
	case LevelDebug:
		levelStr = "debug"
	case LevelWarn:
		levelStr = "warn"
	case LevelError:
		levelStr = "error"
	}

	var merged map[string]any
	if len(fields) > 0 {
		merged = fields[0]
	}

	evt := Event{
		Timestamp: time.Now(),
		Level:     levelStr,
		Component: comp,
		Message:   msg,
		Fields:    merged,
	}

	b, _ := json.Marshal(evt)
	fmt.Fprintf(w, "%s\n", b)
}

// --- Turn Timer ---

// TurnTimer measures the duration of a turn.
type TurnTimer struct {
	mu    sync.Mutex
	start time.Time
	name  string
}

// NewTurnTimer starts a timer.
func NewTurnTimer(name string) *TurnTimer {
	return &TurnTimer{name: name, start: time.Now()}
}

// Elapsed returns the duration since the timer was created.
func (t *TurnTimer) Elapsed() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return time.Since(t.start)
}

// TokenUsage records token usage for a turn.
type TokenUsage struct {
	TurnID    string `json:"turnId"`
	Input     int    `json:"input"`
	Output    int    `json:"output"`
	CacheRead int    `json:"cacheRead"`
	Duration  int64  `json:"durationMs"`
}
