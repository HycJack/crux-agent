package main

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// captureStdout redirects os.Stdout to a pipe for the lifetime of the
// returned restore closure. The caller MUST defer the closure; calling
// it returns the bytes captured during the test.
//
// Typical usage:
//
//	restore := captureStdout(t)
//	defer restore()
//	// ... writes to os.Stdout go to the pipe ...
//	// bytes := restore()
func captureStdout(t *testing.T) func() []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	return func() []byte {
		os.Stdout = orig
		_ = w.Close()
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		_ = r.Close()
		return buf.Bytes()
	}
}

// captureStdin swaps os.Stdin for a pipe, returning a writer the caller
// feeds simulated keystrokes into plus a closer that restores os.Stdin
// and signals EOF to the reader.
func captureStdin(t *testing.T) (write func([]byte), closeIt func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	return func(p []byte) {
			_, _ = w.Write(p)
		}, func() {
			_ = w.Close()
			_ = r.Close()
			os.Stdin = orig
		}
}

func TestProcessKey_PrintableAppendsToBuffer(t *testing.T) {
	restore := captureStdout(t)
	defer restore()
	ev := newInputEvents()
	var buf []byte
	for _, c := range []byte("hi") {
		buf = processKey(c, buf, ev)
	}
	if string(buf) != "hi" {
		t.Fatalf("expected buffer \"hi\", got %q", buf)
	}
	select {
	case <-ev.line:
		t.Fatal("no line should be emitted yet")
	default:
	}
}

func TestProcessKey_EnterEmitsLineAndClearsBuffer(t *testing.T) {
	restore := captureStdout(t)
	defer restore()
	ev := newInputEvents()
	var buf []byte
	for _, c := range []byte("hello") {
		buf = processKey(c, buf, ev)
	}
	buf = processKey('\r', buf, ev)
	if buf != nil {
		t.Fatalf("buffer should be nil after Enter, got %q", buf)
	}
	select {
	case got := <-ev.line:
		if got != "hello" {
			t.Fatalf("expected line \"hello\", got %q", got)
		}
	default:
		t.Fatal("expected line on ev.line")
	}
}

func TestProcessKey_NewlineAlsoEmitsLine(t *testing.T) {
	restore := captureStdout(t)
	defer restore()
	ev := newInputEvents()
	var buf []byte
	for _, c := range []byte("hi") {
		buf = processKey(c, buf, ev)
	}
	buf = processKey('\n', buf, ev)
	select {
	case got := <-ev.line:
		if got != "hi" {
			t.Fatalf("expected \"hi\", got %q", got)
		}
	default:
		t.Fatal("expected line")
	}
}

func TestProcessKey_EscEmitsSignalAndClearsBuffer(t *testing.T) {
	restore := captureStdout(t)
	defer restore()
	ev := newInputEvents()
	var buf []byte
	for _, c := range []byte("partial") {
		buf = processKey(c, buf, ev)
	}
	buf = processKey(0x1b, buf, ev) // ESC
	if buf != nil {
		t.Fatalf("buffer should be nil after ESC, got %q", buf)
	}
	select {
	case <-ev.esc:
		// ok
	default:
		t.Fatal("expected ev.esc to fire")
	}
	select {
	case <-ev.line:
		t.Fatal("ESC must not emit a line")
	default:
	}
}

func TestProcessKey_CtrlCEmitsSignalAndClearsBuffer(t *testing.T) {
	restore := captureStdout(t)
	defer restore()
	ev := newInputEvents()
	var buf []byte
	for _, c := range []byte("draft") {
		buf = processKey(c, buf, ev)
	}
	buf = processKey(0x03, buf, ev) // Ctrl+C
	if buf != nil {
		t.Fatalf("buffer should be nil after Ctrl+C, got %q", buf)
	}
	select {
	case <-ev.ctrlC:
		// ok
	default:
		t.Fatal("expected ev.ctrlC to fire")
	}
}

func TestProcessKey_BackspaceRemovesLastChar(t *testing.T) {
	restore := captureStdout(t)
	defer restore()
	ev := newInputEvents()
	var buf []byte
	for _, c := range []byte("abc") {
		buf = processKey(c, buf, ev)
	}
	buf = processKey(0x7f, buf, ev) // backspace
	if string(buf) != "ab" {
		t.Fatalf("expected \"ab\" after backspace, got %q", buf)
	}
	// backspace on empty buffer is a no-op
	buf = nil
	buf = processKey(0x7f, buf, ev)
	if len(buf) != 0 {
		t.Fatalf("expected empty buffer, got %q", buf)
	}
}

func TestProcessKey_NonPrintableIgnored(t *testing.T) {
	restore := captureStdout(t)
	defer restore()
	ev := newInputEvents()
	var buf []byte
	// Other control chars (not Enter/Backspace/Ctrl+C) are dropped silently.
	for _, b := range []byte{0x01, 0x02, 0x1f, 0x80} {
		buf = processKey(b, buf, ev)
	}
	if len(buf) != 0 {
		t.Fatalf("expected empty buffer, got %q", buf)
	}
}

func TestProcessKey_FullRoundTrip(t *testing.T) {
	// Simulate typing "abc", backspace, "d", Enter. Expect line "abd".
	restore := captureStdout(t)
	defer restore()
	ev := newInputEvents()
	var buf []byte
	for _, c := range []byte("abc") {
		buf = processKey(c, buf, ev)
	}
	buf = processKey(0x7f, buf, ev)
	buf = processKey('d', buf, ev)
	buf = processKey('\r', buf, ev)
	select {
	case got := <-ev.line:
		if got != "abd" {
			t.Fatalf("expected \"abd\", got %q", got)
		}
	default:
		t.Fatal("expected line")
	}
}

func TestSend_NonBlockingWhenFull(t *testing.T) {
	ch := make(chan struct{}, 1)
	send(ch) // first send lands in buffer
	send(ch) // second send drops (buffer full)
	select {
	case <-ch:
	default:
		t.Fatal("expected first send to land")
	}
}

func TestEnableRawInput_NonTTYUsesCookedReader(t *testing.T) {
	write, closeIt := captureStdin(t)
	defer closeIt()
	ev := newInputEvents()
	restore := enableRawInput(ev)
	defer restore()

	write([]byte("hello\n"))
	select {
	case got := <-ev.line:
		if got != "hello" {
			t.Fatalf("expected \"hello\", got %q", got)
		}
	case <-ev.done:
		t.Fatal("done fired before EOF")
	}

	closeIt() // close writer → cooked scanner hits EOF → reader exits
	<-ev.done
}

func TestEnableRawInput_NonTTYEscCtrlCIgnored(t *testing.T) {
	// In cooked (non-TTY) mode ESC and Ctrl+C are not delivered — only
	// whole lines are forwarded.
	write, closeIt := captureStdin(t)
	defer closeIt()
	ev := newInputEvents()
	restore := enableRawInput(ev)
	defer restore()

	write([]byte("/quit\n"))
	select {
	case got := <-ev.line:
		if got != "/quit" {
			t.Fatalf("expected \"/quit\", got %q", got)
		}
	case <-ev.esc:
		t.Fatal("esc should not fire in cooked mode")
	case <-ev.ctrlC:
		t.Fatal("ctrlC should not fire in cooked mode")
	}

	closeIt()
}

func TestClearLine_NoopOnEmpty(t *testing.T) {
	restore := captureStdout(t)
	defer restore()
	clearLine(nil)
	clearLine([]byte{})
	captured := restore()
	if len(captured) != 0 {
		t.Fatalf("expected no output, got %q", captured)
	}
}

func TestClearLine_ErasesEachChar(t *testing.T) {
	restore := captureStdout(t)
	defer restore()
	clearLine([]byte("abc"))
	captured := restore()
	// 3 chars × 3 bytes (BS, space, BS) = 9 bytes
	if len(captured) != 9 {
		t.Fatalf("expected 9 bytes, got %d (%q)", len(captured), captured)
	}
}
