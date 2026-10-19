package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// requireSessionAuth answers whether the session may open a connection of its
// own to a server that requires per-session authentication: it is
// authenticated to it, forwards a validated bearer to a token-forwarding
// server, or holds a subject-scoped grant it adopts now. Otherwise the error
// is a *sessionNotAuthenticatedError.
func (a *AggregatorServer) requireSessionAuth(ctx context.Context, serverInfo *ServerInfo, sessionID, sub string) error {
	serverName := serverInfo.Name
	if a.authStore == nil {
		return fmt.Errorf("auth store not initialized")
	}
	authenticated, _ := a.authStore.IsAuthenticated(ctx, sessionID, serverName)
	if !authenticated && a.awaitServerBootstrap(ctx, sessionID, serverName) > 0 {
		// The session's fan-out was connecting this very server: judge the
		// finished connect, not the moment before it.
		authenticated, _ = a.authStore.IsAuthenticated(ctx, sessionID, serverName)
	}
	if !authenticated {
		// Token-forwarding servers need no per-server auth entry when the
		// request itself carries a forwardable validated bearer: that bearer
		// is the credential the connection forwards. Without this, a
		// stale-token eviction that revoked the entry makes calls fail until
		// the asynchronous SSO re-init completes, even though the caller
		// presents a fresh token.
		if !ShouldUseTokenForwarding(serverInfo) || forwardableBearer(ctx) == "" {
			// A subject-scoped grant (GitHub-style connectors) belongs to the
			// person, so a session that never called core_auth_login for the
			// server still holds the consent: connect it now instead of
			// sending the caller back to a login it already completed.
			adopted, adoptErr := a.adoptSubjectGrant(ctx, serverInfo, sessionID, sub)
			if adoptErr != nil {
				return &sessionNotAuthenticatedError{server: serverName, cause: adoptErr}
			}
			if !adopted {
				return &sessionNotAuthenticatedError{server: serverName}
			}
		}
	}

	return nil
}

// sessionClient is a connection newSessionClient opened for a session, and
// what the pool records next to it.
type sessionClient struct {
	client         MCPClient
	tokenExpiry    time.Time
	exchangedToken string
}

// newSessionClient builds and initializes a connection of the session to a
// server that requires per-session authentication, as the session: token
// exchange, token forwarding or the session's OAuth grant, by the server's
// auth config. It is not pooled. listen puts the client in listen mode
// (internalmcp.ResourceListener) before its handshake.
func (a *AggregatorServer) newSessionClient(ctx context.Context, serverInfo *ServerInfo, sessionID, sub string, listen bool) (*sessionClient, error) {
	serverName := serverInfo.Name
	var client MCPClient
	var tokenExpiry time.Time
	var exchangedToken string
	var forwardedToken string

	if ShouldUseTokenExchange(serverInfo) {
		var err error
		client, tokenExpiry, exchangedToken, err = a.exchangeTokenAndCreateClient(ctx, serverInfo, sessionID)
		if err != nil {
			return nil, err
		}

	} else if ShouldUseTokenForwarding(serverInfo) {
		onStaleToken := func() {
			if a.connPool != nil {
				a.connPool.Evict(sessionID, serverName)
				logging.InfoWithAttrs("Aggregator", "Evicted stale token-forwarding connection",
					slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
					slog.String("server", serverName))
			}
		}
		var err error
		client, forwardedToken, err = a.newTokenForwardingClient(ctx, sessionID, sub, a.getMusterIssuer(), serverInfo, onStaleToken)
		if err != nil {
			return nil, err
		}

	} else if oauthProtected(serverInfo) {
		oauthHandler := api.GetOAuthHandler()
		if oauthHandler == nil || !oauthHandler.IsEnabled() {
			return nil, fmt.Errorf("OAuth handler not available for %s", serverName)
		}

		// What the 401 discovered, else the operator's pin. A server that
		// registered from its 401 alone after a restart and has no pin knows
		// neither: resolve its RFC 9728 metadata once and record it on the
		// entry, so a session that is already authenticated -- and therefore
		// never runs core_auth_login again -- can still open its connection.
		issuer, scope := sessionOAuthIssuer(serverInfo)
		if issuer == "" {
			authInfo, err := a.resolveServerAuthInfo(ctx, serverInfo)
			if err != nil {
				return nil, fmt.Errorf("resolve the authorization server of %s: %w", serverName, err)
			}
			issuer, scope = authInfo.Issuer, authInfo.Scope
		}
		if issuer == "" {
			return nil, fmt.Errorf("unable to determine auth method for server %s: its OAuth issuer is unknown (no RFC 9728 metadata, no spec.auth.authorizationServer pin)", serverName)
		}
		headers, err := sessionHeaders(ctx, serverInfo)
		if err != nil {
			return nil, err
		}
		tokenStore := internalmcp.NewMusterTokenStore(sessionID, sub, issuer, oauthHandler)
		clientID, clientSecret := oauthHandler.GetClientCredentialsForIssuer(ctx, issuer)
		// The same client establishConnection builds at login: the server's
		// spec.headers, spec.meta and, with spec.auth.forwardIdentity, the
		// session's ID token on every request and its spec.timeout as the
		// budget of every operation, or a session that is already
		// authenticated -- and whose pooled connection is gone -- would run
		// its calls under the defaults the login path does not.
		client = internalmcp.NewDynamicAuthClient(serverInfo.URL, tokenStore, scope, clientID, clientSecret).
			WithHeaders(headers).
			WithHeaderFunc(a.identityHeaderFunc(sessionID, serverInfo)).
			WithMeta(serverInfo.Meta).
			WithTimeout(serverInfo.Timeout).
			WithMaxCallDuration(serverInfo.MaxCallDuration).
			WithAuthLossHandler(a.makeSessionAuthLossHandler(sessionID, serverName))

	} else {
		return nil, fmt.Errorf("unable to determine auth method for server %s", serverName)
	}

	if listen {
		listener, ok := client.(internalmcp.ResourceListener)
		if !ok {
			return nil, fmt.Errorf("%s: %w", serverName, internalmcp.ErrSubscriptionsListenUnsupported)
		}
		listener.UseSubscriptionsListen()
	}

	// Initialize the on-demand client
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		if forwardedToken != "" {
			// A backend rejection is indistinguishable from other transport
			// failures here, so the token diagnostic is attached to every
			// token-forwarding connect failure.
			logging.WarnWithAttrs("Aggregator", "Token-forwarding client rejected on connect",
				slog.String("server", serverName),
				slog.String("error", err.Error()),
				slog.String("diagnostic", forwardedTokenDiagnostic(forwardedToken, err)))
		}
		return nil, fmt.Errorf("failed to initialize on-demand client for %s: %w", serverName, err)
	}

	return &sessionClient{client: client, tokenExpiry: tokenExpiry, exchangedToken: exchangedToken}, nil
}
