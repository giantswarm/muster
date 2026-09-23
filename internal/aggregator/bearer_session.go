package aggregator

import (
	"context"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// Sessions whose authority is a forwarded bearer.
//
// A caller that presents an IdP-issued JWT -- an agent's on-behalf-of token,
// a workload token accepted through trustedIssuers -- gets a session keyed by
// that very token: mcp-oauth derives the session ID ("ext-<digest>") from it,
// and muster issues only opaque tokens, so a decodable bearer is always such
// a token. The session has no refresh chain. The bearer is its only
// credential, a request presenting it after its exp is refused before it
// reaches muster's handlers, and the caller's next token opens another
// session. The session's authority therefore ends at the bearer's exp, an
// instant muster reads from the token the moment it arrives.
//
// bearerSessions ends such a session at that instant, once: its pooled
// backend connections are closed, which stops their listen streams and takes
// them out of the capability poll, its auth marks and capability entries are
// dropped, and a server whose last connection it was goes back to Awaiting
// Session. Left alone, the connections would keep sending the expired token --
// on every poll and every listen-stream reconnect -- until three rejections
// evicted each of them.
//
// exp is wall time, like every JWT check, so the timers run on the system
// clock, not the process clock the test harness advances.

// bearerSessions tracks the sessions bound to a forwarded bearer and ends
// each when its bearer expires. The zero value is not usable; see
// newBearerSessions. Safe for concurrent use.
type bearerSessions struct {
	mu       sync.Mutex
	sessions map[string]*bearerSession
	stopped  bool
}

// bearerSession is one bound session: the bearer's exp and the timer that
// ends the session at it.
type bearerSession struct {
	exp   time.Time
	timer *time.Timer
}

func newBearerSessions() *bearerSessions {
	return &bearerSessions{sessions: make(map[string]*bearerSession)}
}

// bind records that sessionID's authority ends at exp and schedules end for
// that instant (at once when exp has passed). The session is keyed by its
// bearer, so its exp never changes: a session already bound is left as it is,
// which keeps the per-request call to a map lookup.
func (b *bearerSessions) bind(sessionID string, exp time.Time, end func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return
	}
	if _, ok := b.sessions[sessionID]; ok {
		return
	}
	b.sessions[sessionID] = &bearerSession{
		exp:   exp,
		timer: time.AfterFunc(time.Until(exp), end),
	}
}

// bound reports whether sessionID is bound to a bearer.
func (b *bearerSessions) bound(sessionID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.sessions[sessionID]
	return ok
}

// expired reports whether sessionID is bound to a bearer whose exp has
// passed at now.
func (b *bearerSessions) expired(sessionID string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sessions[sessionID]
	return ok && !now.Before(s.exp)
}

// release forgets sessionID when its bearer has expired at now and returns
// the exp, so exactly one of the paths that notice the expiry (the timer, the
// capability poll, a connection's header func) ends the session.
func (b *bearerSessions) release(sessionID string, now time.Time) (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sessions[sessionID]
	if !ok || now.Before(s.exp) {
		return time.Time{}, false
	}
	s.timer.Stop()
	delete(b.sessions, sessionID)
	return s.exp, true
}

// stop cancels every pending end and refuses new bindings, for shutdown: the
// connection pool is drained there anyway.
func (b *bearerSessions) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
	for id, s := range b.sessions {
		s.timer.Stop()
		delete(b.sessions, id)
	}
}

// bindBearerSession binds a session authenticated with a forwarded bearer to
// that bearer's exp. A session without one (opaque-token human sessions,
// which refresh through their token family) and a bearer without a parseable
// exp are left to the header func's strike counter.
func (a *AggregatorServer) bindBearerSession(sso ssoSession) {
	if a.bearerSessions == nil || sso.sessionID == "" || !isForwardableToken(sso.tokens.Bearer) {
		return
	}
	if a.bearerSessions.bound(sso.sessionID) {
		return
	}
	exp, err := pkgoauth.Expiry(sso.tokens.Bearer)
	if err != nil {
		logging.Debug("Aggregator", "Session %s: forwarded bearer has no parseable exp, not bound to it: %v",
			logging.TruncateIdentifier(sso.sessionID), err)
		return
	}
	sessionID := sso.sessionID
	a.bearerSessions.bind(sessionID, exp, func() { a.endBearerSession(sessionID) })
}

// endExpiredBearerSession ends sessionID when it is bound to a forwarded
// bearer that has expired, and reports whether it was: the capability poll
// asks it before it lists through one of the session's connections.
func (a *AggregatorServer) endExpiredBearerSession(sessionID string) bool {
	if a.bearerSessions == nil || !a.bearerSessions.expired(sessionID, time.Now()) {
		return false
	}
	a.endBearerSession(sessionID)
	return true
}

// endBearerSession ends a session whose forwarded bearer has expired, once:
// see bearerSessions. A call before the exp, or after another path ended the
// session, does nothing.
func (a *AggregatorServer) endBearerSession(sessionID string) {
	if a.bearerSessions == nil || a.ctx != nil && a.ctx.Err() != nil {
		return
	}
	exp, ok := a.bearerSessions.release(sessionID, time.Now())
	if !ok {
		return
	}

	var servers []string
	if a.connPool != nil {
		for _, pooled := range a.connPool.Snapshot(sessionID) {
			servers = append(servers, pooled.ServerName)
		}
	}
	log := logging.Debug
	if len(servers) > 0 {
		log = logging.Info
	}
	log("Aggregator", "Session %s ended: its forwarded bearer expired at %s; closed %d backend connections",
		logging.TruncateIdentifier(sessionID), exp.UTC().Format(time.RFC3339), len(servers))

	a.tearDownSession(context.Background(), sessionID)
	for _, name := range servers {
		if a.connPool != nil && a.connPool.HasServer(name) {
			// Another session still holds a live connection, so the
			// server-level state remains whatever that connection proves.
			continue
		}
		a.notifyMCPServerAuthRequired(name, "the forwarded bearer of its last session expired")
	}
}
