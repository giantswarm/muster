package aggregator

import (
	"context"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// JWT resource access tokens bound the lifetime of their authenticated session.
// Their backend grants are independent; closing pooled connections at expiry
// prevents a backend grant from outliving the caller's session authority.
// JWT expiry uses wall time, independently of the harness process clock.
//
// A forwarded or trusted-issuer bearer's session is keyed by its principal,
// not its bytes (ForwardedSessionIdentityPrincipal), so a client that forwards
// a refreshed token for the same person keeps the session. The session then
// ends at the exp of the latest validated bearer muster saw for it: a later
// exp moves the end, an earlier or equal one changes nothing. A replica that
// saw only an older bearer reads the latest exp the replicas share
// (bearerExpiryStore) before it ends the session.

// bearerSessionStoreTimeout bounds a read or write of the shared bearer exp.
const bearerSessionStoreTimeout = 5 * time.Second

// bearerSessions tracks the sessions bound to a resource bearer and ends
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
// that instant (at once when exp has passed). A session already bound moves
// to exp only when exp is later than its bound exp: the old timer stops and a
// new one starts. It reports whether the binding was made or moved.
func (b *bearerSessions) bind(sessionID string, exp time.Time, end func()) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return false
	}
	if s, ok := b.sessions[sessionID]; ok {
		if !exp.After(s.exp) {
			return false
		}
		s.timer.Stop()
		s.exp = exp
		s.timer = time.AfterFunc(time.Until(exp), end)
		return true
	}
	b.sessions[sessionID] = &bearerSession{
		exp:   exp,
		timer: time.AfterFunc(time.Until(exp), end),
	}
	return true
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
// capability poll) ends the session.
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

// bindBearerSession bounds a JWT-authenticated session by its resource token's
// expiry. Opaque access tokens use the authorization server's session lifecycle.
// The bearer reached here through the OAuth middleware, which validated it
// (signature, issuer, audience, exp): only a valid token moves the binding.
func (a *AggregatorServer) bindBearerSession(ctx context.Context, sso ssoSession) {
	if a.bearerSessions == nil || sso.sessionID == "" || !isJWTBearer(sso.tokens.Bearer) {
		return
	}
	exp, err := pkgoauth.Expiry(sso.tokens.Bearer)
	if err != nil {
		logging.Debug("Aggregator", "Session %s: resource bearer has no parseable exp, not bound to it: %v",
			logging.TruncateIdentifier(sso.sessionID), err)
		return
	}
	a.bindBearerExpiry(ctx, sso.sessionID, exp)
}

// bindBearerExpiry binds sessionID to exp, or moves its binding to exp when
// exp is later, and then records exp for the other replicas.
func (a *AggregatorServer) bindBearerExpiry(ctx context.Context, sessionID string, exp time.Time) {
	if !a.bearerSessions.bind(sessionID, exp, func() { a.endBearerSession(sessionID) }) {
		return
	}
	if a.bearerExpiries == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bearerSessionStoreTimeout)
	defer cancel()
	if err := a.bearerExpiries.extend(ctx, sessionID, exp); err != nil {
		// The other replica then ends the session at the exp it saw; the
		// session's next request connects it again there.
		logging.Warn("Aggregator", "Session %s: bearer exp not shared with the other replicas: %v",
			logging.TruncateIdentifier(sessionID), err)
	}
}

// endExpiredBearerSession ends sessionID when it is bound to a forwarded
// bearer that has expired, and reports whether it was: the capability poll
// asks it before it lists through one of the session's connections.
func (a *AggregatorServer) endExpiredBearerSession(sessionID string) bool {
	if a.bearerSessions == nil || !a.bearerSessions.expired(sessionID, time.Now()) {
		return false
	}
	return a.endBearerSession(sessionID)
}

// endBearerSession ends a session whose resource bearer has expired, once:
// see bearerSessions. A call before the exp, or after another path ended the
// session, does nothing. A session for which another replica recorded a
// later, still valid bearer exp is not ended: its binding moves to that exp.
// It reports whether it ended the session.
func (a *AggregatorServer) endBearerSession(sessionID string) bool {
	if a.bearerSessions == nil || a.ctx != nil && a.ctx.Err() != nil {
		return false
	}
	if !a.bearerSessions.expired(sessionID, time.Now()) {
		return false
	}
	if latest, ok := a.latestSharedBearerExpiry(sessionID); ok && time.Now().Before(latest) {
		logging.Debug("Aggregator", "Session %s: a later resource bearer reached another replica; it now ends at %s",
			logging.TruncateIdentifier(sessionID), latest.UTC().Format(time.RFC3339))
		a.bearerSessions.bind(sessionID, latest, func() { a.endBearerSession(sessionID) })
		return false
	}
	exp, ok := a.bearerSessions.release(sessionID, time.Now())
	if !ok {
		return false
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
	log("Aggregator", "Session %s ended: its resource bearer expired at %s; closed %d backend connections",
		logging.TruncateIdentifier(sessionID), exp.UTC().Format(time.RFC3339), len(servers))

	a.tearDownSession(context.Background(), sessionID)
	for _, name := range servers {
		if a.connPool != nil && a.connPool.HasServer(name) {
			// Another session still holds a live connection, so the
			// server-level state remains whatever that connection proves.
			continue
		}
		a.notifyMCPServerAuthRequired(name, "the resource bearer of its last session expired")
	}
	return true
}

// latestSharedBearerExpiry returns the latest bearer exp the replicas
// recorded for sessionID. A store that does not answer gives no exp: the
// session then ends at the exp this replica saw, so no session outlives a
// bearer muster cannot show was valid.
func (a *AggregatorServer) latestSharedBearerExpiry(sessionID string) (time.Time, bool) {
	if a.bearerExpiries == nil {
		return time.Time{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), bearerSessionStoreTimeout)
	defer cancel()
	latest, err := a.bearerExpiries.latest(ctx, sessionID)
	if err != nil {
		logging.Warn("Aggregator", "Session %s: shared bearer exp not read, the session ends at this replica's: %v",
			logging.TruncateIdentifier(sessionID), err)
		return time.Time{}, false
	}
	return latest, !latest.IsZero()
}
