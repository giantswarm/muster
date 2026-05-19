//go:build !windows

package subprocess

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_Start_Stop_HappyPath(t *testing.T) {
	logger, logs := captureLogger(t)
	readyPath := tmpFile(t, "ready")

	mgr := newManager(t, logger,
		WithDrainTimeout(2*time.Second),
		WithStartupTimeout(5*time.Second),
	)

	env := fakeEnv(map[string]string{
		envFakeMode:      fakeModeNormal,
		envFakeReadyFile: readyPath,
	})

	require.NoError(t, mgr.Start(t.Context(), os.Args[0], nil, env, fileReadyProbe(readyPath)))

	pid := managerPID(mgr)
	require.Greater(t, pid, 0, "child pid must be set after Start")
	require.True(t, pidExists(pid), "child must be alive after Start returns")

	require.NoError(t, mgr.Stop(t.Context()))
	require.Eventually(t, func() bool { return !pidExists(pid) },
		2*time.Second, 20*time.Millisecond, "child must be reaped after Stop")

	assert.Contains(t, logs.String(), `"msg":"subprocess: started"`)
}

func TestManager_Start_BinaryMissing(t *testing.T) {
	logger, _ := captureLogger(t)
	mgr := newManager(t, logger,
		WithStartupTimeout(1*time.Second),
		WithBackoff(50*time.Millisecond, 50*time.Millisecond),
	)

	err := mgr.Start(t.Context(), "/no/such/binary", nil, fakeEnv(nil), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "/no/such/binary")
}

func TestManager_Stop_BeforeStart(t *testing.T) {
	logger, _ := captureLogger(t)
	mgr, err := New(logger)
	require.NoError(t, err)

	require.NoError(t, mgr.Stop(t.Context()))
	// Stop is terminal — subsequent Start fails.
	err = mgr.Start(t.Context(), os.Args[0], nil,
		fakeEnv(map[string]string{envFakeMode: fakeModeNormal}), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already stopped")
}

func TestManager_Stop_Idempotent(t *testing.T) {
	logger, _ := captureLogger(t)
	readyPath := tmpFile(t, "ready")

	mgr := newManager(t, logger,
		WithDrainTimeout(1*time.Second),
		WithStartupTimeout(2*time.Second),
	)

	env := fakeEnv(map[string]string{
		envFakeMode:      fakeModeNormal,
		envFakeReadyFile: readyPath,
	})
	require.NoError(t, mgr.Start(t.Context(), os.Args[0], nil, env, fileReadyProbe(readyPath)))
	require.NoError(t, mgr.Stop(t.Context()))
	require.NoError(t, mgr.Stop(t.Context()))
}

func TestManager_Start_Concurrent(t *testing.T) {
	logger, _ := captureLogger(t)
	readyPath := tmpFile(t, "ready")

	mgr := newManager(t, logger,
		WithDrainTimeout(1*time.Second),
		WithStartupTimeout(3*time.Second),
	)
	t.Cleanup(func() { _ = mgr.Stop(context.Background()) })

	env := fakeEnv(map[string]string{
		envFakeMode:      fakeModeNormal,
		envFakeReadyFile: readyPath,
	})

	const N = 8
	var wg sync.WaitGroup
	var winners, alreadyRunning atomic.Int32
	errs := make(chan error, N)
	for range N {
		wg.Go(func() {
			err := mgr.Start(t.Context(), os.Args[0], nil, env, fileReadyProbe(readyPath))
			switch {
			case err == nil:
				winners.Add(1)
			case errors.Is(err, ErrAlreadyRunning):
				alreadyRunning.Add(1)
			default:
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("unexpected error: %v", e)
	}
	require.Equal(t, int32(1), winners.Load(), "exactly one Start must win")
	require.Equal(t, int32(N-1), alreadyRunning.Load(), "rest must observe ErrAlreadyRunning")
}

func TestManager_Restart_OnCrash_WithBackoff(t *testing.T) {
	logger, _ := captureLogger(t)
	startFile := tmpFile(t, "starts.log")

	mgr := newManager(t, logger,
		WithDrainTimeout(1*time.Second),
		WithStartupTimeout(3*time.Second),
		WithBackoff(50*time.Millisecond, 200*time.Millisecond),
		WithMaxRestarts(3),
	)

	env := fakeEnv(map[string]string{
		envFakeMode:      fakeModeCrash,
		envFakeStartFile: startFile,
		envFakeRunFor:    "30",
		envFakeExitCode:  "7",
	})
	require.NoError(t, mgr.Start(t.Context(), os.Args[0], nil, env, nil))
	t.Cleanup(func() { _ = mgr.Stop(context.Background()) })

	// MaxRestarts=3 → 1 initial + 3 restarts = 4 invocations.
	require.Eventually(t, func() bool {
		return len(readLines(t, startFile)) >= 4
	}, 5*time.Second, 50*time.Millisecond)

	lines := readLines(t, startFile)
	require.GreaterOrEqual(t, len(lines), 4)

	gaps := make([]time.Duration, 0, len(lines)-1)
	for i := 1; i < len(lines); i++ {
		a, _ := strconv.ParseInt(lines[i-1], 10, 64)
		b, _ := strconv.ParseInt(lines[i], 10, 64)
		gaps = append(gaps, time.Duration(b-a))
	}
	// Backoff must monotonically grow (within slack) up to the cap.
	require.LessOrEqual(t, gaps[0], gaps[1]+25*time.Millisecond, "backoff must not shrink")
	require.LessOrEqual(t, gaps[1], gaps[2]+25*time.Millisecond, "backoff must not shrink")
	// Last gap respects the 200ms cap: child run ~30ms + cap 200ms + scheduling slack.
	require.Less(t, gaps[len(gaps)-1], 600*time.Millisecond, "backoff must respect cap")
}

func TestManager_Stop_DuringStartup(t *testing.T) {
	logger, _ := captureLogger(t)
	readyPath := tmpFile(t, "ready")

	mgr := newManager(t, logger,
		WithDrainTimeout(2*time.Second),
		WithStartupTimeout(5*time.Second),
	)

	env := fakeEnv(map[string]string{
		envFakeMode:      fakeModeNormal,
		envFakeReadyFile: readyPath,
		envFakeReadyWait: "10000", // child sleeps 10s before touching ready file
	})

	startErr := make(chan error, 1)
	go func() {
		startErr <- mgr.Start(t.Context(), os.Args[0], nil, env, fileReadyProbe(readyPath))
	}()

	require.Eventually(t, func() bool { return managerPID(mgr) > 0 },
		2*time.Second, 20*time.Millisecond)

	require.NoError(t, mgr.Stop(t.Context()))

	select {
	case err := <-startErr:
		require.Error(t, err, "Start must surface a startup failure when Stop wins")
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
}

func TestManager_Stop_SIGKILLFallback(t *testing.T) {
	logger, logs := captureLogger(t)
	readyPath := tmpFile(t, "ready")
	signalLog := tmpFile(t, "signals.log")

	mgr := newManager(t, logger,
		WithDrainTimeout(200*time.Millisecond),
		WithStartupTimeout(3*time.Second),
	)

	env := fakeEnv(map[string]string{
		envFakeMode:      fakeModeNormal + ",ignore_term,record_signals",
		envFakeReadyFile: readyPath,
		envFakeSignalLog: signalLog,
	})
	require.NoError(t, mgr.Start(t.Context(), os.Args[0], nil, env, fileReadyProbe(readyPath)))

	pid := managerPID(mgr)
	require.NoError(t, mgr.Stop(t.Context()))
	require.Eventually(t, func() bool { return !pidExists(pid) },
		3*time.Second, 50*time.Millisecond)
	assert.Contains(t, logs.String(), "escalating to SIGKILL")
}

func TestManager_CapturesStdoutStderr(t *testing.T) {
	logger, logs := captureLogger(t)
	readyPath := tmpFile(t, "ready")

	mgr := newManager(t, logger,
		WithDrainTimeout(2*time.Second),
		WithStartupTimeout(3*time.Second),
	)
	t.Cleanup(func() { _ = mgr.Stop(context.Background()) })

	env := fakeEnv(map[string]string{
		envFakeMode:      fakeModeNormal,
		envFakeReadyFile: readyPath,
		envFakeStdout:    "hello-from-stdout",
		envFakeStderr:    "hello-from-stderr",
	})
	require.NoError(t, mgr.Start(t.Context(), os.Args[0], nil, env, fileReadyProbe(readyPath)))

	require.Eventually(t, func() bool {
		s := logs.String()
		return strings.Contains(s, "hello-from-stdout") && strings.Contains(s, "hello-from-stderr")
	}, 2*time.Second, 20*time.Millisecond)
}

func TestNew_RejectsBadOptions(t *testing.T) {
	logger, _ := captureLogger(t)
	for _, tc := range []struct {
		name string
		opt  Option
	}{
		{"drain<=0", WithDrainTimeout(0)},
		{"startup<=0", WithStartupTimeout(-time.Second)},
		{"backoff init<=0", WithBackoff(0, time.Second)},
		{"backoff max<init", WithBackoff(time.Second, time.Millisecond)},
		{"maxRestarts<-1", WithMaxRestarts(-2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(logger, tc.opt)
			require.Error(t, err)
		})
	}
	t.Run("nil logger", func(t *testing.T) {
		_, err := New(nil)
		require.Error(t, err)
	})
}

// TestManager_StopDuringStartup exercises the race between cmd.Start
// returning and the supervisor recording m.pgid: Stop is invoked before
// the child has had time to reach readiness. The Manager must still
// reap the child rather than leak it.
func TestManager_StopDuringStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal model differs on windows")
	}
	logger, _ := captureLogger(t)
	readyPath := tmpFile(t, "ready")

	mgr := newManager(t, logger,
		WithDrainTimeout(2*time.Second),
		WithStartupTimeout(5*time.Second),
	)

	env := fakeEnv(map[string]string{
		envFakeMode:      fakeModeNormal,
		envFakeReadyFile: readyPath,
		// Block the child indefinitely before touching ready — the
		// readiness probe never completes on its own.
		envFakeReadyWait: "60000",
	})

	startErr := make(chan error, 1)
	go func() {
		startErr <- mgr.Start(t.Context(), os.Args[0], nil, env, fileReadyProbe(readyPath))
	}()

	// Give the supervisor goroutine a chance to schedule but try to
	// land Stop in the cmd.Start → pgid-assignment window.
	time.Sleep(2 * time.Millisecond)

	require.NoError(t, mgr.Stop(t.Context()))

	select {
	case err := <-startErr:
		require.Error(t, err, "Start must surface an error when Stop wins")
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after Stop")
	}

	pid := managerPID(mgr)
	if pid > 0 {
		require.Eventually(t, func() bool { return !pidExists(pid) },
			3*time.Second, 20*time.Millisecond, "child must be reaped")
	}
}

// TestManager_StopReapsGroup verifies the package-level guarantee that
// children the supervised process spawned terminate with it.
func TestManager_StopReapsGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group signalling differs on windows")
	}
	logger, _ := captureLogger(t)
	readyPath := tmpFile(t, "ready")
	childPIDFile := tmpFile(t, "child.pid")

	mgr := newManager(t, logger,
		WithDrainTimeout(2*time.Second),
		WithStartupTimeout(5*time.Second),
	)

	env := fakeEnv(map[string]string{
		envFakeMode:         fakeModeNormal + ",spawn_child",
		envFakeReadyFile:    readyPath,
		envFakeChildPIDFile: childPIDFile,
	})
	require.NoError(t, mgr.Start(t.Context(), os.Args[0], nil, env, fileReadyProbe(readyPath)))
	t.Cleanup(func() { _ = mgr.Stop(context.Background()) })

	var grandchild int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(childPIDFile) //nolint:gosec // test file in t.TempDir
		if err != nil || len(data) == 0 {
			return false
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			return false
		}
		grandchild = pid
		return pidExists(pid)
	}, 3*time.Second, 20*time.Millisecond, "grandchild must be spawned and alive")

	require.NoError(t, mgr.Stop(t.Context()))

	require.Eventually(t, func() bool { return !pidExists(grandchild) },
		3*time.Second, 20*time.Millisecond, "grandchild must be reaped with its group")
}

func newManager(t *testing.T, logger *slog.Logger, opts ...Option) *Manager {
	t.Helper()
	mgr, err := New(logger, opts...)
	require.NoError(t, err)
	return mgr
}

func managerPID(m *Manager) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pgid
}
