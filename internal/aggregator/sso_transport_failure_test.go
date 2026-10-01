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

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/server"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// flakyBackend is a streamable-http MCP backend that answers every request
// with the status in fail while it is non-zero, and serves MCP otherwise.
type flakyBackend struct {
	*httptest.Server
	fail atomic.Int32
}

func newFlakyBackend(t *testing.T) *flakyBackend {
	t.Helper()
	backend := mcpgoserver.NewMCPServer("flaky", "0.0.1")
	backend.AddTool(mcp.NewTool("noop"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	streamable := mcpgoserver.NewStreamableHTTPServer(backend, mcpgoserver.WithStateful(true))
	b := &flakyBackend{}
	b.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// newForwardingTestAggregator registers one token-forwarding server pointing
// at url and returns the aggregator, the server's info and a session context
// carrying a forwardable ID token.
func newForwardingTestAggregator(t *testing.T, url string) (*AggregatorServer, *ServerInfo, context.Context) {
	t.Helper()
	api.RegisterOAuthHandler(nil)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	registry := NewServerRegistry("x")
	require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "backend", ToolPrefix: "be"},
		URL:                url,
		AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com"},
		AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
	}))
	pool := NewSessionConnectionPool(time.Hour)
	t.Cleanup(pool.Stop)
	t.Cleanup(pool.DrainAll)

	a := &AggregatorServer{registry: registry, connPool: pool, ssoTracker: newSSOTracker()}
	info := getServerInfo(t, registry, "backend")

	idToken := unsignedJWT(t, map[string]any{
		"sub": "alice", "iss": "https://dex.example.com", "aud": []string{"muster"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	ctx := server.ContextWithCallerTokens(context.Background(), server.CallerTokens{IDToken: idToken, Bearer: idToken})
	ctx = api.WithSubject(ctx, "alice")
	ctx = api.WithSessionID(ctx, "session-1")
	return a, info, ctx
}

// expireSSOFailure moves the recorded failure's timestamp past its backoff,
// as if the backoff had elapsed.
func expireSSOFailure(t *testing.T, tr *ssoTracker, sub, serverName string) {
	t.Helper()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	entry := tr.failedServers[sub][serverName]
	require.NotNil(t, entry)
	entry.failedAt = time.Now().Add(-entry.backoff())
}

// TestEstablishSSOConnection_TransportFailureIsNotAnAuthFailure: a backend
// that answers 503 during the forwarded-token connect has not judged the token.
// The failure is recorded as a transport failure (short backoff, status
// unreachable, no trust diagnostic), and once the backend is back the next
// attempt after that short backoff connects without a muster restart.
func TestEstablishSSOConnection_TransportFailureIsNotAnAuthFailure(t *testing.T) {
	backend := newFlakyBackend(t)
	a, info, ctx := newForwardingTestAggregator(t, backend.URL+"/mcp")

	backend.fail.Store(http.StatusServiceUnavailable)
	require.Equal(t, ssoConnectFailed, a.establishSSOConnection(ctx, info, "https://dex.example.com"))

	require.True(t, a.ssoTracker.HasSSOFailed("alice", "backend"), "the failed attempt is held back")
	assert.True(t, a.ssoTracker.HasSSOTransportFailed("alice", "backend"),
		"a 503 is a transport failure, not a refused token")
	failure, ok := a.ssoTracker.activeFailure("alice", "backend")
	require.True(t, ok)
	assert.Equal(t, ssoTransportBackoffBase, failure.backoff(),
		"a transport failure is retried on the short schedule, not the 5-30 min auth backoff")
	assert.NotContains(t, failure.reason, "must trust this issuer",
		"a transport failure carries no token-trust diagnostic")
	assert.Contains(t, failure.reason, "503")
	assert.Equal(t, pkgoauth.SessionServerStatusUnreachable,
		a.determineSessionAuthStatus("alice", "session-1", "backend", info),
		"re-authenticating cannot fix a transport failure, so it is not reauth_required")

	backend.fail.Store(0)
	expireSSOFailure(t, a.ssoTracker, "alice", "backend")
	require.Equal(t, ssoConnected, a.establishSSOConnection(ctx, info, "https://dex.example.com"),
		"the recovered backend connects on the next attempt")
}

// TestEstablishSSOConnection_RefusedTokenIsAnAuthFailure: a 401 is the
// backend's verdict on the forwarded token. It keeps the long auth backoff,
// reauth_required and the trust diagnostic.
func TestEstablishSSOConnection_RefusedTokenIsAnAuthFailure(t *testing.T) {
	backend := newFlakyBackend(t)
	a, info, ctx := newForwardingTestAggregator(t, backend.URL+"/mcp")

	backend.fail.Store(http.StatusUnauthorized)
	require.Equal(t, ssoConnectFailed, a.establishSSOConnection(ctx, info, "https://dex.example.com"))

	assert.False(t, a.ssoTracker.HasSSOTransportFailed("alice", "backend"))
	failure, ok := a.ssoTracker.activeFailure("alice", "backend")
	require.True(t, ok)
	assert.Equal(t, ssoTrackerFailureTTL, failure.backoff())
	assert.Contains(t, failure.reason, "must trust this issuer")
	assert.Equal(t, pkgoauth.SessionServerStatusReauthRequired,
		a.determineSessionAuthStatus("alice", "session-1", "backend", info))
}

func TestSSOTracker_FailureKindsKeepTheirOwnSchedule(t *testing.T) {
	t.Run("transport failures double from 15s up to 2 min", func(t *testing.T) {
		assert.Equal(t, 15*time.Second, ssoTransportBackoffDuration(1))
		assert.Equal(t, 30*time.Second, ssoTransportBackoffDuration(2))
		assert.Equal(t, 2*time.Minute, ssoTransportBackoffDuration(5))
		assert.Equal(t, 2*time.Minute, ssoTransportBackoffDuration(100))
	})

	t.Run("a transport failure expires after its short backoff", func(t *testing.T) {
		tr := newSSOTracker()
		tr.MarkSSOTransportFailed("u", "s", "backend unreachable")
		require.True(t, tr.HasSSOFailed("u", "s"))
		tr.mu.Lock()
		tr.failedServers["u"]["s"].failedAt = time.Now().Add(-20 * time.Second)
		tr.mu.Unlock()
		assert.False(t, tr.HasSSOFailed("u", "s"))
	})

	t.Run("a failure of the other kind restarts the count", func(t *testing.T) {
		tr := newSSOTracker()
		tr.MarkSSOTransportFailed("u", "s", "")
		tr.MarkSSOTransportFailed("u", "s", "")
		require.Equal(t, 2, tr.GetFailureCount("u", "s"))

		tr.MarkSSOFailedWithReason("u", "s", "401")
		assert.Equal(t, 1, tr.GetFailureCount("u", "s"))
		assert.False(t, tr.HasSSOTransportFailed("u", "s"))
		assert.Equal(t, "401", tr.SSOFailureReason("u", "s"))
	})
}
