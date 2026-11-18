package aggregator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
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
		a.bindBearerSession(context.Background(), ssoSession{sessionID: sessionID, tokens: server.CallerTokens{Bearer: bearer}})
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

// bearerTick is the time scale of the tests that let a bound bearer expire.
const bearerTick = 200 * time.Millisecond

// TestBindBearerExpiry_SessionEndsAtTheLatestBearerExp: a session keyed by
// its principal gets a refreshed bearer before the old one expires; it ends
// at the exp of the latest valid bearer, never earlier (an older bearer
// replayed after a newer one) and never later.
func TestBindBearerExpiry_SessionEndsAtTheLatestBearerExp(t *testing.T) {
	for name, tc := range map[string]struct {
		binds []int // the bearers' exps, in bearerTicks from now
		endAt int   // the exp the session ends at
	}{
		"no later bearer: ends at the bound exp": {binds: []int{1}, endAt: 1},
		"a later bearer moves the end":           {binds: []int{1, 3}, endAt: 3},
		"an older bearer does not move it":       {binds: []int{3, 1}, endAt: 3},
	} {
		t.Run(name, func(t *testing.T) {
			a, _ := bearerSessionFixture(t)
			now := time.Now()
			for _, ticks := range tc.binds {
				a.bindBearerExpiry(context.Background(), "live", now.Add(time.Duration(ticks)*bearerTick))
			}

			time.Sleep(time.Until(now.Add(time.Duration(tc.endAt)*bearerTick - bearerTick/2)))
			_, ok := a.connPool.Get("live", "shared")
			require.True(t, ok, "the session holds its connection before the latest bearer's exp")

			require.Eventually(t, func() bool { return len(a.connPool.Snapshot("live")) == 0 },
				2*time.Second, 10*time.Millisecond, "the session ends at the latest bearer's exp")
		})
	}
}

// TestBindBearerExpiry_TwoReplicas_LaterBearerOnOneKeepsTheSessionOnBoth:
// both replicas bound the session to its first bearer; the person's later
// requests, with a refreshed bearer, all reach replica b. Replica a's timer
// at the first bearer's exp must not end the session: its teardown would
// revoke the shared auth state that b's connections stand on. Both end it
// at the refreshed bearer's exp.
func TestBindBearerExpiry_TwoReplicas_LaterBearerOnOneKeepsTheSessionOnBoth(t *testing.T) {
	a, _ := bearerSessionFixture(t)
	authStore := oauthstore.NewInMemorySessionAuthStore(time.Hour)
	t.Cleanup(authStore.Stop)
	expiries := newValkeyBearerExpiryStore(newTestValkeyClient(t), "muster:")
	a.authStore, a.bearerExpiries = authStore, expiries
	poolB := NewSessionConnectionPool(time.Hour)
	t.Cleanup(func() { poolB.Stop(); poolB.DrainAll() })
	b := &AggregatorServer{registry: a.registry, connPool: poolB, bearerSessions: newBearerSessions(),
		authStore: authStore, bearerExpiries: expiries}
	t.Cleanup(b.bearerSessions.stop)

	ctx := context.Background()
	require.NoError(t, authStore.MarkAuthenticated(ctx, "live", "shared"))
	poolB.Put("live", "shared", &noopMCPClient{})
	now := time.Now()
	a.bindBearerExpiry(ctx, "live", now.Add(bearerTick))
	b.bindBearerExpiry(ctx, "live", now.Add(bearerTick))
	b.bindBearerExpiry(ctx, "live", now.Add(3*bearerTick))
	// A third replica whose first request carries the first bearer records
	// it after b recorded the refreshed one.
	require.NoError(t, expiries.extend(ctx, "live", now.Add(bearerTick)))

	time.Sleep(time.Until(now.Add(2 * bearerTick)))
	_, onA := a.connPool.Get("live", "shared")
	_, onB := poolB.Get("live", "shared")
	authenticated, err := authStore.IsAuthenticated(ctx, "live", "shared")
	require.NoError(t, err)
	assert.True(t, onA && onB && authenticated, "past the first bearer's exp the session is live on both replicas")

	require.Eventually(t, func() bool {
		authenticated, _ := authStore.IsAuthenticated(ctx, "live", "shared")
		return len(a.connPool.Snapshot("live")) == 0 && len(poolB.Snapshot("live")) == 0 && !authenticated
	}, 2*time.Second, 10*time.Millisecond, "both replicas end the session at the refreshed bearer's exp")
}

// TestEndBearerSession_SharedRecordUnreadable_EndsAtThisReplicasExp: when
// the shared record does not answer, nothing shows a later valid bearer, so
// the session ends at the exp this replica saw.
func TestEndBearerSession_SharedRecordUnreadable_EndsAtThisReplicasExp(t *testing.T) {
	a, _ := bearerSessionFixture(t)
	a.bearerExpiries = unreadableBearerExpiries{}

	assert.True(t, a.endBearerSession("expired"))
	assert.Empty(t, a.connPool.Snapshot("expired"))
}

// unreadableBearerExpiries is a shared record whose store does not answer.
type unreadableBearerExpiries struct{}

func (unreadableBearerExpiries) extend(context.Context, string, time.Time) error {
	return errors.New("valkey down")
}

func (unreadableBearerExpiries) latest(context.Context, string) (time.Time, error) {
	return time.Time{}, errors.New("valkey down")
}
