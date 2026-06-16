// Package sse provides a robust Server-Sent Events (SSE) line scanner
// for reading streaming LLM responses.
//
// Key improvements over raw bufio.Scanner:
//   - Context-aware: stops immediately when ctx is cancelled
//   - Smaller initial buffer (4KB) that grows on demand, saving memory
//   - Proper handling of bufio.ErrTooLong (single line > max buffer)
//   - Clean callback-based API for event processing
package sse

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

const (
	// DefaultInitialBufSize is the initial buffer size for most SSE lines.
	// Most LLM SSE events are < 10KB, so 4KB covers the common case
	// and the buffer doubles as needed up to MaxBufSize.
	DefaultInitialBufSize = 4 * 1024 // 4KB

	// DefaultMaxBufSize is the maximum allowed line size.
	// 1MB handles even very large tool-call argument payloads.
	DefaultMaxBufSize = 1024 * 1024 // 1MB
)

// ScanConfig controls the SSE scanner behavior.
type ScanConfig struct {
	// InitialBufSize is the starting buffer size (default 4KB).
	InitialBufSize int
	// MaxBufSize is the maximum allowed single-line size (default 1MB).
	MaxBufSize int
}

func (c ScanConfig) withDefaults() ScanConfig {
	if c.InitialBufSize <= 0 {
		c.InitialBufSize = DefaultInitialBufSize
	}
	if c.MaxBufSize <= 0 {
		c.MaxBufSize = DefaultMaxBufSize
	}
	if c.InitialBufSize > c.MaxBufSize {
		c.InitialBufSize = c.MaxBufSize
	}
	return c
}

// Scan reads SSE lines from r and calls onData for each "data: " line.
// It stops when:
//   - ctx is cancelled (returns ctx.Err() immediately)
//   - "data: [DONE]" is encountered (returns nil)
//   - The underlying reader returns an error (e.g. connection closed)
//   - A single line exceeds MaxBufSize (returns descriptive error)
//
// Context cancellation is enforced by closing r (via io.Closer) as soon
// as ctx.Done() fires, which unblocks any pending Read call. If r does
// not implement io.Closer, cancellation relies on the reader itself
// being closed by the caller (e.g. resp.Body.Close()).
func Scan(ctx context.Context, r io.Reader, cfg ScanConfig, onData func(data string) error) error {
	cfg = cfg.withDefaults()

	// Start a goroutine that closes the reader when ctx is cancelled.
	// This unblocks any blocking Read inside bufio.Scanner.
	if closer, ok := r.(io.Closer); ok {
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-ctx.Done():
				closer.Close()
			case <-done:
			}
		}()
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, cfg.InitialBufSize), cfg.MaxBufSize)

	for scanner.Scan() {
		// Check context between lines for responsiveness.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return nil
		}
		if err := onData(data); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		// If context was cancelled, return ctx error (more informative).
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == bufio.ErrTooLong {
			return fmt.Errorf("sse: single event line exceeded %d bytes (possible data corruption)", cfg.MaxBufSize)
		}
		return fmt.Errorf("sse: scan error: %w", err)
	}
	return nil
}
