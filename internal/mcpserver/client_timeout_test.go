package mcpserver

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowFakeClient answers a tool call after delay, or -- the way mcp-go
// reports a request its context cut -- with a transport error wrapping the
// context's error when the context ends first: a backend tool that blocks
// while it follows something.
type slowFakeClient struct {
	sessionFakeClient
	delay time.Duration
}

func (f *slowFakeClient) CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	select {
	case <-time.After(f.delay):
		return f.sessionFakeClient.CallTool(ctx, req)
	case <-ctx.Done():
		return nil, fmt.Errorf("transport error: failed to send request: Post \"http://backend/mcp\": %w", ctx.Err())
	}
}

func slowClient(delay, timeout time.Duration) *baseMCPClient {
	return &baseMCPClient{client: &slowFakeClient{delay: delay}, connected: true, timeout: timeout}
}

// A call that finishes within the server's timeout succeeds, however long
// it takes relative to the default.
func TestOperationTimeout_CallWithinTheBudgetSucceeds(t *testing.T) {
	base := slowClient(150*time.Millisecond, time.Second)

	res, err := base.callTool(t.Context(), "watch", nil)

	require.NoError(t, err)
	require.NotNil(t, res)
}

// A call that outlasts the server's timeout is cut when the budget runs out
// and fails with the deadline, not with the transport error mcp-go reports.
func TestOperationTimeout_CallBeyondTheBudgetFailsWithTheDeadline(t *testing.T) {
	base := slowClient(10*time.Second, 300*time.Millisecond)
	start := time.Now()

	_, err := base.callTool(t.Context(), "watch", nil)

	elapsed := time.Since(start)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.EqualError(t, err, "no answer within the server's timeout of 300ms: context deadline exceeded")
	assert.GreaterOrEqual(t, elapsed, 300*time.Millisecond)
	assert.Less(t, elapsed, 5*time.Second, "the call was cut by the timeout, not by the backend")
}

// A client built without a timeout -- a server whose definition sets no
// spec.timeout -- runs every operation under the CRD's default of 30 s.
func TestOperationTimeout_DefaultsToThirtySeconds(t *testing.T) {
	ctx, cancel, timeout := (&baseMCPClient{}).operationContext(t.Context())
	defer cancel()

	assert.Equal(t, 30*time.Second, timeout)
	assert.Equal(t, DefaultTimeout, timeout)
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(30*time.Second), deadline, time.Second)
}

// A caller whose own context ends before the server's timeout gets that
// failure as the transport reported it; only the server's budget is renamed.
func TestOperationTimeout_CallerDeadlineIsReportedAsIs(t *testing.T) {
	base := slowClient(10*time.Second, 10*time.Second)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, err := base.callTool(ctx, "watch", nil)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "transport error")
}

// The client an OAuth server's per-session connection is built on takes the
// same budget as every other: a tool call on it is cut at the server's
// timeout, not at the default.
func TestOperationTimeout_DynamicAuthClientHonoursWithTimeout(t *testing.T) {
	c := NewDynamicAuthClient("http://backend/mcp", nil, "", "", "").WithTimeout(300 * time.Millisecond)
	c.client = &slowFakeClient{delay: 10 * time.Second}
	c.connected = true
	start := time.Now()

	_, err := c.CallTool(t.Context(), "watch", nil)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.EqualError(t, err, "no answer within the server's timeout of 300ms: context deadline exceeded")
	assert.Less(t, time.Since(start), 5*time.Second, "the call was cut by the timeout, not by the backend")
}
