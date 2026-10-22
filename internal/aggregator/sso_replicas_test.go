package aggregator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// gatedBackend is a streamable-http MCP backend whose next request, once
// hold is called, waits until release: a connect that is under way for as
// long as the test needs. While fail is non-zero it answers with that status.
type gatedBackend struct {
	*httptest.Server
	holdNext atomic.Bool
	fail     atomic.Int32
	entered  chan struct{}
	gate     chan struct{}
}

func newGatedBackend(t *testing.T) *gatedBackend {
	t.Helper()
	backend := mcpgoserver.NewMCPServer("gated", "0.0.1")
	backend.AddTool(mcp.NewTool("noop"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	streamable := mcpgoserver.NewStreamableHTTPServer(backend, mcpgoserver.WithStateful(true))
	b := &gatedBackend{entered: make(chan struct{}, 1), gate: make(chan struct{})}
	b.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b.holdNext.CompareAndSwap(true, false) {
			b.entered <- struct{}{}
			<-b.gate
		}
		if status := int(b.fail.Load()); status != 0 {
			if status == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		streamable.ServeHTTP(w, r)
	}))
	t.Cleanup(b.Close)
	return b
}

func (b *gatedBackend) hold()    { b.holdNext.Store(true) }
func (b *gatedBackend) release() { close(b.gate) }

// newReplicaPair returns two aggregators serving the same token-forwarding
// server from one shared auth store, as muster's replicas share Valkey, each
// with its own connection pool and SSO tracker, plus the session context.
func newReplicaPair(t *testing.T, url string) (replicaA, replicaB *AggregatorServer, info *ServerInfo, ctx context.Context) {
	t.Helper()
	authStore := oauthstore.NewInMemorySessionAuthStore(30 * time.Minute)
	t.Cleanup(authStore.Stop)

	replicaA, info, ctx = newForwardingTestAggregator(t, url)
	replicaA.authStore = authStore
	replicaB, _, _ = newForwardingTestAggregator(t, url)
	replicaB.authStore = authStore
	return replicaA, replicaB, info, ctx
}

// TestEstablishSSOConnection_AnotherReplicasMarkStaysWhileConnecting: the
// session is connected on replica A, and replica B, whose pool has no client
// for it, connects it as well. While B's connect is under way, A still reads
// the session connected: the shared mark is A's connection, and revoking it
// before B's connect sent auth://status on A to auth_required -- the portal
// asked for a sign-in that was already done.
func TestEstablishSSOConnection_AnotherReplicasMarkStaysWhileConnecting(t *testing.T) {
	backend := newGatedBackend(t)
	replicaA, replicaB, info, ctx := newReplicaPair(t, backend.URL+"/mcp")

	require.Equal(t, ssoConnected, replicaA.establishSSOConnection(ctx, info, "https://dex.example.com"))
	require.Equal(t, pkgoauth.SessionServerStatusConnected,
		replicaA.determineSessionAuthStatus("alice", "session-1", "backend", info))

	backend.hold()
	done := make(chan ssoConnectOutcome, 1)
	go func() { done <- replicaB.establishSSOConnection(ctx, info, "https://dex.example.com") }()
	<-backend.entered

	assert.Equal(t, pkgoauth.SessionServerStatusConnected,
		replicaA.determineSessionAuthStatus("alice", "session-1", "backend", info),
		"replica B's connect must not take replica A's connection out of the status")
	authed, err := replicaA.authStore.IsAuthenticated(ctx, "session-1", "backend")
	require.NoError(t, err)
	assert.True(t, authed, "the shared mark stays while replica B connects")

	backend.release()
	require.Equal(t, ssoConnected, <-done, "replica B connects the session on its own pool")
	_, pooled := replicaB.connPool.Get("session-1", "backend")
	assert.True(t, pooled, "replica B holds its own client after the connect")
}

// TestEstablishSSOConnection_MarkWithoutClientGoesWhenTheConnectFails: a mark
// whose connect this replica could not make (the backend refused the token) is
// revoked, so the status says what failed rather than connected.
func TestEstablishSSOConnection_MarkWithoutClientGoesWhenTheConnectFails(t *testing.T) {
	backend := newGatedBackend(t)
	replicaA, replicaB, info, ctx := newReplicaPair(t, backend.URL+"/mcp")

	require.Equal(t, ssoConnected, replicaA.establishSSOConnection(ctx, info, "https://dex.example.com"))

	backend.fail.Store(http.StatusUnauthorized)
	require.Equal(t, ssoConnectFailed, replicaB.establishSSOConnection(ctx, info, "https://dex.example.com"))

	authed, err := replicaB.authStore.IsAuthenticated(ctx, "session-1", "backend")
	require.NoError(t, err)
	assert.False(t, authed, "a refused reconnect revokes the mark")
	assert.Equal(t, pkgoauth.SessionServerStatusReauthRequired,
		replicaB.determineSessionAuthStatus("alice", "session-1", "backend", info))
}
