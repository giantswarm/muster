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

// isBound reports whether sessionID is bound to a bearer: any bound exp is
// after the zero time.
func isBound(b *bearerSessions, sessionID string) bool {
	return b.live(sessionID, time.Time{})
}

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
	assert.False(t, isBound(b, "s"))
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
	assert.False(t, isBound(b, "s"))
	assert.False(t, isBound(b, "t"))
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

	assert.True(t, isBound(a.bearerSessions, "ext-with-exp"))
	assert.False(t, isBound(a.bearerSessions, "ext-without-exp"))
	assert.False(t, isBound(a.bearerSessions, "family-opaque"))
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

// bindBearer runs agg's binding for a request of sessionID whose bearer
// expires at exp.
func bindBearer(t *testing.T, agg *AggregatorServer, sessionID string, exp time.Time) {
	t.Helper()
	bearer := unsignedJWT(t, map[string]any{"sub": "agent", "exp": exp.Unix()})
	agg.bindBearerSession(context.Background(), ssoSession{sessionID: sessionID, tokens: server.CallerTokens{Bearer: bearer}})
}

// TestBindBearerSession_SessionEndsAtTheLatestBearerExp: a session keyed by
// its principal gets a refreshed bearer before the old one expires; its
// binding ends at the exp of the latest bearer, never earlier (an older
// bearer replayed after a newer one) and never later.
func TestBindBearerSession_SessionEndsAtTheLatestBearerExp(t *testing.T) {
	for name, tc := range map[string]struct {
		binds []time.Duration // the bearers' exps from now, in request order
		endAt time.Duration   // the exp the session's binding ends at
	}{
		"no later bearer: ends at the bound exp": {binds: []time.Duration{time.Hour}, endAt: time.Hour},
		"a later bearer moves the end":           {binds: []time.Duration{time.Hour, 2 * time.Hour}, endAt: 2 * time.Hour},
		"an older bearer does not move it":       {binds: []time.Duration{2 * time.Hour, time.Hour}, endAt: 2 * time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			a := &AggregatorServer{bearerSessions: newBearerSessions()}
			t.Cleanup(a.bearerSessions.stop)
			now := time.Now()
			for _, d := range tc.binds {
				bindBearer(t, a, "s", now.Add(d))
			}

			assert.True(t, a.bearerSessions.live("s", now.Add(tc.endAt-time.Minute)), "bound before the latest bearer's exp")
			assert.False(t, a.bearerSessions.live("s", now.Add(tc.endAt)), "not bound past the latest bearer's exp")
		})
	}
}

// TestBearerSessions_MovedBindingEndsAtTheNewExp: the timer of a moved
// binding ends the session at the new exp; the old one does not.
func TestBearerSessions_MovedBindingEndsAtTheNewExp(t *testing.T) {
	b := newBearerSessions()
	t.Cleanup(b.stop)
	var ended atomic.Int32
	end := func() {
		if _, ok := b.release("s", time.Now()); ok {
			ended.Add(1)
		}
	}
	b.bind("s", time.Now().Add(50*time.Millisecond), end)
	b.bind("s", time.Now().Add(300*time.Millisecond), end)

	require.Eventually(t, func() bool { return ended.Load() == 1 }, 2*time.Second, 10*time.Millisecond)
}

// twoBearerReplicas is two aggregators that share the session auth state and
// the bearer exp record, as two muster replicas do through Valkey; each has
// its own pool holding session "live"'s connection to "shared".
func twoBearerReplicas(t *testing.T) (a, b *AggregatorServer, authStore *oauthstore.InMemorySessionAuthStore) {
	t.Helper()
	a, _ = bearerSessionFixture(t)
	authStore = oauthstore.NewInMemorySessionAuthStore(time.Hour)
	t.Cleanup(authStore.Stop)
	expiries := newValkeyBearerExpiryStore(newTestValkeyClient(t), "muster:")
	a.authStore, a.bearerExpiries = authStore, expiries
	poolB := NewSessionConnectionPool(time.Hour)
	t.Cleanup(func() { poolB.Stop(); poolB.DrainAll() })
	poolB.Put("live", "shared", &noopMCPClient{})
	b = &AggregatorServer{registry: a.registry, connPool: poolB, bearerSessions: newBearerSessions(),
		authStore: authStore, bearerExpiries: expiries}
	t.Cleanup(b.bearerSessions.stop)
	require.NoError(t, authStore.MarkAuthenticated(context.Background(), "live", "shared"))
	return a, b, authStore
}

// TestBindBearerSession_TwoReplicas_LaterBearerOnOneKeepsTheSessionOnBoth:
// both replicas bound the session to its first bearer; the person's later
// requests, with a refreshed bearer, all reach replica b. Replica a's timer
// at the first bearer's exp must not end the session: its teardown would
// revoke the shared auth state that b's connections stand on. Replica a
// moves its binding to the refreshed bearer's exp instead.
func TestBindBearerSession_TwoReplicas_LaterBearerOnOneKeepsTheSessionOnBoth(t *testing.T) {
	a, b, authStore := twoBearerReplicas(t)
	ctx := context.Background()
	now := time.Now()
	first := time.Unix(now.Add(2*time.Second).Unix(), 0)
	bindBearer(t, a, "live", first)
	bindBearer(t, b, "live", first)
	bindBearer(t, b, "live", now.Add(time.Hour))
	// A third replica whose first request carries the first bearer records
	// it after b recorded the refreshed one.
	require.NoError(t, b.bearerExpiries.extend(ctx, "live", first))

	require.Never(t, func() bool {
		_, onA := a.connPool.Get("live", "shared")
		_, onB := b.connPool.Get("live", "shared")
		authenticated, _ := authStore.IsAuthenticated(ctx, "live", "shared")
		return !onA || !onB || !authenticated
	}, time.Until(first)+time.Second, 20*time.Millisecond, "past the first bearer's exp the session is live on both replicas")
	assert.True(t, a.bearerSessions.live("live", now.Add(time.Hour-time.Minute)), "replica a now ends it at the refreshed bearer's exp")
}

// TestBindBearerSession_FailedRecordIsTriedAgain: a refreshed bearer whose
// exp the shared record did not take (Valkey did not answer) is recorded on
// the session's next request, so the other replica does not end the session
// at the old exp.
func TestBindBearerSession_FailedRecordIsTriedAgain(t *testing.T) {
	_, b, _ := twoBearerReplicas(t)
	expiries := b.bearerExpiries
	refreshed := time.Unix(time.Now().Add(time.Hour).Unix(), 0)

	b.bearerExpiries = unreadableBearerExpiries{}
	bindBearer(t, b, "live", refreshed)
	b.bearerExpiries = expiries
	bindBearer(t, b, "live", refreshed)

	latest, err := expiries.latest(context.Background(), "live")
	require.NoError(t, err)
	assert.Equal(t, refreshed, latest)
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
