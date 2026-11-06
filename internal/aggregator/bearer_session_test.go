package aggregator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/server"
)

// TestBearerSessions_EndsOnceAtExp: a bound session is ended by its timer at
// the bearer's exp and only then, and exactly once -- the timer's end and a
// later release do not both get it.
func TestBearerSessions_EndsOnceAtExp(t *testing.T) {
	b := newBearerSessions()
	exp := time.Now().Add(100 * time.Millisecond)
	var ended atomic.Int32
	b.bind("s", exp, func() {
		if _, ok := b.release("s", time.Now()); ok {
			ended.Add(1)
		}
	})

	assert.False(t, b.expired("s", time.Now()), "not expired before its exp")
	_, ok := b.release("s", time.Now())
	assert.False(t, ok, "a release before the exp is refused")

	require.Eventually(t, func() bool { return ended.Load() == 1 }, 2*time.Second, 10*time.Millisecond)
	_, ok = b.release("s", time.Now())
	assert.False(t, ok, "a session is released once")
	assert.False(t, b.bound("s"))
}

// TestBearerSessions_FirstBindingStands: the session is keyed by its bearer,
// so its exp is the one of its first binding.
func TestBearerSessions_FirstBindingStands(t *testing.T) {
	b := newBearerSessions()
	t.Cleanup(b.stop)
	now := time.Now()
	b.bind("s", now.Add(time.Hour), func() {})
	b.bind("s", now.Add(-time.Hour), func() {})

	assert.False(t, b.expired("s", now))
	assert.True(t, b.expired("s", now.Add(2*time.Hour)))
}

// TestBearerSessions_StopCancelsPendingEnds: shutdown cancels the timers and
// refuses new bindings.
func TestBearerSessions_StopCancelsPendingEnds(t *testing.T) {
	b := newBearerSessions()
	var ended atomic.Int32
	b.bind("s", time.Now().Add(50*time.Millisecond), func() { ended.Add(1) })
	b.stop()
	b.bind("t", time.Now(), func() { ended.Add(1) })

	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, int32(0), ended.Load())
	assert.False(t, b.bound("s"))
	assert.False(t, b.bound("t"))
}

// TestBindBearerSession_OnlyForwardedBearersWithExp: a session is bound when
// its bearer is a decodable JWT with an exp; an opaque bearer (muster's own
// token, whose session refreshes through its family) and a JWT without exp
// are not.
func TestBindBearerSession_OnlyForwardedBearersWithExp(t *testing.T) {
	a := &AggregatorServer{bearerSessions: newBearerSessions()}
	t.Cleanup(a.bearerSessions.stop)

	withExp := unsignedJWT(t, map[string]any{"sub": "agent", "exp": time.Now().Add(time.Hour).Unix()})
	withoutExp := unsignedJWT(t, map[string]any{"sub": "agent"})
	for sessionID, bearer := range map[string]string{
		"ext-with-exp":    withExp,
		"ext-without-exp": withoutExp,
		"family-opaque":   "opaque-access-token",
	} {
		a.bindBearerSession(ssoSession{sessionID: sessionID, tokens: server.CallerTokens{Bearer: bearer}})
	}

	assert.True(t, a.bearerSessions.bound("ext-with-exp"))
	assert.False(t, a.bearerSessions.bound("ext-without-exp"))
	assert.False(t, a.bearerSessions.bound("family-opaque"))
}

// bearerSessionFixture is an aggregator with two forwarding servers and a
// pool: session "expired" holds connections to both, session "live" to
// "shared" only. "expired" is bound to a bearer past its exp.
func bearerSessionFixture(t *testing.T) (*AggregatorServer, *updatableStubRegistry) {
	t.Helper()
	services := &updatableStubRegistry{services: map[string]*updatableStubService{}}
	registry := NewServerRegistry("x")
	for _, name := range []string{"only-expired", "shared"} {
		services.services[name] = &updatableStubService{stubServiceInfo: stubServiceInfo{name: name, state: api.StateConnected}}
		require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: name, Namespace: "agent-platform"},
			URL:                "https://" + name + ".example.test/mcp",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.test", Scope: "openid"},
			AuthConfig:         &api.MCPServerAuth{Type: "oauth", ForwardToken: true},
		}))
	}
	api.RegisterServiceRegistry(services)
	t.Cleanup(func() { api.RegisterServiceRegistry(nil) })

	pool := NewSessionConnectionPool(time.Hour)
	t.Cleanup(func() { pool.Stop(); pool.DrainAll() })
	pool.Put("expired", "only-expired", &noopMCPClient{})
	pool.Put("expired", "shared", &noopMCPClient{})
	pool.Put("live", "shared", &noopMCPClient{})

	a := &AggregatorServer{registry: registry, connPool: pool, bearerSessions: newBearerSessions()}
	t.Cleanup(a.bearerSessions.stop)
	a.bearerSessions.bind("expired", time.Now().Add(-time.Second), func() {})
	return a, services
}

// TestEndBearerSession_ClosesTheSessionOnce: the expired session's
// connections are closed, the other session's are not, and the server whose
// last connection it held goes back to Awaiting Session -- once, however
// often the end is asked for.
func TestEndBearerSession_ClosesTheSessionOnce(t *testing.T) {
	logBuf := captureLog(t)
	a, services := bearerSessionFixture(t)

	a.endBearerSession("expired")
	a.endBearerSession("expired")

	assert.Empty(t, a.connPool.Snapshot("expired"))
	_, ok := a.connPool.Get("live", "shared")
	assert.True(t, ok, "another session's connection stays")
	assert.Equal(t, []api.ServiceState{api.StateAwaitingSession}, services.services["only-expired"].updates)
	assert.Empty(t, services.services["shared"].updates, "a server another session still holds keeps its state")
	assert.Contains(t, logBuf.String(), "its resource bearer expired at")
	assert.Contains(t, logBuf.String(), "closed 2 backend connections")
}

// TestEndBearerSession_NotBeforeExp: a bound session whose bearer is still
// valid is left alone.
func TestEndBearerSession_NotBeforeExp(t *testing.T) {
	a, services := bearerSessionFixture(t)
	a.bearerSessions.bind("live", time.Now().Add(time.Hour), func() {})

	a.endBearerSession("live")

	_, ok := a.connPool.Get("live", "shared")
	assert.True(t, ok)
	assert.Empty(t, services.services["shared"].updates)
}

// TestPooledSessions_EndsExpiredBearerSessions: the capability poll does not
// list through a session whose resource bearer has expired; it ends it.
func TestPooledSessions_EndsExpiredBearerSessions(t *testing.T) {
	a, _ := bearerSessionFixture(t)

	var sessions []string
	for _, ps := range a.pooledSessions("shared") {
		sessions = append(sessions, ps.SessionID)
	}

	assert.Equal(t, []string{"live"}, sessions)
	assert.Empty(t, a.connPool.Snapshot("expired"), "the expired session was ended, not skipped")
}

// TestPollSessionCapabilities_SkipsExpiredBearerSession: a listing planned
// before the bearer expired is not sent through the session's connection.
func TestPollSessionCapabilities_SkipsExpiredBearerSession(t *testing.T) {
	a, _ := bearerSessionFixture(t)
	client, ok := a.connPool.Get("expired", "shared")
	require.True(t, ok)
	listing := &countingListClient{MCPClient: client}

	a.pollSessionCapabilities("shared", PooledSession{SessionID: "expired", Client: listing})

	assert.Equal(t, int32(0), listing.lists.Load(), "no listing through the expired session")
	assert.Empty(t, a.connPool.Snapshot("expired"))
}

// countingListClient counts the listings sent through it.
type countingListClient struct {
	MCPClient
	lists atomic.Int32
}

func (c *countingListClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	c.lists.Add(1)
	return c.MCPClient.ListTools(ctx)
}
