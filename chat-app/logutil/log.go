// Package logutil provides daily-rotating file logging for the chat app.
//
// Logs are written to <appDataDir>/logs/<YYYY-MM-DD>.log, one file per day.
// Each log line is prefixed with a timestamp and the log level.
package logutil

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	mu      sync.Mutex
	logFile *os.File
	logger  *log.Logger
	curDate string
	dataDir string
	enabled bool
)

// Init initializes the log system with the given data directory.
// Logs will be written to <dataDir>/logs/<YYYY-MM-DD>.log.
// Call Init once at startup.
func Init(appDataDir string) error {
	mu.Lock()
	defer mu.Unlock()

	dataDir = appDataDir
	enabled = true

	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create logs dir: %w", err)
	}

	if err := rotateUnsafe(); err != nil {
		return err
	}

	return nil
}

// rotateUnsafe rotates the log file if the date has changed.
// Must be called with mu held.
func rotateUnsafe() error {
	today := time.Now().Format("2006-01-02")
	if today == curDate && logFile != nil {
		return nil
	}

	// Close old file
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}

	curDate = today
	logPath := filepath.Join(dataDir, "logs", today+".log")

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", logPath, err)
	}

	logFile = f
	logger = log.New(logFile, "", log.Ltime|log.Lmicroseconds)
	return nil
}

// ensureRotate checks if the log file needs rotating. Safe for concurrent use.
func ensureRotate() {
	mu.Lock()
	defer mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if today != curDate {
		_ = rotateUnsafe()
	}
}

// Infof writes an info-level log entry.
func Infof(format string, args ...interface{}) {
	if !enabled {
		return
	}
	ensureRotate()
	mu.Lock()
	defer mu.Unlock()
	if logger != nil {
		logger.Printf("[INFO] "+format, args...)
	}
}

// Warnf writes a warning-level log entry.
func Warnf(format string, args ...interface{}) {
	if !enabled {
		return
	}
	ensureRotate()
	mu.Lock()
	defer mu.Unlock()
	if logger != nil {
		logger.Printf("[WARN] "+format, args...)
	}
}

// Errorf writes an error-level log entry.
func Errorf(format string, args ...interface{}) {
	if !enabled {
		return
	}
	ensureRotate()
	mu.Lock()
	defer mu.Unlock()
	if logger != nil {
		logger.Printf("[ERROR] "+format, args...)
	}
}

// Debugf writes a debug-level log entry.
func Debugf(format string, args ...interface{}) {
	if !enabled {
		return
	}
	ensureRotate()
	mu.Lock()
	defer mu.Unlock()
	if logger != nil {
		logger.Printf("[DEBUG] "+format, args...)
	}
}

// Close closes the log file. Call at shutdown.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
	enabled = false
}
