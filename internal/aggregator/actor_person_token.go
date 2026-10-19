package aggregator

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/credprovider"
	"github.com/giantswarm/muster/v5/internal/server"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// personTokenRefreshBackoff spaces the refreshes one person's expired login
// token gets from actor requests, so a person whose logins cannot be
// refreshed does not cost an upstream round trip per MCP call.
const personTokenRefreshBackoff = 30 * time.Second

// personTokenRefreshes records, per person, when an actor request last
// refreshed their logins. The zero value is ready to use.
type personTokenRefreshes struct {
	mu   sync.Mutex
	last map[string]time.Time
	now  func() time.Time
}

// due reports whether the person's logins may be refreshed now, and if so
// records the attempt.
func (r *personTokenRefreshes) due(subject string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.now != nil {
		now = r.now()
	}
	if last, ok := r.last[subject]; ok && now.Sub(last) < personTokenRefreshBackoff {
		return false
	}
	if r.last == nil {
		r.last = map[string]time.Time{}
	}
	for sub, last := range r.last {
		if now.Sub(last) >= personTokenRefreshBackoff {
			delete(r.last, sub)
		}
	}
	r.last[subject] = now
	return true
}

// withPersonIDToken puts the bound person's login ID token on an actor
// request, so the servers that forward it (forwardToken, forwardIdentity) or
// exchange it (tokenExchange) connect for the actor as the person, and files
// it as the actor session's login token, where those servers' background
// connections look it up. The token is the one that lives longest among the
// person's own live muster logins; an expired one is renewed by refreshing those
// logins at their identity provider. Without one the request is served as
// before and those servers stay unconnected for the actor.
func (a *AggregatorServer) withPersonIDToken(ctx context.Context, caller *credprovider.ActorCaller) context.Context {
	oh := api.GetOAuthHandler()
	issuer := a.getMusterIssuer()
	if oh == nil || !oh.IsEnabled() || issuer == "" {
		return ctx
	}
	token := a.personLoginIDToken(ctx, oh, caller.Subject, issuer)
	if token == nil {
		// A person who signed out takes the copy out of the actor session too.
		api.ClearLoginIDToken(oh, caller.SessionID, issuer)
		logging.DebugWithAttrs("Aggregator", "Actor bearer: no live login ID token of the bound person",
			slog.String("sub", logging.TruncateIdentifier(caller.Subject)),
			slog.String("session", caller.Session))
		return ctx
	}
	// Filed without the person's user ID: the copy is no login of the person
	// and never serves another actor request as one.
	api.StoreLoginIDToken(oh, caller.SessionID, "", issuer, token.IDToken, token.ExpiresAt)
	return server.ContextWithIDToken(ctx, token.IDToken)
}

// personLoginIDToken returns the person's longest-lived live login ID token,
// refreshing their logins this replica serves when none is live.
func (a *AggregatorServer) personLoginIDToken(ctx context.Context, oh api.OAuthHandler, subject, issuer string) *api.OAuthToken {
	if token := api.PersonLoginIDToken(oh, subject, issuer); token != nil {
		return token
	}
	refresh := a.sessionRefresher()
	if refresh == nil || a.subjectSessions == nil || !a.personTokenRefreshes.due(subject) {
		return nil
	}
	for _, sessionID := range a.subjectSessions.OAuthSessionIDsForSubject(subject) {
		if credprovider.IsActorSession(sessionID) {
			continue
		}
		if err := refresh(ctx, sessionID); err != nil {
			logging.DebugWithAttrs("Aggregator", "Actor bearer: refreshing a login of the bound person failed",
				slog.String("session", logging.TruncateIdentifier(sessionID)),
				slog.String("error", err.Error()))
			continue
		}
		if token := api.PersonLoginIDToken(oh, subject, issuer); token != nil {
			return token
		}
	}
	return nil
}
