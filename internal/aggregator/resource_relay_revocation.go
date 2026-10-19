package aggregator

import (
	"context"
	"log/slog"
	"sync"

	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// resourceRelays holds the open relays of resource subscriptions, so a
// revocation reaches them: every path that takes a session's access to a
// server away -- sign-out, a lost or refused credential, the session's end, a
// server's sign-in reset -- evicts the session's pooled connection, and the
// pool tells resourceRelays (SetEvictionListener). Each relay the eviction
// names checks the session's access again and ends when it is gone; an
// eviction that only renews a credential leaves it running.
type resourceRelays struct {
	mu     sync.Mutex
	next   uint64
	relays map[uint64]*resourceRelay
}

// resourceRelay is one backend stream of one session.
type resourceRelay struct {
	sessionID, server string
	authorized        func() bool
	cancel            context.CancelFunc
}

// relayRegistry returns the aggregator's relays, created with the first one
// and listening to the pool's evictions from then on.
func (a *AggregatorServer) relayRegistry() *resourceRelays {
	a.relaysOnce.Do(func() {
		a.relays = &resourceRelays{relays: make(map[uint64]*resourceRelay)}
		if a.connPool != nil {
			a.connPool.SetEvictionListener(a.relays.evicted)
		}
	})
	return a.relays
}

// add registers a relay; the returned func removes it.
func (r *resourceRelays) add(sessionID, server string, authorized func() bool, cancel context.CancelFunc) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	id := r.next
	r.relays[id] = &resourceRelay{sessionID: sessionID, server: server, authorized: authorized, cancel: cancel}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.relays, id)
	}
}

// evicted re-checks the relays of a pool eviction: of sessionID to server,
// "" matching any. The checks run on a goroutine of their own: an eviction
// may come from inside a backend connection's own request.
func (r *resourceRelays) evicted(sessionID, server string) {
	r.mu.Lock()
	var hit []*resourceRelay
	for _, rel := range r.relays {
		if (sessionID == "" || rel.sessionID == sessionID) && (server == "" || rel.server == server) {
			hit = append(hit, rel)
		}
	}
	r.mu.Unlock()
	if len(hit) == 0 {
		return
	}
	go func() {
		for _, rel := range hit {
			if rel.authorized() {
				continue
			}
			logging.InfoWithAttrs("Aggregator", "Relayed resource subscription ended: the session no longer reaches the server",
				slog.String("server", rel.server),
				slog.String("sessionID", logging.TruncateIdentifier(rel.sessionID)))
			rel.cancel()
		}
	}()
}

// relayAuthorized reports whether a relay opened with ctx may go on: the
// session still passes requireSessionAuth, and a forwarded bearer it rides
// on has not expired.
func (a *AggregatorServer) relayAuthorized(ctx context.Context, info *ServerInfo, sessionID, sub string) bool {
	if bearer := forwardableBearer(ctx); bearer != "" {
		if expired, _ := pkgoauth.IsExpired(bearer); expired {
			return false
		}
	}
	return a.requireSessionAuth(ctx, info, sessionID, sub) == nil
}
