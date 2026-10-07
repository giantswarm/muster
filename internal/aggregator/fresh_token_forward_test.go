package aggregator

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
)

// connectForwardingSession connects session-1 of alice to the forwarding
// backend with a token issued at iat and returns the aggregator and the
// session's pooled client.
func connectForwardingSession(t *testing.T, backend *flakyBackend, iat time.Time) (*AggregatorServer, MCPClient, context.Context) {
	t.Helper()
	a, info, _ := newForwardingTestAggregator(t, backend.URL+"/mcp")
	a.authStore = oauthstore.NewInMemorySessionAuthStore(time.Hour)
	a.capabilityStore = oauthstore.NewInMemoryCapabilityStore(time.Hour)
	ctx := forwardedTokenIssuedAt(t, iat)

	require.Equal(t, ssoConnected, a.establishSSOConnection(ctx, info, "https://dex.example.com"))
	client, ok := a.connPool.Get("session-1", "backend")
	require.True(t, ok)
	return a, client, ctx
}

// TestCallTool_FreshTokenRefusedKeepsTheSession: right after the issuer
// rotated its signing key, a backend refuses the token of a login made moments
// ago on a pooled connection's tool call. The person's login is valid, so the
// session keeps its authentication and connection and the call is answered
// with a transient error, not the sign-in challenge; once the backend knows
// the key, the next call goes through on the same connection.
func TestCallTool_FreshTokenRefusedKeepsTheSession(t *testing.T) {
	backend := newFlakyBackend(t)
	a, client, ctx := connectForwardingSession(t, backend, time.Now().Add(-30*time.Second))

	backend.fail.Store(http.StatusUnauthorized)
	result, err := a.callToolWithTokenExchangeRetry(ctx, "backend", "noop", nil, "session-1", "alice")
	require.NoError(t, err)
	require.True(t, result.IsError)
	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "refused a token issued moments ago")
	assert.NotContains(t, text, "auth_required")

	authenticated, _ := a.authStore.IsAuthenticated(ctx, "session-1", "backend")
	assert.True(t, authenticated, "a fresh-token refusal is not a lost credential")
	assert.True(t, a.connPool.Holds("session-1", "backend", client), "the pooled connection stays")

	backend.fail.Store(0)
	result, err = a.callToolWithTokenExchangeRetry(ctx, "backend", "noop", nil, "session-1", "alice")
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

// TestCallTool_OldTokenRefusedIsALostCredential: a 401 for a token issued
// longer ago than the gateway's JWKS refresh is the backend's verdict on the
// credential: the session's authentication is retired and the call answered
// with the sign-in challenge, as before.
func TestCallTool_OldTokenRefusedIsALostCredential(t *testing.T) {
	backend := newFlakyBackend(t)
	a, _, ctx := connectForwardingSession(t, backend, time.Now().Add(-10*time.Minute))

	backend.fail.Store(http.StatusUnauthorized)
	result, err := a.callToolWithTokenExchangeRetry(ctx, "backend", "noop", nil, "session-1", "alice")
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.NotContains(t, result.Content[0].(mcp.TextContent).Text, "refused a token issued moments ago")

	authenticated, _ := a.authStore.IsAuthenticated(ctx, "session-1", "backend")
	assert.False(t, authenticated)
	_, pooled := a.connPool.Get("session-1", "backend")
	assert.False(t, pooled)
}

// TestRelistSession_FreshTokenRefusedIsRetried: the capability poll's
// re-listing through a session's pooled connection meets the same fresh-token
// 401. muster re-lists again on its own on the short transport backoff, and
// once the backend knows the key the session's capabilities are current
// without a request from the person.
func TestRelistSession_FreshTokenRefusedIsRetried(t *testing.T) {
	backend := newFlakyBackend(t)
	a, client, ctx := connectForwardingSession(t, backend, time.Now().Add(-30*time.Second))
	retries := captureSSORetries(a)
	require.NoError(t, a.capabilityStore.DeleteEntry(ctx, "session-1", "backend"))

	backend.fail.Store(http.StatusUnauthorized)
	a.relistSession("backend", "session-1", client, refreshByPoll, 0)
	retry := nextRetry(t, retries)
	assert.Equal(t, ssoTransportBackoffBase, retry.delay)

	// Still unknown at the first retry: the next one backs off further.
	retry.run()
	retry = nextRetry(t, retries)
	assert.Equal(t, 2*ssoTransportBackoffBase, retry.delay)

	backend.fail.Store(0)
	retry.run()
	assert.Empty(t, retries, "a successful re-listing schedules none")
	caps, err := a.capabilityStore.Get(ctx, "session-1", "backend")
	require.NoError(t, err)
	require.NotNil(t, caps)
	assert.Equal(t, "noop", caps.Tools[0].Name)
}

// TestRelistSession_NoRetry: muster does not re-list on its own after a 401
// for an old token, nor for a connection the session no longer holds.
func TestRelistSession_NoRetry(t *testing.T) {
	t.Run("old token", func(t *testing.T) {
		backend := newFlakyBackend(t)
		a, client, _ := connectForwardingSession(t, backend, time.Now().Add(-10*time.Minute))
		retries := captureSSORetries(a)

		backend.fail.Store(http.StatusUnauthorized)
		a.relistSession("backend", "session-1", client, refreshByPoll, 0)
		assert.Empty(t, retries)
	})
	t.Run("connection evicted", func(t *testing.T) {
		backend := newFlakyBackend(t)
		a, client, _ := connectForwardingSession(t, backend, time.Now().Add(-30*time.Second))
		retries := captureSSORetries(a)

		backend.fail.Store(http.StatusUnauthorized)
		a.relistSession("backend", "session-1", client, refreshByPoll, 0)
		retry := nextRetry(t, retries)
		a.connPool.Evict("session-1", "backend")
		backend.fail.Store(0)
		retry.run()
		assert.Empty(t, retries)
	})
}
