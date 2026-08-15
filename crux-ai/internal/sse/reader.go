// Reader is a context-aware SSE line iterator built on bufio.Scanner.
//
// It is the recommended drop-in for the bufio.Scanner pattern used by
// every native provider:
//
//	for line, ok := range sse.NewReader(ctx, body, cfg).Lines() {
//	    if !ok { break }
//	    ...
//	}
//
// Compared to a raw bufio.Scanner loop, Reader guarantees:
//
//   - Cancellation: when ctx is cancelled, the underlying io.Closer (e.g.
//     http.Response.Body) is closed so the in-flight Read returns
//     immediately. Without this, ctx cancellation cannot unblock a slow
//     provider and the goroutine hangs until either the next event
//     arrives or the connection times out.
//   - Bounded memory: the line-size cap mirrors ScanConfig.MaxBufSize
//     (default 1MB). Lines that exceed the cap surface as a
//     single-bufio.ErrTooLong equivalent via ErrLineTooLong, then the
//     iterator ends.
//   - Clean EOF: when the provider closes the stream cleanly (with or
//     without a final "data: [DONE]"), the iterator returns ok=false
//     and Err() == nil.
//
// Reader does NOT interpret "data: " lines for the caller; providers keep
// full control of SSE framing, identical to the inline bufio.Scanner
// loops they already run. This keeps the diff to each provider minimal
// while fixing the ctx-cancellation bug.
package sse

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
)

// ErrLineTooLong is returned (via Lines + Err) when a single line
// exceeds MaxBufSize. This mirrors bufio.ErrTooLong, exposed so callers
// can branch without importing bufio.
var ErrLineTooLong = errors.New("sse: line exceeded max buffer size")

// ReaderConfig mirrors ScanConfig. Use this when constructing a Reader.
//
// InitialBufSize and MaxBufSize follow the same defaults as ScanConfig
// when zero / negative; see ScanConfig.withDefaults.
type ReaderConfig struct {
	InitialBufSize int
	MaxBufSize     int
}

func (c ReaderConfig) withDefaults() ReaderConfig {
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

// Reader yields non-empty, non-data-prefix lines as they arrive from r.
//
// Lifetime: Lines() spawns an internal goroutine that ends when:
//   - ctx is cancelled (and the io.Closer closes the source),
//   - the source returns io.EOF,
//   - the scanner reports an error (e.g. ErrLineTooLong),
//   - the caller breaks out of the range loop.
type Reader struct {
	lines chan string
	err   error
	done  chan struct{}
}

// NewReader returns a Reader bound to ctx. The returned Reader must be
// drained (via range on Lines) until ok=false for the internal goroutine
// to release its handle on r.
func NewReader(ctx context.Context, r io.Reader, cfg ReaderConfig) *Reader {
	cfg = cfg.withDefaults()
	rd := &Reader{
		lines: make(chan string, 64),
		done:  make(chan struct{}),
	}
	go rd.run(ctx, r, cfg)
	return rd
}

func (rd *Reader) run(ctx context.Context, r io.Reader, cfg ReaderConfig) {
	defer close(rd.done)
	defer close(rd.lines)

	// If r implements io.Closer, watch ctx and close it on cancel so any
	// blocking Read unblocks immediately. This is the cancellation hook
	// that bufio.Scanner does not provide on its own.
	if closer, ok := r.(io.Closer); ok {
		watcherDone := make(chan struct{})
		defer close(watcherDone)
		go func() {
			select {
			case <-ctx.Done():
				_ = closer.Close()
			case <-watcherDone:
			}
		}()
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, cfg.InitialBufSize), cfg.MaxBufSize)

	for scanner.Scan() {
		if ctx.Err() != nil {
			rd.err = ctx.Err()
			return
		}
		line := scanner.Text()
		select {
		case <-ctx.Done():
			rd.err = ctx.Err()
			return
		case rd.lines <- line:
		}
	}

	if err := scanner.Err(); err != nil {
		switch {
		case ctx.Err() != nil:
			rd.err = ctx.Err()
		case err == bufio.ErrTooLong:
			rd.err = fmt.Errorf("%w (max %d bytes)", ErrLineTooLong, cfg.MaxBufSize)
		default:
			rd.err = fmt.Errorf("sse: scan error: %w", err)
		}
		return
	}
}

// Lines returns the receive end of the line channel. The range loop ends
// when the source is exhausted, ctx is cancelled, or an error is hit.
//
// Typical use:
//
//	for line, ok := range sse.NewReader(ctx, body, cfg).Lines() {
//	    if !ok { break }
//	    if !strings.HasPrefix(line, "data: ") { continue }
//	    data := strings.TrimPrefix(line, "data: ")
//	    if data == "[DONE]" { break }
//	    ...
//	}
func (rd *Reader) Lines() <-chan string { return rd.lines }

// Err returns the first non-nil error that ended the stream, or nil on
// clean EOF / context cancel. Always inspect after the Lines() loop.
func (rd *Reader) Err() error { return rd.err }

// Wait blocks until the internal goroutine has ended. Useful for callers
// that want to guarantee the source is no longer being read before they
// close it themselves. Most callers don't need this.
func (rd *Reader) Wait() { <-rd.done }
