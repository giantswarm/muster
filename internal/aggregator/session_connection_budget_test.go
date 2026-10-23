package aggregator

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/mcpserver"
)

// newWaitingBackend serves a tool "wait" that answers only once its caller
// gives up, and returns a connected streamable-http client to it built with
// the given timeout: a session's pooled connection to a slow backend.
func newWaitingBackend(t *testing.T, timeout time.Duration) *mcpserver.StreamableHTTPClient {
	t.Helper()
	release := make(chan struct{})
	backend := server.NewMCPServer("backend", "test")
	backend.AddTool(mcp.NewTool("wait"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		select {
		case <-ctx.Done():
		case <-release:
		}
		return mcp.NewToolResultText("late"), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(backend))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(release) })

	c := mcpserver.NewStreamableHTTPClientWithHeaders(ts.URL, nil).WithTimeout(timeout)
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.Initialize(t.Context()))
	return c
}

// A changed spec.timeout reaches the connection a session already holds: the
// re-registration the reconciler's restart brings applies it, and the next
// call on that connection is cut by the new timeout while the connection
// stays pooled. Before, it kept the timeout it was opened with until muster
// restarted.
func TestRegisterServerPendingAuth_TimeoutChangeReachesLiveConnections(t *testing.T) {
	auth := &api.MCPServerAuth{Type: "oauth", ForwardToken: true}
	f := newResetFixture(t, auth)
	live := newWaitingBackend(t, time.Hour)
	f.agg.connPool.Put("session-c", "svc", live)

	require.NoError(t, f.am.RegisterServerPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "svc", ToolPrefix: "svc", Timeout: 300 * time.Millisecond},
		URL:                "https://svc.example.com/mcp",
		AuthConfig:         auth,
	}))

	pooled, ok := f.agg.connPool.Get("session-c", "svc")
	require.True(t, ok, "the connection stays open")
	require.Same(t, live, pooled)

	// Bounded only so a regression fails instead of waiting out the hour.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	_, err := pooled.CallTool(ctx, "wait", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no answer within the server's timeout of 300ms")

	for _, sid := range []string{"session-a", "session-b"} {
		authenticated, capabilities, stillPooled := f.state(t, sid, "svc")
		assert.True(t, authenticated && capabilities && stillPooled, "%s keeps its connection: a timeout change is no auth change", sid)
	}
}

// SetServerBudget touches only the named server's connections and counts the
// ones that took the budget.
func TestSessionConnectionPool_SetServerBudget(t *testing.T) {
	pool := NewSessionConnectionPool(time.Hour)
	t.Cleanup(pool.Stop)
	svc := mcpserver.NewStreamableHTTPClientWithHeaders("https://svc.example.com/mcp", nil).WithTimeout(time.Hour)
	other := mcpserver.NewStreamableHTTPClientWithHeaders("https://other.example.com/mcp", nil).WithTimeout(time.Hour)
	pool.Put("s1", "svc", svc)
	pool.Put("s2", "svc", &poolTestClient{})
	pool.Put("s1", "other", other)

	assert.Equal(t, 1, pool.SetServerBudget("svc", time.Second, time.Minute), "a client without SetBudget is skipped")
	assert.Equal(t, 0, pool.SetServerBudget("missing", time.Second, time.Minute))
}
