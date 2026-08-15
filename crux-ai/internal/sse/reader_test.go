package sse

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestReaderLinesDrains(t *testing.T) {
	input := "first\nsecond\nthird\n"
	rd := NewReader(context.Background(), strings.NewReader(input), ReaderConfig{})
	var got []string
	for line := range rd.Lines() {
		got = append(got, line)
	}
	if err := rd.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 || got[0] != "first" || got[1] != "second" || got[2] != "third" {
		t.Fatalf("unexpected lines: %#v", got)
	}
}

func TestReaderErrLineTooLong(t *testing.T) {
	// Build a payload with a line that exceeds the small MaxBufSize.
	big := strings.Repeat("x", 256)
	input := "small\n" + big + "\n"
	rd := NewReader(context.Background(), strings.NewReader(input), ReaderConfig{
		InitialBufSize: 64,
		MaxBufSize:     128,
	})
	for range rd.Lines() {
		// drain; we just need to wait for the error
	}
	err := rd.Err()
	if err == nil {
		t.Fatal("expected error for oversized line")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("error should mention exceeded: %v", err)
	}
}

// TestReaderCancelClosesSource confirms that cancelling ctx unblocks the
// internal goroutine even when the underlying reader would otherwise
// hang (which is the bug the old bufio.Scanner-in-provider pattern has).
func TestReaderCancelClosesSource(t *testing.T) {
	// blockReader never returns EOF and never yields data; without
	// cancellation, a bufio.Scanner-driven loop on this reader hangs
	// forever.
	r := newBlockingReader()
	ctx, cancel := context.WithCancel(context.Background())
	rd := NewReader(ctx, r, ReaderConfig{})

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	doneCh := make(chan error, 1)
	go func() {
		for range rd.Lines() {
			// drain
		}
		doneCh <- rd.Err()
	}()

	select {
	case err := <-doneCh:
		if !strings.Contains(err.Error(), "context canceled") {
			t.Errorf("expected context-canceled error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Reader.Lines did not unblock after ctx cancel")
	}
}

// blockingReader satisfies io.Reader and io.Closer but never returns data
// unless its Close channel has been signalled.
type blockingReader struct {
	closed chan struct{}
}

func newBlockingReader() *blockingReader { return &blockingReader{closed: make(chan struct{})} }

func (b *blockingReader) Read(p []byte) (int, error) {
	<-b.closed
	return 0, errClosed
}

func (b *blockingReader) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

var errClosed = &readAfterCloseErr{}

type readAfterCloseErr struct{}

func (*readAfterCloseErr) Error() string { return "read after close" }
