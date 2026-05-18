package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// errInvalidFrame is returned when a request body cannot be parsed as JSON-RPC.
var errInvalidFrame = errors.New("invalid JSON-RPC frame")

// errBridgeUnavailable is returned when Send is called on a stopped or never-
// started Bridge.
var errBridgeUnavailable = errors.New("bridge unavailable")

// BridgeOptions configures a Bridge.
type BridgeOptions struct {
	// Command is the child process command (path or PATH-resolvable name).
	Command string
	// Args are the child process arguments.
	Args []string
	// Env is appended to the parent environment when launching the child.
	Env map[string]string
	// Logger receives structured diagnostics. Required.
	Logger *slog.Logger
}

// Bridge owns the stdio MCP child process and demultiplexes JSON-RPC
// responses back to the Send calls that registered the matching id.
type Bridge struct {
	opts   BridgeOptions
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan []byte

	procDone chan struct{}
	procErr  error

	readDone chan struct{}

	state struct {
		mu      sync.Mutex
		started bool
		stopped bool
		readErr error
	}
}

// NewBridge constructs a Bridge with the given options. Start must be called
// before Send.
func NewBridge(opts BridgeOptions) *Bridge {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Bridge{
		opts:     opts,
		pending:  make(map[string]chan []byte),
		procDone: make(chan struct{}),
		readDone: make(chan struct{}),
	}
}

// Start launches the child process and starts the stdout reader goroutine.
// It returns an error if the process cannot be started; runtime failures
// surface through Send and IsHealthy.
func (b *Bridge) Start(_ context.Context) error {
	b.state.mu.Lock()
	if b.state.started {
		b.state.mu.Unlock()
		return errors.New("bridge already started")
	}
	b.state.started = true
	b.state.mu.Unlock()

	cmd := exec.Command(b.opts.Command, b.opts.Args...)
	cmd.Env = append(cmd.Environ(), envSliceFromMap(b.opts.Env)...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create child stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("create child stdout pipe: %w", err)
	}
	cmd.Stderr = stderrLogger{logger: b.opts.Logger}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("start child %q: %w", b.opts.Command, err)
	}

	b.cmd = cmd
	b.stdin = stdin
	b.stdout = stdout
	go b.readLoop()
	go func() {
		b.procErr = cmd.Wait()
		close(b.procDone)
	}()
	return nil
}

// Send writes the frame to the child's stdin and, if the frame carries a
// JSON-RPC id, waits for the matching response. For notifications (no id),
// Send returns nil, nil after the frame has been written.
func (b *Bridge) Send(ctx context.Context, frame []byte) ([]byte, error) {
	if !b.IsHealthy() {
		return nil, errBridgeUnavailable
	}

	id, hasID, err := extractID(frame)
	if err != nil {
		return nil, err
	}

	var respCh chan []byte
	var key string
	if hasID {
		key = string(id)
		respCh = make(chan []byte, 1)
		b.pendingMu.Lock()
		if _, dup := b.pending[key]; dup {
			b.pendingMu.Unlock()
			return nil, fmt.Errorf("duplicate in-flight id %s", key)
		}
		b.pending[key] = respCh
		b.pendingMu.Unlock()
	}

	if err := b.writeFrame(frame); err != nil {
		b.unregister(key)
		return nil, fmt.Errorf("write frame to child: %w", err)
	}
	if !hasID {
		return nil, nil
	}

	select {
	case resp, ok := <-respCh:
		if !ok {
			return nil, fmt.Errorf("child stream closed before response for id %s: %w", key, errBridgeUnavailable)
		}
		return resp, nil
	case <-ctx.Done():
		b.unregister(key)
		return nil, ctx.Err()
	}
}

// IsHealthy reports whether the child is running and its stdout has not
// errored out.
func (b *Bridge) IsHealthy() bool {
	select {
	case <-b.procDone:
		return false
	default:
	}
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	if !b.state.started || b.state.stopped {
		return false
	}
	return b.state.readErr == nil
}

// Stop closes the child's stdin, sends SIGTERM, waits up to timeout for the
// process to exit, and falls back to SIGKILL on expiry.
func (b *Bridge) Stop(timeout time.Duration) error {
	b.state.mu.Lock()
	if b.state.stopped {
		b.state.mu.Unlock()
		return nil
	}
	b.state.stopped = true
	cmd := b.cmd
	stdin := b.stdin
	b.state.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	if stdin != nil {
		_ = stdin.Close()
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-b.procDone:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-b.procDone
	}

	b.failPending(fmt.Errorf("child exited: %w", errBridgeUnavailable))
	<-b.readDone
	return nil
}

// Wait blocks until the child exits and returns its exit error, if any.
func (b *Bridge) Wait() error {
	<-b.procDone
	return b.procErr
}

// pendingCountForTest exposes the in-flight registration count to tests.
func (b *Bridge) pendingCountForTest() int {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	return len(b.pending)
}

func (b *Bridge) writeFrame(frame []byte) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if _, err := b.stdin.Write(frame); err != nil {
		return err
	}
	_, err := b.stdin.Write([]byte{'\n'})
	return err
}

func (b *Bridge) readLoop() {
	defer close(b.readDone)
	r := bufio.NewReaderSize(b.stdout, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if delivered := b.deliver(line); !delivered {
				b.opts.Logger.Debug("bridge: dropped unmatched frame", "len", len(line))
			}
		}
		if err != nil {
			b.state.mu.Lock()
			if !errors.Is(err, io.EOF) {
				b.state.readErr = err
			}
			b.state.mu.Unlock()
			b.failPending(fmt.Errorf("child stdout closed: %w", errBridgeUnavailable))
			return
		}
	}
}

func (b *Bridge) deliver(line []byte) bool {
	id, hasID, err := extractID(line)
	if err != nil || !hasID {
		return false
	}
	key := string(id)
	b.pendingMu.Lock()
	ch, ok := b.pending[key]
	if ok {
		delete(b.pending, key)
	}
	b.pendingMu.Unlock()
	if !ok {
		return false
	}
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}
	ch <- line
	close(ch)
	return true
}

func (b *Bridge) unregister(key string) {
	if key == "" {
		return
	}
	b.pendingMu.Lock()
	delete(b.pending, key)
	b.pendingMu.Unlock()
}

func (b *Bridge) failPending(_ error) {
	b.pendingMu.Lock()
	for k, ch := range b.pending {
		close(ch)
		delete(b.pending, k)
	}
	b.pendingMu.Unlock()
}

// extractID returns the raw bytes of the JSON-RPC id field, or hasID=false if
// the frame has no id (i.e., it is a notification).
func extractID(frame []byte) ([]byte, bool, error) {
	var probe struct {
		ID *json.RawMessage `json:"id,omitempty"`
	}
	if err := json.Unmarshal(frame, &probe); err != nil {
		return nil, false, fmt.Errorf("parse JSON-RPC frame: %w: %w", errInvalidFrame, err)
	}
	if probe.ID == nil {
		return nil, false, nil
	}
	return []byte(*probe.ID), true, nil
}

func envSliceFromMap(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// stderrLogger forwards the child's stderr line-by-line through slog.
type stderrLogger struct{ logger *slog.Logger }

func (s stderrLogger) Write(p []byte) (int, error) {
	s.logger.Info("child stderr", "msg", string(p))
	return len(p), nil
}
