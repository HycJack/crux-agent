package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Process manages a single plugin subprocess and its JSON-RPC
// communication over stdin/stdout.
//
// Concurrency model:
//   - Call/Notify are safe to call from multiple goroutines.
//   - Pending requests are tracked in a map keyed by request id.
//   - readLoop runs in its own goroutine, demuxing response/notification.
//   - stderr is logged (never written to stdout — that's JSON-RPC).
type Process struct {
	manifest *Manifest
	logger   *slog.Logger

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	stderr io.ReadCloser

	mu       sync.Mutex
	nextID   atomic.Int64
	pending  map[int]chan *Response
	onNotify func(Notification)
	running  bool
	cancelFn context.CancelFunc
}

// NewProcess creates a new plugin process from a manifest.
func NewProcess(m *Manifest, logger *slog.Logger) *Process {
	if logger == nil {
		logger = slog.Default()
	}
	return &Process{
		manifest: m,
		logger:   logger.With("plugin", m.ID),
		pending:  make(map[int]chan *Response),
	}
}

// SetNotifyHandler sets the callback for notifications (no-id frames)
// received from the plugin.
func (p *Process) SetNotifyHandler(fn func(Notification)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onNotify = fn
}

// Start launches the plugin subprocess. Must be called before Call/Notify.
func (p *Process) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	childCtx, cancel := context.WithCancel(ctx)
	p.cancelFn = cancel

	parts := strings.Fields(p.manifest.Command)
	if len(parts) == 0 {
		cancel()
		return fmt.Errorf("plugin %s: empty command", p.manifest.ID)
	}

	cmd := exec.CommandContext(childCtx, parts[0], parts[1:]...)
	cmd.Dir = p.manifest.Dir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("plugin %s: stdin pipe: %w", p.manifest.ID, err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("plugin %s: stdout pipe: %w", p.manifest.ID, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("plugin %s: stderr pipe: %w", p.manifest.ID, err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("plugin %s: start: %w", p.manifest.ID, err)
	}

	p.cmd = cmd
	p.stdin = stdin
	p.stdout = bufio.NewScanner(stdoutPipe)
	p.stdout.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB max line
	p.stderr = stderrPipe
	p.running = true

	go p.readLoop()
	go p.logStderr()

	p.logger.Info("plugin process started", "pid", cmd.Process.Pid, "command", p.manifest.Command)
	return nil
}

// IsRunning reports whether the subprocess is alive.
func (p *Process) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// Manifest returns the plugin manifest.
func (p *Process) Manifest() *Manifest {
	return p.manifest
}

// Call sends a JSON-RPC request and blocks waiting for the response.
// Returns the raw "result" payload (caller unmarshals to specific struct).
func (p *Process) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := int(p.nextID.Add(1))

	req, err := NewRequest(method, params, id)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ch := make(chan *Response, 1)
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return nil, fmt.Errorf("plugin %s: not running", p.manifest.ID)
	}
	p.pending[id] = ch
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	p.mu.Lock()
	_, err = p.stdin.Write(data)
	p.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("plugin %s: write: %w", p.manifest.ID, err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// Notify sends a JSON-RPC notification (no id, no response expected).
// Used for async hooks (after_tool_call, turn_end) and plugin→host
// notifications (message.inbound, chat.send).
func (p *Process) Notify(method string, params interface{}) error {
	n, err := NewNotification(method, params)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running {
		return fmt.Errorf("plugin %s: not running", p.manifest.ID)
	}
	if _, err := p.stdin.Write(data); err != nil {
		return fmt.Errorf("plugin %s: notify write: %w", p.manifest.ID, err)
	}
	return nil
}

// Stop gracefully shuts down the plugin: sends shutdown RPC, waits up to
// the given timeout, then kills if still alive.
func (p *Process) Stop(timeout time.Duration) {
	// Capture state under lock, then release so we don't deadlock with Call.
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	wasRunning := p.running
	stdin := p.stdin
	cancelFn := p.cancelFn
	cmd := p.cmd
	p.running = false
	p.mu.Unlock()

	// Best-effort shutdown RPC. Call() needs the lock, which we've released.
	if wasRunning && stdin != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_, _ = p.Call(ctx, MethodShutdown, nil)
		cancel()
	}

	// Cancel context → exec.CommandContext will SIGKILL the subprocess.
	if cancelFn != nil {
		cancelFn()
	}

	// Wait for process to exit (exec.CommandContext kills it via cancelFn).
	if cmd != nil && cmd.Process != nil {
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(timeout):
			_ = cmd.Process.Kill()
			<-done
		}
	}

	p.logger.Info("plugin process stopped")
}

// readLoop demuxes stdout frames into Response (matches pending id) or
// Notification (no id, dispatched to onNotify).
func (p *Process) readLoop() {
	for p.stdout.Scan() {
		line := p.stdout.Bytes()
		if len(line) == 0 {
			continue
		}

		// Peek at id field presence to distinguish response vs notification.
		// Cheaper than unmarshaling twice.
		var peek struct {
			ID      *int  `json:"id"`
			JSONRPC string `json:"jsonrpc"`
		}
		if err := json.Unmarshal(line, &peek); err != nil {
			p.logger.Warn("invalid JSON from plugin", "err", err, "line", truncate(string(line), 200))
			continue
		}

		if peek.ID == nil {
			// Notification.
			var n Notification
			if err := json.Unmarshal(line, &n); err != nil {
				p.logger.Warn("parse notification failed", "err", err)
				continue
			}
			p.mu.Lock()
			handler := p.onNotify
			p.mu.Unlock()
			if handler != nil {
				handler(n)
			}
			continue
		}

		// Response.
		var r Response
		if err := json.Unmarshal(line, &r); err != nil {
			p.logger.Warn("parse response failed", "err", err)
			continue
		}
		p.mu.Lock()
		ch, ok := p.pending[r.ID]
		p.mu.Unlock()
		if ok {
			ch <- &r
		} else {
			p.logger.Warn("response for unknown id", "id", r.ID)
		}
	}

	if err := p.stdout.Err(); err != nil {
		p.logger.Warn("stdout read ended with error", "err", err)
	} else {
		p.logger.Info("plugin stdout closed")
	}
}

// logStderr forwards plugin stderr to the host logger at Info level.
func (p *Process) logStderr() {
	scanner := bufio.NewScanner(p.stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for scanner.Scan() {
		p.logger.Info("plugin stderr", "msg", scanner.Text())
	}
}

// truncate keeps log lines bounded.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}