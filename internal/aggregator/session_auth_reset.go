package aggregator

import (
	"context"
	"log/slog"
	"reflect"
	"slices"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// sessionAuthInvalidated reports whether an MCPServer's auth configuration
// changed from previous to current in a way that invalidates the connections
// live sessions made under previous, and names the field that changed.
//
// A session's connection to a server is made from the credentials the
// configuration selects: the session's forwarded login token (forwardToken),
// a token exchanged for it (tokenExchange, with its endpoint, connector and
// client), or a grant from the pinned authorization server (issuer, endpoints,
// expected issuer, grant scope, client Secret, scopes), with or without the
// login token next to it (forwardIdentity). When that selection
// changes, the connections and the "authenticated" marks the sessions hold
// describe a credential the server no longer accepts; they have to go, or
// every session keeps a Connected status whose calls fail (#1276). A first
// registration (previous nil) invalidates nothing: no session has connected
// under a configuration that did not exist.
func sessionAuthInvalidated(previous, current *api.MCPServerAuth) (bool, string) {
	if previous == nil {
		return false, ""
	}
	prev, cur := authOrEmpty(previous), authOrEmpty(current)
	switch {
	case prev.ForwardToken != cur.ForwardToken:
		return true, "forwardToken"
	case prev.ForwardIdentity != cur.ForwardIdentity:
		return true, "forwardIdentity"
	case !slices.Equal(prev.RequiredAudiences, cur.RequiredAudiences):
		return true, "requiredAudiences"
	case !sameTokenExchange(prev.TokenExchange, cur.TokenExchange):
		return true, "tokenExchange"
	case !sameAuthorizationServer(prev.AuthorizationServer, cur.AuthorizationServer):
		return true, "authorizationServer"
	}
	return false, ""
}

// authOrEmpty reads a nil auth configuration as the empty one, the way the
// registry stores it (RegisterPendingAuth).
func authOrEmpty(auth *api.MCPServerAuth) *api.MCPServerAuth {
	if auth == nil {
		return &api.MCPServerAuth{}
	}
	return auth
}

// sameTokenExchange compares two token-exchange configurations by their spec
// fields alone: the client credentials resolved from the Secret at runtime
// are stamped onto the running service's copy and are not a change.
func sameTokenExchange(a, b *api.TokenExchangeConfig) bool {
	if a == nil || b == nil {
		return a == b
	}
	return reflect.DeepEqual(a.SpecOnly(), b.SpecOnly())
}

// resetSessionAuth puts the server back to auth_required for every live
// session after its auth configuration changed (changed names the field):
// the authenticated marks and the pooled clients go, and so do the SSO
// pending and failure records made under the previous configuration. The
// cached capabilities stay: they keep the server's tools resolvable for the
// session, so its next call answers with the sign-in (authRequiredAnswer)
// instead of "tool not found", and the next connection replaces them.
//
// Sessions on other replicas share the stores, so their marks and records go
// with these; their pooled clients are closed by the reset their own replica
// runs when the same registration reaches it.
func (a *AggregatorServer) resetSessionAuth(ctx context.Context, serverName, changed string) {
	a.revokeServerSessions(ctx, serverName)
	if a.ssoTracker != nil {
		a.ssoTracker.ClearServer(serverName)
	}
	logging.InfoWithAttrs("Aggregator", "session_auth_reset",
		slog.String("server", serverName),
		slog.String("changed", "auth."+changed))
}

// revokeServerSessions removes the authenticated mark and closes the pooled
// client of every session for one server. What the sessions' capability
// caches hold for it is the caller's decision.
func (a *AggregatorServer) revokeServerSessions(ctx context.Context, serverName string) {
	if a.authStore != nil {
		if err := a.authStore.RevokeServer(ctx, serverName); err != nil {
			logging.WarnWithAttrs("Aggregator", "Failed to revoke auth for server",
				slog.String("server", serverName), slog.String("error", err.Error()))
		}
	}
	if a.connPool != nil {
		a.connPool.EvictServer(serverName)
	}
}
