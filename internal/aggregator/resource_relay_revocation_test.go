package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/server"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResourceRelays_EvictionEndsOnlyRevokedRelays pins that a pool eviction
// re-checks the relays it names and ends those whose session lost the server,
// and leaves an authorized one -- a credential renewal -- and unnamed ones
// running.
func TestResourceRelays_EvictionEndsOnlyRevokedRelays(t *testing.T) {
	a := newTestAggregatorWithPool(t)
	r := a.relayRegistry()

	type relay struct {
		ctx    context.Context
		remove func()
	}
	open := func(sessionID, server string, authorized bool) relay {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		return relay{ctx: ctx, remove: r.add(sessionID, server, func() bool { return authorized }, cancel)}
	}
	ended := func(rel relay) bool { return rel.ctx.Err() != nil }

	revoked := open("s1", "mailbox", false)
	renewed := open("s1", "other", true)
	otherSession := open("s2", "mailbox", false)

	a.connPool.Evict("s1", "mailbox")
	require.Eventually(t, func() bool { return ended(revoked) }, 5*time.Second, 5*time.Millisecond)
	assert.False(t, ended(otherSession), "an eviction of another session leaves the relay")

	a.connPool.EvictSession("s1")
	assert.Never(t, func() bool { return ended(renewed) }, 50*time.Millisecond, 5*time.Millisecond,
		"a relay whose session still reaches the server runs on")

	a.connPool.EvictServer("mailbox")
	require.Eventually(t, func() bool { return ended(otherSession) }, 5*time.Second, 5*time.Millisecond)

	removed := open("s3", "mailbox", false)
	removed.remove()
	a.connPool.Evict("s3", "mailbox")
	assert.Never(t, func() bool { return ended(removed) }, 50*time.Millisecond, 5*time.Millisecond,
		"a closed relay is no longer reached")
}

// TestRelayAuthorized_ExpiredForwardedBearer pins that a relay riding on a
// forwarded bearer ends with the bearer.
func TestRelayAuthorized_ExpiredForwardedBearer(t *testing.T) {
	a := newSubscriptionAggregator(t, "http://unused.invalid/mcp")
	info, ok := a.registry.GetServerInfo("mailbox")
	require.True(t, ok)
	withBearer := func(exp time.Time) context.Context {
		ctx := server.ContextWithBearerToken(context.Background(), unsignedJWT(t, map[string]any{"sub": "alice", "exp": exp.Unix()}))
		return api.WithSessionID(ctx, "session-alice")
	}
	assert.True(t, a.relayAuthorized(withBearer(time.Now().Add(time.Hour)), info, "session-alice", "alice"))
	assert.False(t, a.relayAuthorized(withBearer(time.Now().Add(-time.Minute)), info, "session-alice", "alice"))
}

// TestRelaySubscriptionsListen_SignOutEndsTheRelay pins the whole path: a
// session that loses the server (its auth mark revoked, its connection
// evicted, as sign-out does) has its backend stream closed although its own
// stream is still open.
func TestRelaySubscriptionsListen_SignOutEndsTheRelay(t *testing.T) {
	backend, url := newMailboxBackend(t, false)
	a := newSubscriptionAggregator(t, url, "alice")
	alice := newSubscriber(t, "alice")
	require.NoError(t, a.relaySubscriptionsListen(alice.ctx, 1, listenMessage(t, 1, "test://mailbox/alice")))
	require.Equal(t, 1, backend.openStreams())

	// A forwarded bearer is its own credential: the relay rides on it until
	// it expires. Revoke what the relay checks.
	a.relayRegistry().mu.Lock()
	for _, rel := range a.relays.relays {
		rel.authorized = func() bool { return false }
	}
	a.relayRegistry().mu.Unlock()
	a.connPool.Evict("session-alice", "mailbox")
	require.Eventually(t, func() bool { return backend.openStreams() == 0 }, 10*time.Second, 10*time.Millisecond,
		"the backend stream closes with the session's access")
	assert.NoError(t, alice.ctx.Err(), "the client's own stream is not ended by muster")
}
