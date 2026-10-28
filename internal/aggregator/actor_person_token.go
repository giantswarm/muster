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
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// pinnedLoginRefreshBackoff spaces the refreshes one pinned login gets from
// actor requests, so a login whose refresh token is rejected does not cost an
// upstream round trip per MCP call.
const pinnedLoginRefreshBackoff = 30 * time.Second

// pinnedLoginRefreshes records, per login family, when an actor request last
// refreshed it. The zero value is ready to use.
type pinnedLoginRefreshes struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// due reports whether the family may be refreshed now, and if so records the
// attempt.
func (r *pinnedLoginRefreshes) due(familyID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if last, ok := r.last[familyID]; ok && now.Sub(last) < pinnedLoginRefreshBackoff {
		return false
	}
	if r.last == nil {
		r.last = map[string]time.Time{}
	}
	for id, last := range r.last {
		if now.Sub(last) >= pinnedLoginRefreshBackoff {
			delete(r.last, id)
		}
	}
	r.last[familyID] = now
	return true
}

// withPersonIDToken puts the login ID token of the session the actor's
// binding pins on an actor request, so the servers that forward it
// (forwardToken, forwardIdentity) or exchange it (tokenExchange) connect for
// the actor as the person, and files it as the actor session's login token,
// where those servers' background connections look it up. Only the pinned
// session's own token serves: a login family's, renewed by refreshing that
// family when it expired (mcp-oauth refuses a revoked one), or the latest
// token a client forwarded in a forwarded session, which muster cannot renew. Without one, and for a
// binding that pins none, the request carries no person token and the
// actor's copy is cleared.
func (a *AggregatorServer) withPersonIDToken(ctx context.Context, caller *credprovider.ActorCaller) context.Context {
	oh := api.GetOAuthHandler()
	issuer := a.getMusterIssuer()
	if oh == nil || !oh.IsEnabled() || issuer == "" {
		return ctx
	}
	token := a.pinnedLoginIDToken(ctx, oh, caller.LoginSession, issuer)
	if token != nil && api.IsForwardedSession(caller.LoginSession) && !issuedTo(token.IDToken, caller.LoginIdentity) {
		token = nil
	}
	if token == nil {
		api.ClearLoginIDToken(oh, caller.SessionID, issuer)
		logging.DebugWithAttrs("Aggregator", "Actor bearer: no live login ID token of the pinned session",
			slog.String("login_session", logging.TruncateIdentifier(caller.LoginSession)),
			slog.String("session", caller.Session))
		return ctx
	}
	// Filed without the person's user ID: the copy is no login of the person
	// and is never indexed as one.
	api.StoreLoginIDToken(oh, caller.SessionID, "", issuer, token.IDToken, token.ExpiresAt)
	return server.ContextWithIDToken(ctx, token.IDToken)
}

// pinnedLoginIDToken returns the pinned session's live login ID token,
// refreshing a login family's when none is live.
func (a *AggregatorServer) pinnedLoginIDToken(ctx context.Context, oh api.OAuthHandler, loginSession, issuer string) *api.OAuthToken {
	if loginSession == "" {
		return nil
	}
	if token := api.SessionLoginIDToken(oh, loginSession, issuer); token != nil {
		return token
	}
	if api.IsForwardedSession(loginSession) {
		return nil
	}
	refresh := a.sessionRefresher()
	if refresh == nil || !a.pinnedLoginRefreshes.due(loginSession) {
		return nil
	}
	if err := refresh(ctx, loginSession); err != nil {
		logging.DebugWithAttrs("Aggregator", "Actor bearer: refreshing the pinned login failed",
			slog.String("login_session", logging.TruncateIdentifier(loginSession)),
			slog.String("error", err.Error()))
		return nil
	}
	return api.SessionLoginIDToken(oh, loginSession, issuer)
}

// issuedTo reports whether a forwarded session's token was issued to the
// issuer and subject of the token that pinned the session. A forwarded
// session's ID is a truncated hash, no authorization key, so the slot it
// names is checked against the binding before its token serves the actor.
func issuedTo(idToken string, login api.SignInIdentity) bool {
	if login.Issuer == "" || login.Subject == "" {
		return false
	}
	iss, err := pkgoauth.Issuer(idToken)
	if err != nil || iss != login.Issuer {
		return false
	}
	sub, err := pkgoauth.Subject(idToken)
	return err == nil && sub == login.Subject
}
