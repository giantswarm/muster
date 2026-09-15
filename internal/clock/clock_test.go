package clock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// resetForTest puts the clock back on the system time. The offset is
// process-wide, so tests that advance it run sequentially and clean up.
func resetForTest(t *testing.T) {
	t.Helper()
	offset.Store(0)
	t.Cleanup(func() { offset.Store(0) })
}

func TestNowFollowsTheSystemTimePlusTheOffset(t *testing.T) {
	resetForTest(t)
	before := time.Now()
	require.WithinDuration(t, before, Now(), 50*time.Millisecond)
	require.Zero(t, Offset())

	total, err := Advance(31 * time.Minute)
	require.NoError(t, err)
	require.Equal(t, 31*time.Minute, total)
	require.Equal(t, 31*time.Minute, Offset())
	require.WithinDuration(t, time.Now().Add(31*time.Minute), Now(), 50*time.Millisecond)

	// Since and Until measure on the advanced clock.
	require.InDelta(t, (31 * time.Minute).Seconds(), Since(before).Seconds(), 1)
	require.InDelta(t, (-31 * time.Minute).Seconds(), Until(before).Seconds(), 1)

	// The offset accumulates.
	total, err = Advance(time.Minute)
	require.NoError(t, err)
	require.Equal(t, 32*time.Minute, total)
}

func TestAdvanceRefusesToMoveBackwards(t *testing.T) {
	resetForTest(t)
	_, err := Advance(-time.Second)
	require.ErrorContains(t, err, "cannot move backwards")
	require.Zero(t, Offset())
}

func TestNewTickerRefusesANonPositiveInterval(t *testing.T) {
	require.Panics(t, func() { NewTicker(0) })
}

// TestTickerFiresWhenTheClockIsAdvancedPastItsNextTick: a ticker due in an
// hour fires at once when the clock moves past that hour, once, and its
// following tick is an hour after the advance.
func TestTickerFiresWhenTheClockIsAdvancedPastItsNextTick(t *testing.T) {
	resetForTest(t)
	ticker := NewTicker(time.Hour)
	defer ticker.Stop()

	select {
	case <-ticker.C:
		t.Fatal("a ticker due in an hour must not tick before the clock moved")
	case <-time.After(50 * time.Millisecond):
	}

	_, err := Advance(3 * time.Hour)
	require.NoError(t, err)
	select {
	case tick := <-ticker.C:
		require.WithinDuration(t, Now(), tick, time.Second)
	case <-time.After(5 * time.Second):
		t.Fatal("the ticker did not fire after the clock passed its next tick")
	}

	// Three hours skipped yield one tick, not three; the next is an hour out.
	select {
	case <-ticker.C:
		t.Fatal("skipped intervals must not be delivered as extra ticks")
	case <-time.After(50 * time.Millisecond):
	}
	_, err = Advance(30 * time.Minute)
	require.NoError(t, err)
	select {
	case <-ticker.C:
		t.Fatal("half an interval after the last tick nothing is due")
	case <-time.After(50 * time.Millisecond):
	}
	_, err = Advance(31 * time.Minute)
	require.NoError(t, err)
	select {
	case <-ticker.C:
	case <-time.After(5 * time.Second):
		t.Fatal("the ticker did not fire after a full interval on the clock")
	}
}

// TestTickerFiresOnTheSystemTime: without an advance the ticker is a
// time.Ticker.
func TestTickerFiresOnTheSystemTime(t *testing.T) {
	resetForTest(t)
	ticker := NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for i := 0; i < 3; i++ {
		select {
		case <-ticker.C:
		case <-time.After(5 * time.Second):
			t.Fatalf("tick %d did not arrive on the system time", i+1)
		}
	}
}

func TestStoppedTickerIsForgotten(t *testing.T) {
	resetForTest(t)
	ticker := NewTicker(time.Hour)
	ticker.Stop()
	ticker.Stop() // idempotent
	tickers.mu.Lock()
	_, live := tickers.m[ticker]
	tickers.mu.Unlock()
	require.False(t, live)
	_, err := Advance(2 * time.Hour)
	require.NoError(t, err)
	select {
	case <-ticker.C:
		t.Fatal("a stopped ticker must not tick")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestStartControlWithoutTheVariableIsANoop(t *testing.T) {
	t.Setenv(EnvControlSocket, "")
	stop, err := StartControl()
	require.NoError(t, err)
	stop()
}

// TestControlSocketAdvancesTheClock: the endpoint the variable selects
// answers advance and offset requests and refuses the rest; the socket file
// is removed when the control stops.
func TestControlSocketAdvancesTheClock(t *testing.T) {
	resetForTest(t)
	sock := filepath.Join(shortTempDir(t), "clock.sock")
	t.Setenv(EnvControlSocket, sock)
	stop, err := StartControl()
	require.NoError(t, err)

	offsetBefore, err := RemoteOffset(sock)
	require.NoError(t, err)
	require.Zero(t, offsetBefore)

	total, err := RemoteAdvance(sock, 2*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, total)
	require.Equal(t, 2*time.Minute, Offset())

	total, err = RemoteAdvance(sock, 30*time.Second)
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute+30*time.Second, total)

	_, err = RemoteAdvance(sock, -time.Second)
	require.ErrorContains(t, err, "cannot move backwards")

	_, err = request(sock, "rewind 5m")
	require.ErrorContains(t, err, "unknown command")
	_, err = request(sock, "advance soon")
	require.ErrorContains(t, err, "invalid duration")

	stop()
	_, statErr := os.Stat(sock)
	require.True(t, os.IsNotExist(statErr), "the socket file must be removed when the control stops")
	_, err = RemoteOffset(sock)
	require.Error(t, err, "nothing serves the socket after stop")
}

func TestStartControlReplacesAStaleSocketFile(t *testing.T) {
	resetForTest(t)
	sock := filepath.Join(shortTempDir(t), "clock.sock")
	require.NoError(t, os.WriteFile(sock, nil, 0o600))
	t.Setenv(EnvControlSocket, sock)
	stop, err := StartControl()
	require.NoError(t, err)
	defer stop()
	_, err = RemoteOffset(sock)
	require.NoError(t, err)
}

func TestStartControlFailsOnAnUnusablePath(t *testing.T) {
	t.Setenv(EnvControlSocket, filepath.Join(shortTempDir(t), "missing-dir", "clock.sock"))
	_, err := StartControl()
	require.ErrorContains(t, err, EnvControlSocket)
}

func TestRespond(t *testing.T) {
	resetForTest(t)
	require.Equal(t, "ok 0s", respond("offset"))
	require.Equal(t, "ok 1m0s", respond("advance 1m"))
	require.Equal(t, "ok 1m0s", respond("offset"))
	require.True(t, strings.HasPrefix(respond("advance"), "error: invalid duration"))
	require.True(t, strings.HasPrefix(respond(""), "error: unknown command"))
}

// shortTempDir returns a temporary directory short enough for a Unix socket
// path (t.TempDir can exceed the limit under a long test name).
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "clk")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
