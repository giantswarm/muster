package aggregator

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

// subjectGrantConnectTimeout bounds one connect made with a person's grant.
// The connect runs on a context detached from the caller's cancellation
// because concurrent callers of the same session share it (singleflight):
// the first caller giving up must not fail the others.
const subjectGrantConnectTimeout = 30 * time.Second

// holdsSubjectGrants reports whether a session may connect to this server
// with a grant another session of the same person completed: the server
// authenticates per session through a browser-consent login (not SSO, which
// connects on its own), and its authorization server files grants under the
// person (spec.auth.authorizationServer.grantScope: subject).
func holdsSubjectGrants(info *ServerInfo) bool {
	if info == nil || !info.RequiresSessionAuth() {
		return false
	}
	if ShouldUseTokenExchange(info) || ShouldUseTokenForwarding(info) {
		return false
	}
	return info.AuthConfig.AuthorizationServer.SubjectScoped()
}

// sessionOAuthIssuer returns the issuer and scope a session's tokens for the
// server are filed under: what the 401 discovered, else what the operator
// pinned in spec.auth.authorizationServer. The 401 yields nothing for an
// authorization server without a discovery document (GitHub), so after a
// restart the registry entry carries only the pin; a session already
// authenticated to the server -- its mark and grant persisted -- must still
// be able to build a connection from it.
func sessionOAuthIssuer(info *ServerInfo) (issuer, scope string) {
	if info == nil {
		return "", ""
	}
	if info.AuthInfo != nil {
		issuer, scope = info.AuthInfo.Issuer, info.AuthInfo.Scope
	}
	if info.AuthConfig == nil || info.AuthConfig.AuthorizationServer == nil {
		return issuer, scope
	}
	as := info.AuthConfig.AuthorizationServer
	if issuer == "" {
		issuer = strings.TrimSuffix(as.Issuer, "/")
	}
	if scope == "" {
		scope = as.Scopes
	}
	return issuer, scope
}

// adoptSubjectGrant connects sessionID to the server with the grant the
// person (sub) already holds, when the session itself is not authenticated
// to it. It is the tool-call and tools-list counterpart of the reuse
// core_auth_login performs: a session that never called core_auth_login for
// the server -- another MCP client, a fresh CLI session, a front-end whose
// bearer rotated -- is connected on first use instead of being told "user
// not authenticated to server", a consent it already gave.
//
// It reports whether the session is authenticated to the server afterwards.
// A session without a grant is left as it was, with a nil error, so callers
// keep their existing failure for it. A grant the backend rejects with 401 is
// cleared for the person, as core_auth_login does, so the next login issues a
// fresh sign-in link instead of retrying a dead token; any other connect
// failure is returned.
func (a *AggregatorServer) adoptSubjectGrant(ctx context.Context, info *ServerInfo, sessionID, sub string) (bool, error) {
	if info == nil || sessionID == "" || sub == "" || a.authStore == nil || !holdsSubjectGrants(info) {
		return false, nil
	}
	serverName := info.Name
	if authenticated, _ := a.authStore.IsAuthenticated(ctx, sessionID, serverName); authenticated {
		return true, nil
	}
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return false, nil
	}
	issuer, scope := sessionOAuthIssuer(info)
	if issuer == "" {
		return false, nil
	}
	token := api.FullTokenByIssuerForUser(oauthHandler, sessionID, sub, issuer)
	if token == nil || token.AccessToken == "" {
		return false, nil
	}

	// Concurrent first calls from one session -- a front-end fans several
	// tool calls out at once -- share one connect.
	result, err, _ := a.subjectGrantGroup.Do(sessionID+"/"+serverName, func() (any, error) {
		if authenticated, _ := a.authStore.IsAuthenticated(ctx, sessionID, serverName); authenticated {
			return true, nil
		}
		connectCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), subjectGrantConnectTimeout)
		defer cancel()
		connectCtx = api.WithSessionID(api.WithSubject(connectCtx, sub), sessionID)

		connection, err := establishConnection(connectCtx, a, serverName, info.URL, issuer, scope, token.AccessToken)
		if err != nil {
			if is401Error(err) {
				logging.InfoWithAttrs("Aggregator", "The person's grant was rejected by the server, clearing it",
					slog.String("server", serverName),
					slog.String("issuer", issuer),
					slog.String("sub", logging.TruncateIdentifier(sub)))
				api.ClearTokenByIssuerForUser(oauthHandler, sessionID, sub, issuer)
				return false, nil
			}
			return false, err
		}
		if connection.Client != nil {
			if a.connPool != nil {
				a.connPool.Put(sessionID, serverName, connection.Client)
			} else {
				_ = connection.Client.Close()
			}
		}
		// Other live sessions of the person may have listed and cached their
		// tools without this server; the browser-callback connect pushes the
		// same notification.
		a.notifySubjectCapabilitiesChanged(sub, connection)
		logging.InfoWithAttrs("Aggregator", "Session connected with the person's existing grant",
			slog.String("server", serverName),
			slog.String("issuer", issuer),
			slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
			slog.String("sub", logging.TruncateIdentifier(sub)),
			slog.Int("tools", connection.ToolCount))
		return true, nil
	})
	if err != nil {
		return false, err
	}
	adopted, _ := result.(bool)
	return adopted, nil
}

// adoptSubjectGrants connects the session to every server it is not yet
// authenticated to but the person already authorized, and reports how many
// connections that made. Cheap when there is nothing to do: only servers
// holdsSubjectGrants accepts are looked at, and each costs one auth-store
// read once the session is authenticated to it.
func (a *AggregatorServer) adoptSubjectGrants(ctx context.Context, sessionID, sub string) int {
	if sessionID == "" || sub == "" || a.authStore == nil {
		return 0
	}
	adopted := 0
	for _, info := range a.registry.GetAllServers() {
		if !holdsSubjectGrants(info) {
			continue
		}
		if authenticated, _ := a.authStore.IsAuthenticated(ctx, sessionID, info.Name); authenticated {
			continue
		}
		ok, err := a.adoptSubjectGrant(ctx, info, sessionID, sub)
		if err != nil {
			logging.WarnWithAttrs("Aggregator", "Could not connect with the person's existing grant",
				slog.String("server", info.Name),
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
				slog.String("error", err.Error()))
			continue
		}
		if ok {
			adopted++
		}
	}
	return adopted
}
