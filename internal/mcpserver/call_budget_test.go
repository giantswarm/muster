package mcpserver

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// progressingFakeClient answers a tool call after reporting progress
// `reports` times, one every `every`, through the client's own router, the
// way a downstream notifications/progress reaches it. A negative reports
// reports forever. After its last report it stays silent for `silence`
// before it answers.
type progressingFakeClient struct {
	sessionFakeClient
	base    *baseMCPClient
	reports int
	every   time.Duration
	silence time.Duration
}

func (f *progressingFakeClient) CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cut := func() (*mcp.CallToolResult, error) {
		return nil, fmt.Errorf("transport error: failed to send request: Post \"http://backend/mcp\": %w", ctx.Err())
	}
	for i := 1; f.reports < 0 || i <= f.reports; i++ {
		select {
		case <-time.After(f.every):
		case <-ctx.Done():
			return cut()
		}
		if req.Params.Meta != nil && req.Params.Meta.ProgressToken != nil {
			f.base.progress.route(progressNotification(req.Params.Meta.ProgressToken.(string), float64(i)))
		}
	}
	select {
	case <-time.After(f.silence):
		return f.sessionFakeClient.CallTool(ctx, req)
	case <-ctx.Done():
		return cut()
	}
}

func progressingClient(timeout, maxCallDuration time.Duration, reports int, every, silence time.Duration) *baseMCPClient {
	base := &baseMCPClient{connected: true, timeout: timeout, maxCallDuration: maxCallDuration}
	base.client = &progressingFakeClient{base: base, reports: reports, every: every, silence: silence}
	return base
}

func withCaller(ctx context.Context) context.Context {
	return WithProgressReporter(ctx, NewProgressReporter("caller", (&progressRecorder{}).send))
}

// A call that keeps reporting progress runs past the server's timeout:
// every notification gives it the full timeout again.
func TestCallBudget_ProgressKeepsTheCallAlive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := progressingClient(30*time.Second, 10*time.Minute, 6, 20*time.Second, 20*time.Second)
		start := time.Now()

		res, err := base.callTool(withCaller(t.Context()), "sweep", nil)

		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, 140*time.Second, time.Since(start))
	})
}

// A call whose caller sent no progressToken runs with a reporter that keeps
// the progress inside muster (the aggregator gives it one): the downstream
// call still gets a token, and its progress keeps the call alive.
func TestCallBudget_ProgressWithoutACallerTokenKeepsTheCallAlive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := progressingClient(30*time.Second, 10*time.Minute, 6, 20*time.Second, 20*time.Second)
		start := time.Now()

		res, err := base.callTool(WithProgressReporter(t.Context(), NewProgressReporter(nil, nil)), "sweep", nil)

		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, 140*time.Second, time.Since(start))
	})
}

// A call without any reporter on its context sends no token downstream, so
// the server's timeout bounds the whole call.
func TestCallBudget_WithoutACallerProgressTheTimeoutHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := progressingClient(30*time.Second, 10*time.Minute, 6, 20*time.Second, 0)
		start := time.Now()

		_, err := base.callTool(t.Context(), "sweep", nil)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.EqualError(t, err, "no answer within the server's timeout of 30s: context deadline exceeded")
		assert.Equal(t, 30*time.Second, time.Since(start))
	})
}

// A server whose progress.maxDuration equals its timeout is bounded by its
// timeout, whatever progress it reports.
func TestCallBudget_MaxDurationAtTheTimeoutExtendsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := progressingClient(30*time.Second, 30*time.Second, 6, 20*time.Second, 0)
		start := time.Now()

		_, err := base.callTool(withCaller(t.Context()), "sweep", nil)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.EqualError(t, err, "no answer within the server's timeout of 30s: context deadline exceeded")
		assert.Equal(t, 30*time.Second, time.Since(start))
	})
}

// A server that sets no progress.maxDuration is cut at DefaultMaxCallDuration.
func TestCallBudget_UnsetMaxDurationIsCappedAtTheDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := progressingClient(30*time.Second, 0, -1, 20*time.Second, 0)
		start := time.Now()

		_, err := base.callTool(withCaller(t.Context()), "sweep", nil)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.EqualError(t, err, "no answer within the server's progress.maxDuration of 1h0m0s: context deadline exceeded")
		assert.Equal(t, DefaultMaxCallDuration, time.Since(start))
	})
}

// A server that falls silent after reporting is cut one timeout after its
// last notification.
func TestCallBudget_SilenceAfterProgressIsCut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := progressingClient(30*time.Second, 10*time.Minute, 2, 20*time.Second, time.Hour)
		start := time.Now()

		_, err := base.callTool(withCaller(t.Context()), "sweep", nil)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.EqualError(t, err, "no answer within the server's timeout of 30s: context deadline exceeded")
		assert.Equal(t, 40*time.Second+30*time.Second, time.Since(start))
	})
}

// A server that reports forever is cut at its progress.maxDuration.
func TestCallBudget_ProgressDoesNotExtendPastMaxCallDuration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := progressingClient(30*time.Second, 10*time.Minute, -1, 20*time.Second, 0)
		start := time.Now()

		_, err := base.callTool(withCaller(t.Context()), "sweep", nil)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.EqualError(t, err, "no answer within the server's progress.maxDuration of 10m0s: context deadline exceeded")
		assert.Equal(t, 10*time.Minute, time.Since(start))
	})
}

// Operations other than a tool call keep the plain timeout, so a cut reads
// as the deadline it is even where nothing renames it (Initialize).
func TestCallBudget_OnlyAToolCallRunsUnderIt(t *testing.T) {
	base := &baseMCPClient{timeout: 30 * time.Second, maxCallDuration: 10 * time.Minute}

	ctx, cancel, _ := base.operationContext(withCaller(t.Context()))
	defer cancel()

	assert.Nil(t, callBudgetFrom(ctx))
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(30*time.Second), deadline, time.Second)
}

// Progress for a call already cut does not revive it.
func TestCallBudget_ExtendAfterTheCutChangesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := withCallBudget(t.Context(), time.Second, time.Minute)
		defer cancel()

		time.Sleep(time.Second)
		synctest.Wait()
		require.ErrorIs(t, context.Cause(ctx), errBudgetSpent)

		callBudgetFrom(ctx).extend()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		require.ErrorIs(t, context.Cause(ctx), errBudgetSpent)
	})
}
