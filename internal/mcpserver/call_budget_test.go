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

func progressingClient(timeout time.Duration, reports int, every, silence time.Duration) *baseMCPClient {
	base := &baseMCPClient{connected: true, timeout: timeout}
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
		base := progressingClient(30*time.Second, 6, 20*time.Second, 20*time.Second)
		start := time.Now()

		res, err := base.callTool(withCaller(t.Context()), "sweep", nil)

		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, 140*time.Second, time.Since(start))
	})
}

// A caller that asked for no progress sends no token downstream, so the
// server's timeout bounds the whole call.
func TestCallBudget_WithoutACallerProgressTheTimeoutHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := progressingClient(30*time.Second, 6, 20*time.Second, 0)
		start := time.Now()

		_, err := base.callTool(t.Context(), "sweep", nil)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.EqualError(t, err, "no answer within the server's timeout of 30s: context deadline exceeded")
		assert.Equal(t, 30*time.Second, time.Since(start))
	})
}

// A server that falls silent after reporting is cut one timeout after its
// last notification.
func TestCallBudget_SilenceAfterProgressIsCut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := progressingClient(30*time.Second, 2, 20*time.Second, time.Hour)
		start := time.Now()

		_, err := base.callTool(withCaller(t.Context()), "sweep", nil)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.EqualError(t, err, "no answer within the server's timeout of 30s: context deadline exceeded")
		assert.Equal(t, 40*time.Second+30*time.Second, time.Since(start))
	})
}

// A server that reports forever is cut at MaxProgressCallDuration.
func TestCallBudget_ProgressDoesNotExtendACallForever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := progressingClient(30*time.Second, -1, 20*time.Second, 0)
		start := time.Now()

		_, err := base.callTool(withCaller(t.Context()), "sweep", nil)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.EqualError(t, err, "no answer within 1h0m0s, the longest a call that reports progress may run: context deadline exceeded")
		assert.Equal(t, MaxProgressCallDuration, time.Since(start))
	})
}

// Progress for a call already cut does not revive it.
func TestCallBudget_ExtendAfterTheCutChangesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := withCallBudget(t.Context(), time.Second)
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
