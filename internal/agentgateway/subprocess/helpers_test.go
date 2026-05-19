//go:build !windows

package subprocess

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// captureLogger returns a slog.Logger that writes JSON lines into a
// thread-safe buffer the test can read back.
func captureLogger(t *testing.T) (*slog.Logger, *syncBuffer) {
	t.Helper()
	buf := &syncBuffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// fakeEnv builds the env map a Manager passes to its child so the
// child enters runFakeBinary.
func fakeEnv(extra map[string]string) map[string]string {
	env := map[string]string{
		"PATH": os.Getenv("PATH"),
		"HOME": os.Getenv("HOME"),
	}
	maps.Copy(env, extra)
	return env
}

// fileReadyProbe returns a readiness probe that waits for path to exist.
func fileReadyProbe(path string) func(context.Context) error {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(path); err == nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
}

// pidExists returns true iff signal-0 to pid succeeds. Used to assert a
// process has actually been reaped after Stop.
func pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// readLines slices a file by newline; returns empty slice if missing.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test helper; path from t.TempDir
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		require.NoError(t, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	if len(data) == 0 {
		return nil
	}
	out := []string{}
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, string(data[start:i]))
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, string(data[start:]))
	}
	return out
}

// tmpFile returns an absolute path inside t.TempDir for the given name.
func tmpFile(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}
