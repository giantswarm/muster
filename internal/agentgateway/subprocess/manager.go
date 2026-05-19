package subprocess

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sort"
	"sync"
	"syscall"
	"time"
)

// ErrAlreadyRunning is returned by Start when a process is already
// being supervised.
var ErrAlreadyRunning = errors.New("subprocess: already running")

// Manager owns one external child process: start, supervise, signal,
// and graceful shutdown with a SIGKILL fallback.
//
// A Manager value is single-shot in the sense that Stop is terminal —
// after Stop returns the Manager refuses further Start calls. Create a
// new Manager to spawn another process.
type Manager struct {
	logger *slog.Logger
	opts   options

	mu      sync.Mutex
	state   state
	cmd     *exec.Cmd
	pgid    int
	cancel  context.CancelFunc
	done    chan struct{}
	readyCh chan error
	// spawned is closed exactly once after the supervisor has either
	// recorded the first successful cmd.Start() (m.pgid set) or given
	// up before any spawn. shutdown waits on it before reading m.pgid
	// so SIGTERM never races a still-pending assignment.
	spawned     chan struct{}
	spawnedOnce sync.Once
}

type state int

const (
	stateIdle state = iota
	stateStarting
	stateRunning
	stateStopped
)

// New constructs a Manager. logger captures stdout/stderr lines from
// the supervised process; pass slog.Default() if no specific routing is
// needed.
func New(logger *slog.Logger, opts ...Option) (*Manager, error) {
	if logger == nil {
		return nil, fmt.Errorf("logger must not be nil")
	}
	o := defaultOptions()
	for _, fn := range opts {
		if err := fn(&o); err != nil {
			return nil, err
		}
	}
	return &Manager{
		logger: logger,
		opts:   o,
	}, nil
}

// Start spawns binaryPath with args and env, then blocks until either
// readyProbe succeeds, ctx is cancelled, or the configured startup
// timeout expires.
//
// readyProbe may be nil — in that case Start returns as soon as the
// process is spawned successfully.
//
// After Start returns nil the Manager owns the process; callers
// release it with Stop.
func (m *Manager) Start(
	ctx context.Context,
	binaryPath string,
	args []string,
	env map[string]string,
	readyProbe func(context.Context) error,
) error {
	m.mu.Lock()
	switch m.state {
	case stateStarting, stateRunning:
		m.mu.Unlock()
		return ErrAlreadyRunning
	case stateStopped:
		m.mu.Unlock()
		return fmt.Errorf("subprocess: manager already stopped")
	}
	m.state = stateStarting
	supCtx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})
	m.readyCh = make(chan error, 1)
	m.spawned = make(chan struct{})
	m.spawnedOnce = sync.Once{}
	m.mu.Unlock()

	envSlice := envToSlice(env)
	go m.supervise(supCtx, binaryPath, args, envSlice, readyProbe)

	// Wait for readiness, supervisor exit, ctx cancellation, or timeout.
	startupCtx, startupCancel := context.WithTimeout(ctx, m.opts.startupTimeout)
	defer startupCancel()

	select {
	case err := <-m.readyCh:
		if err != nil {
			// Honor the caller's cancellation on the shutdown leg too —
			// dropping ctx and using context.Background() would let a
			// stuck child keep the failing Start blocked past the
			// caller's lifetime.
			_ = m.shutdown(ctx)
			return err
		}
		m.mu.Lock()
		if m.state == stateStarting {
			m.state = stateRunning
		}
		m.mu.Unlock()
		return nil
	case <-m.done:
		// Supervisor exited before signalling readiness.
		m.mu.Lock()
		m.state = stateStopped
		m.mu.Unlock()
		return fmt.Errorf("subprocess: supervisor exited before ready")
	case <-startupCtx.Done():
		// startupCtx is derived from ctx, so its Done() already implies
		// ctx is cancelled or the startup timeout fired. Either way pass
		// ctx (which may still be live if only the timeout fired) so
		// shutdown respects further caller cancellation.
		_ = m.shutdown(ctx)
		return fmt.Errorf("subprocess: startup: %w", startupCtx.Err())
	}
}

// Stop signals the supervised process with SIGTERM and waits up to the
// configured drain timeout for it to exit; if the timeout expires the
// process group is killed with SIGKILL. Stop is idempotent and safe to
// call from multiple goroutines.
func (m *Manager) Stop(ctx context.Context) error {
	return m.shutdown(ctx)
}

// PID returns the supervised process's pid while it is running, or 0
// if the manager has no live child (idle, between restarts, or
// stopped). The supervised process is its own group leader, so the pid
// equals the group id used by Stop / Reload.
func (m *Manager) PID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pgid
}

func (m *Manager) shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.state == stateIdle || m.state == stateStopped {
		m.state = stateStopped
		m.mu.Unlock()
		return nil
	}
	done := m.done
	cancel := m.cancel
	spawned := m.spawned
	m.state = stateStopped
	m.mu.Unlock()

	// Cancel supervisor first so it stops restarting on us.
	if cancel != nil {
		cancel()
	}

	// Wait for the supervisor to either record the spawned pgid or
	// give up before any spawn — otherwise SIGTERM might race the
	// assignment in supervise() and miss a live child.
	if spawned != nil {
		select {
		case <-spawned:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	m.mu.Lock()
	pgid := m.pgid
	m.mu.Unlock()

	if pgid > 0 {
		if err := signalProcessGroup(pgid, syscall.SIGTERM); err != nil {
			m.logger.Warn("subprocess: SIGTERM failed", slog.Int("pgid", pgid), slog.String("err", err.Error()))
		}
	}

	drain := time.NewTimer(m.opts.drainTimeout)
	defer drain.Stop()

	select {
	case <-done:
		return nil
	case <-drain.C:
		if pgid > 0 {
			m.logger.Warn("subprocess: drain timeout, escalating to SIGKILL",
				slog.Int("pgid", pgid),
				slog.Duration("drain_timeout", m.opts.drainTimeout))
			if err := signalProcessGroup(pgid, syscall.SIGKILL); err != nil {
				m.logger.Warn("subprocess: SIGKILL failed", slog.String("err", err.Error()))
			}
		}
	case <-ctx.Done():
		if pgid > 0 {
			_ = signalProcessGroup(pgid, syscall.SIGKILL)
		}
		<-done
		return ctx.Err()
	}

	// Wait unconditionally for the supervisor to exit.
	<-done
	return nil
}

func (m *Manager) supervise(
	ctx context.Context,
	binaryPath string,
	args []string,
	env []string,
	readyProbe func(context.Context) error,
) {
	defer close(m.done)
	// Guarantees shutdown can never block on spawned even if we exit
	// before recording a pgid (e.g. first cmd.Start failed).
	defer m.markSpawned()

	backoff := m.opts.backoffInitial
	attempt := 0
	readySignalled := false

	for {
		if ctx.Err() != nil {
			return
		}

		cmd := exec.Command(binaryPath, args...) //nolint:gosec // binary path is caller-supplied; not user input
		cmd.Env = env
		configureProcAttr(cmd)

		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			m.signalReadyOnce(&readySignalled, fmt.Errorf("stdout pipe: %w", err))
			return
		}
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			_ = stdoutPipe.Close()
			m.signalReadyOnce(&readySignalled, fmt.Errorf("stderr pipe: %w", err))
			return
		}

		if err := cmd.Start(); err != nil {
			_ = stdoutPipe.Close()
			_ = stderrPipe.Close()
			if !readySignalled {
				m.signalReadyOnce(&readySignalled, fmt.Errorf("start %s: %w", binaryPath, err))
				return
			}
			m.logger.Error("subprocess: start failed", slog.String("err", err.Error()))
			if !m.waitBackoff(ctx, &attempt, &backoff) {
				return
			}
			continue
		}

		pid := cmd.Process.Pid
		m.mu.Lock()
		m.cmd = cmd
		m.pgid = pid
		shuttingDown := ctx.Err() != nil
		m.mu.Unlock()
		m.markSpawned()

		// If Stop fired between cmd.Start returning and this assignment,
		// the parent shutdown saw pgid=0 and didn't signal. Self-signal
		// to unblock cmd.Wait.
		if shuttingDown {
			_ = signalProcessGroup(pid, syscall.SIGTERM)
		}

		m.logger.Info("subprocess: started",
			slog.String("binary", binaryPath),
			slog.Int("pid", pid),
			slog.Int("attempt", attempt))

		var pumpWG sync.WaitGroup
		pumpWG.Add(2)
		go m.pumpLines(ctx, &pumpWG, stdoutPipe, slog.LevelInfo, "stdout", pid)
		go m.pumpLines(ctx, &pumpWG, stderrPipe, slog.LevelWarn, "stderr", pid)

		probeCtx, probeCancel := context.WithCancel(ctx)
		probeDone := make(chan struct{})
		go func() {
			defer close(probeDone)
			if readyProbe == nil {
				if !readySignalled {
					m.signalReadyOnce(&readySignalled, nil)
				}
				return
			}
			if err := readyProbe(probeCtx); err != nil {
				if !readySignalled && probeCtx.Err() == nil {
					m.signalReadyOnce(&readySignalled, fmt.Errorf("ready probe: %w", err))
				}
				return
			}
			if !readySignalled {
				m.signalReadyOnce(&readySignalled, nil)
			}
		}()

		waitErr := cmd.Wait()
		probeCancel()
		<-probeDone
		pumpWG.Wait()

		m.mu.Lock()
		m.cmd = nil
		m.pgid = 0
		m.mu.Unlock()

		if ctx.Err() != nil {
			// Stop was called.
			return
		}

		if waitErr != nil {
			m.logger.Error("subprocess: exited unexpectedly",
				slog.Int("pid", pid),
				slog.String("err", waitErr.Error()))
		} else {
			m.logger.Warn("subprocess: exited unexpectedly with zero status",
				slog.Int("pid", pid))
		}

		if !readySignalled {
			// Never became ready on the very first attempt — surface
			// the failure to Start instead of looping invisibly.
			m.signalReadyOnce(&readySignalled, fmt.Errorf("process exited before ready: %w", waitErr))
			return
		}

		if !m.waitBackoff(ctx, &attempt, &backoff) {
			return
		}
	}
}

func (m *Manager) markSpawned() {
	m.spawnedOnce.Do(func() { close(m.spawned) })
}

func (m *Manager) signalReadyOnce(signalled *bool, err error) {
	if *signalled {
		return
	}
	*signalled = true
	select {
	case m.readyCh <- err:
	default:
	}
}

func (m *Manager) waitBackoff(ctx context.Context, attempt *int, backoff *time.Duration) bool {
	*attempt++
	if m.opts.maxRestarts >= 0 && *attempt > m.opts.maxRestarts {
		m.logger.Error("subprocess: max restarts exceeded",
			slog.Int("attempt", *attempt),
			slog.Int("max_restarts", m.opts.maxRestarts))
		return false
	}
	delay := *backoff
	m.logger.Info("subprocess: scheduling restart",
		slog.Int("attempt", *attempt),
		slog.Duration("delay", delay))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	*backoff = min(*backoff*2, m.opts.backoffMax)
	return true
}

func (m *Manager) pumpLines(ctx context.Context, wg *sync.WaitGroup, r io.ReadCloser, level slog.Level, stream string, pid int) {
	defer wg.Done()
	defer func() { _ = r.Close() }()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		m.logger.Log(ctx, level, "subprocess: "+stream,
			slog.Int("pid", pid),
			slog.String("line", scanner.Text()))
	}
}

// envToSlice flattens env into the KEY=VALUE form exec.Cmd.Env expects.
// Keys are sorted purely for test stability (process env ordering has no
// semantic meaning to POSIX); the sort lets golden tests pin the slice.
func envToSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}
