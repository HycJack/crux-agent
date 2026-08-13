package defaults

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/hycjack/agent-engine/plugin"
)

// ─── Log level ──────────────────────────────────────────────────────────────

// Level defines log severity.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ─── Logger ─────────────────────────────────────────────────────────────────

// Logger is a lightweight structured logger.
type Logger struct {
	mu        sync.Mutex
	level     Level
	component string
	writer    io.Writer
}

// NewLogger creates a new logger that writes to stderr.
func NewLogger(component string) *Logger {
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
func (l *Logger) Debug(msg string, fields map[string]any) {
	l.log(LevelDebug, msg, fields)
}

// Info logs an info message.
func (l *Logger) Info(msg string, fields map[string]any) {
	l.log(LevelInfo, msg, fields)
}

// Warn logs a warning message.
func (l *Logger) Warn(msg string, fields map[string]any) {
	l.log(LevelWarn, msg, fields)
}

// Error logs an error message.
func (l *Logger) Error(msg string, fields map[string]any) {
	l.log(LevelError, msg, fields)
}

func (l *Logger) log(level Level, msg string, fields map[string]any) {
	if level < l.level {
		return
	}

	event := struct {
		Timestamp string         `json:"timestamp"`
		Level     string         `json:"level"`
		Component string         `json:"component"`
		Message   string         `json:"message"`
		Fields    map[string]any `json:"fields,omitempty"`
	}{
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Level:     level.String(),
		Component: l.component,
		Message:   msg,
		Fields:    fields,
	}

	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	l.mu.Lock()
	_, _ = fmt.Fprintln(l.writer, string(data))
	l.mu.Unlock()

	// Also log to stdlib log as fallback
	if level >= LevelWarn {
		log.Printf("[%s] [%s] %s", level.String(), l.component, msg)
	}
}

// compile-time assertion
var _ plugin.ObservePlugin = (*Logger)(nil)
