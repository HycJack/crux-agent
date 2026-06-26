package sse

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestScanNormal(t *testing.T) {
	input := "data: {\"hello\":\"world\"}\n\ndata: {\"foo\":\"bar\"}\ndata: [DONE]\n"
	var events []string
	err := Scan(context.Background(), strings.NewReader(input), ScanConfig{}, func(data string) error {
		events = append(events, data)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0] != `{"hello":"world"}` {
		t.Errorf("event 0 = %q", events[0])
	}
	if events[1] != `{"foo":"bar"}` {
		t.Errorf("event 1 = %q", events[1])
	}
}

func TestScanNoDone(t *testing.T) {
	// Clean EOF without [DONE] should return nil
	input := "data: {\"a\":1}\ndata: {\"b\":2}\n"
	var events []string
	err := Scan(context.Background(), strings.NewReader(input), ScanConfig{}, func(data string) error {
		events = append(events, data)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestScanLineTooLong(t *testing.T) {
	// Create a line that exceeds the small max buffer
	bigLine := "data: " + strings.Repeat("x", 200) + "\ndata: [DONE]\n"
	err := Scan(context.Background(), strings.NewReader(bigLine), ScanConfig{
		InitialBufSize: 64,
		MaxBufSize:     128,
	}, func(data string) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for line exceeding buffer")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("error should mention 'exceeded', got: %v", err)
	}
}

func TestScanCallbackError(t *testing.T) {
	input := "data: {\"a\":1}\ndata: {\"b\":2}\ndata: [DONE]\n"
	callCount := 0
	err := Scan(context.Background(), strings.NewReader(input), ScanConfig{}, func(data string) error {
		callCount++
		if callCount == 2 {
			return io.ErrUnexpectedEOF
		}
		return nil
	})
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("expected io.ErrUnexpectedEOF, got: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls, got %d", callCount)
	}
}

func TestScanIgnoresNonDataLines(t *testing.T) {
	input := "event: message\nid: 123\ndata: {\"ok\":true}\n\nretry: 5000\ndata: [DONE]\n"
	var events []string
	err := Scan(context.Background(), strings.NewReader(input), ScanConfig{}, func(data string) error {
		events = append(events, data)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0] != `{"ok":true}` {
		t.Errorf("event = %q", events[0])
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := ScanConfig{}.withDefaults()
	if cfg.InitialBufSize != DefaultInitialBufSize {
		t.Errorf("InitialBufSize = %d, want %d", cfg.InitialBufSize, DefaultInitialBufSize)
	}
	if cfg.MaxBufSize != DefaultMaxBufSize {
		t.Errorf("MaxBufSize = %d, want %d", cfg.MaxBufSize, DefaultMaxBufSize)
	}
}

func TestConfigInitialLargerThanMax(t *testing.T) {
	cfg := ScanConfig{InitialBufSize: 9999, MaxBufSize: 100}.withDefaults()
	if cfg.InitialBufSize != 100 {
		t.Errorf("InitialBufSize should be clamped to MaxBufSize, got %d", cfg.InitialBufSize)
	}
}

func TestScanContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	input := "data: {\"a\":1}\ndata: [DONE]\n"
	err := Scan(ctx, strings.NewReader(input), ScanConfig{}, func(data string) error {
		return nil
	})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestScanContextCancelWithCloser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pr, _ := io.Pipe() // pipe implements io.Closer; blocks on Read until data arrives

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- Scan(ctx, pr, ScanConfig{}, func(data string) error {
			return nil
		})
	}()

	// Wait for goroutine to start, then cancel.
	// Scan is now blocking on pr.Read() — cancel should close pr.
	<-started
	cancel()

	err := <-done
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func BenchmarkScan(b *testing.B) {
	// Simulate 1000 small SSE events
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString("data: {\"delta\":\"hello\",\"index\":")
		sb.WriteString(strings.Repeat(" ", 50))
		sb.WriteString("}\n")
	}
	sb.WriteString("data: [DONE]\n")
	input := sb.String()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		count := 0
		_ = Scan(context.Background(), strings.NewReader(input), ScanConfig{}, func(data string) error {
			count++
			return nil
		})
	}
}
