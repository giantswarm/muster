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

// A Stop cancels the connect of the Start in flight with errStopRequested and
// returns only after that Start returned.
func TestAbortStartCancelsTheConnectAndWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &Service{}
		var cause error
		startReturned := false
		go func() {
			_ = svc.runStart(t.Context(), func(_, connectCtx context.Context) error {
				<-connectCtx.Done()
				cause = context.Cause(connectCtx)
				time.Sleep(time.Second)
				startReturned = true
				return cause
			})
		}()
		synctest.Wait()

		require.NoError(t, svc.abortStart(t.Context()))
		require.True(t, startReturned, "abortStart returns after the Start")
		require.ErrorIs(t, cause, errStopRequested)
		require.NoError(t, svc.abortStart(t.Context()), "nothing in flight")
	})
}

// A Stop whose context ends before the Start in flight returns says so.
func TestAbortStartGivesUpWithItsContext(t *testing.T) {
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
		require.ErrorIs(t, svc.abortStart(ctx), context.DeadlineExceeded)
		close(release)
	})
}

// A Stop during a connect that hangs ends that connect, leaves no client and
// settles the server stopped; the Start returns errStopRequested instead of
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
	select {
	case err := <-started:
		require.ErrorIs(t, err, errStopRequested)
	default:
		t.Fatal("Stop returned before the Start in flight")
	}
	require.Equal(t, services.StateDisconnected, svc.GetState())
	require.Nil(t, svc.GetMCPClient())
	require.Zero(t, svc.GetConsecutiveFailures(), "a cancelled connect is not a connection failure")
}
