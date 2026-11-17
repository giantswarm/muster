package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/services"
)

// A Start that arrives while another is in flight waits for it and returns
// its result instead of opening a second client.
func TestConcurrentStartsConnectOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &Service{}
		release := make(chan struct{})
		var calls atomic.Int32
		start := func(context.Context, context.Context) error {
			calls.Add(1)
			<-release
			return errors.New("backend refused")
		}

		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range errs {
			wg.Go(func() { errs[i] = svc.runStart(t.Context(), start) })
		}
		synctest.Wait()
		close(release)
		wg.Wait()

		require.Equal(t, int32(1), calls.Load())
		require.EqualError(t, errs[0], "backend refused")
		require.EqualError(t, errs[1], "backend refused")
	})
}

// A Stop cancels the connect of the attempt in flight with ErrStopRequested
// and holds the lifecycle only once that attempt returned.
func TestLockForStopCancelsTheConnectAndWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &Service{BaseService: services.NewBaseService("starting", services.TypeMCPServer, nil)}
		var cause error
		release := make(chan struct{})
		go func() {
			_ = svc.runStart(t.Context(), func(_, connectCtx context.Context) error {
				<-connectCtx.Done()
				cause = context.Cause(connectCtx)
				<-release
				return cause
			})
		}()
		synctest.Wait()

		locked := make(chan func())
		go func() {
			unlock, err := svc.lockForStop(t.Context())
			if err == nil {
				locked <- unlock
			}
		}()
		synctest.Wait()
		require.ErrorIs(t, cause, services.ErrStopRequested)
		select {
		case <-locked:
			t.Fatal("lockForStop returned before the attempt in flight")
		default:
		}

		close(release)
		unlock := <-locked
		unlock()
	})
}

// A start attempt that begins while a Stop is in progress does not connect:
// of a Start and a Stop that overlap, the Stop wins. A Start after the Stop
// connects again.
func TestStartDuringStopDoesNotConnect(t *testing.T) {
	svc := &Service{BaseService: services.NewBaseService("stopping", services.TypeMCPServer, nil)}
	var calls atomic.Int32
	start := func(context.Context, context.Context) error {
		calls.Add(1)
		return nil
	}

	unlock, err := svc.lockForStop(t.Context())
	require.NoError(t, err)
	started := make(chan error, 1)
	go func() { started <- svc.runStart(t.Context(), start) }()
	require.Eventually(t, func() bool {
		svc.startMutex.Lock()
		defer svc.startMutex.Unlock()
		return svc.startInFlight != nil
	}, 5*time.Second, time.Millisecond)
	unlock()

	require.ErrorIs(t, <-started, services.ErrStopRequested)
	require.Zero(t, calls.Load())

	require.NoError(t, svc.runStart(t.Context(), start))
	require.Equal(t, int32(1), calls.Load())
}

// A Stop whose context ends before the attempt in flight returns says so.
func TestLockForStopGivesUpWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &Service{BaseService: services.NewBaseService("stuck", services.TypeMCPServer, nil)}
		release := make(chan struct{})
		go func() {
			_ = svc.runStart(t.Context(), func(context.Context, context.Context) error {
				<-release
				return nil
			})
		}()
		synctest.Wait()

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		_, err := svc.lockForStop(ctx)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		close(release)
	})
}

// A Stop during a connect that hangs ends that connect, leaves no client and
// settles the server stopped; the Start returns ErrStopRequested instead of
// counting a connection failure.
func TestStopDuringConnectSettlesStopped(t *testing.T) {
	entered := make(chan struct{})
	testEnded := make(chan struct{})
	var enterOnce sync.Once
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enterOnce.Do(func() { close(entered) })
		select {
		case <-r.Context().Done():
		case <-testEnded:
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(backend.Close)
	t.Cleanup(func() { close(testEnded) })

	svc, err := NewService(&api.MCPServer{
		Name:      "connecting",
		Namespace: "muster",
		Type:      api.MCPServerTypeStreamableHTTP,
		URL:       backend.URL + "/mcp",
	})
	require.NoError(t, err)

	started := make(chan error, 1)
	go func() { started <- svc.Start(t.Context()) }()
	<-entered
	require.Equal(t, services.StateStarting, svc.GetState())

	require.NoError(t, svc.Stop(t.Context()))
	require.ErrorIs(t, <-started, services.ErrStopRequested)
	require.Equal(t, services.StateDisconnected, svc.GetState())
	require.Nil(t, svc.GetMCPClient())
	require.Zero(t, svc.GetConsecutiveFailures(), "a cancelled connect is not a connection failure")
}

// A Stop that arrives while a Restart waits out its grace period ends the
// restart: the server stays stopped instead of reconnecting after the Stop.
func TestStopDuringRestartGraceStaysStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc, err := NewService(&api.MCPServer{
			Name:      "restarting",
			Namespace: "muster",
			Type:      api.MCPServerTypeStreamableHTTP,
			URL:       "http://backend.invalid/mcp",
		})
		require.NoError(t, err)
		svc.UpdateState(services.StateFailed, services.HealthUnhealthy, nil)

		restarted := make(chan error, 1)
		go func() { restarted <- svc.Restart(t.Context()) }()
		synctest.Wait()

		require.NoError(t, svc.Stop(t.Context()))
		require.ErrorIs(t, <-restarted, services.ErrStopRequested)
		require.Equal(t, services.StateDisconnected, svc.GetState())
		require.Nil(t, svc.GetMCPClient())
	})
}
