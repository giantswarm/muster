package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/giantswarm/muster/internal/admin"
	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/server"
	"github.com/giantswarm/muster/pkg/logging"
)

// adminDeps builds the callbacks that admin.Server needs from the
// aggregator's internal stores. It is a tiny glue layer; all business logic
// lives in the underlying stores and mirrors the teardown sequence used by
// handleAuthServerDeletion / handleUserTokensDeletion.
func (a *AggregatorServer) adminDeps() admin.Deps {
	return admin.Deps{
		ListSessions:     a.adminListSessions,
		GetSessionDetail: a.adminGetSessionDetail,
		DeleteSession:    a.adminDeleteSession,
		ReconnectServer:  a.adminReconnectServer,
		ListMCPServers:   a.adminListMCPServers,
		GetMCPDetail:     a.adminGetMCPDetail,
	}
}

// adminListSessions enumerates sessions from two complementary sources so
// that the admin UI stays useful both right after a restart and for sessions
// that haven't exercised a downstream OAuth server:
//
//   - capabilityStore.ListSessions: durable, survives restarts (Valkey), but
//     only records sessions that have fetched capabilities for at least one
//     server.
//   - subjectSessionTracker: in-memory, wiped on restart, but populated on
//     every tools/list and tools/call — catches sessions that only use
//     meta-tools.
//
// The two sources share the same ID space (OAuth token family ID), so the
// union is a straightforward set merge.
func (a *AggregatorServer) adminListSessions(ctx context.Context) ([]admin.SessionSummary, error) {
	seen := map[string]struct{}{}

	if a.subjectSessions != nil {
		for _, sid := range a.subjectSessions.OAuthSessionIDs() {
			seen[sid] = struct{}{}
		}
	}

	if a.capabilityStore != nil {
		sids, err := a.capabilityStore.ListSessions(ctx)
		if err != nil {
			return nil, fmt.Errorf("list sessions: %w", err)
		}
		for _, sid := range sids {
			seen[sid] = struct{}{}
		}
	}

	out := make([]admin.SessionSummary, 0, len(seen))
	for sid := range seen {
		summary := admin.SessionSummary{SessionID: sid}
		if a.subjectSessions != nil {
			summary.Subject = a.subjectSessions.OAuthSubject(sid)
		}
		if summary.Subject == "" {
			summary.Subject = unknownSubject
		}

		// Extract email from ID token if available
		oauthHandler := api.GetOAuthHandler()
		if oauthHandler != nil && oauthHandler.IsEnabled() {
			if tok := oauthHandler.FindTokenWithIDToken(sid); tok != nil && tok.IDToken != "" {
				summary.Email = admin.ExtractEmailFromIDToken(tok.IDToken)
			}
		}

		if a.capabilityStore != nil {
			if all, err := a.capabilityStore.GetAll(ctx, sid); err == nil {
				summary.ServerCount = len(all)
				for _, caps := range all {
					if caps != nil {
						summary.ToolCount += len(caps.Tools)
					}
				}
			}
		}
		out = append(out, summary)
	}
	return out, nil
}

// unknownSubject is the placeholder shown in the admin UI when we can't
// recover the subject for a session (e.g. after a pod restart, before the
// session has made its first tools/list call).
const unknownSubject = "unknown"

// adminGetSessionDetail returns the full detail view for a session. It
// combines data from the capability store (which servers and what tools),
// the connection pool (live transport/expiry metadata), the server registry
// (issuer info), and the OAuth handler (raw JWTs, never the signature).
func (a *AggregatorServer) adminGetSessionDetail(ctx context.Context, sessionID string) (*admin.SessionDetail, bool, error) {
	var caps map[string]*Capabilities
	if a.capabilityStore != nil {
		got, err := a.capabilityStore.GetAll(ctx, sessionID)
		if err != nil {
			return nil, false, fmt.Errorf("capability store: %w", err)
		}
		caps = got
	}

	// Recover the subject from the OAuth-keyed tracker; same ID space as
	// the capability store keys.
	subject := ""
	if a.subjectSessions != nil {
		subject = a.subjectSessions.OAuthSubject(sessionID)
	}

	// Session is known only if at least one source recognizes it.
	if caps == nil && subject == "" {
		return nil, false, nil
	}
	if subject == "" {
		subject = unknownSubject
	}

	poolSnap := map[string]PooledInfo{}
	if a.connPool != nil {
		for _, info := range a.connPool.Snapshot(sessionID) {
			poolSnap[info.ServerName] = info
		}
	}

	oauthHandler := api.GetOAuthHandler()
	oauthEnabled := oauthHandler != nil && oauthHandler.IsEnabled()

	// Cached once so forward-token servers don't re-fetch per iteration.
	var sessionIDToken string
	if oauthEnabled {
		if tok := oauthHandler.FindTokenWithIDToken(sessionID); tok != nil {
			sessionIDToken = tok.IDToken
		}
	}

	// Dedupe JWT rows when two servers share an issuer.
	seenIssuers := map[string]bool{}

	// Extract email from ID token if available
	var email string
	if oauthEnabled {
		if tok := oauthHandler.FindTokenWithIDToken(sessionID); tok != nil && tok.IDToken != "" {
			email = admin.ExtractEmailFromIDToken(tok.IDToken)
		}
	}

	detail := &admin.SessionDetail{SessionID: sessionID, Subject: subject, Email: email}
	for serverName, c := range caps {
		entry := admin.ServerEntry{Name: serverName}
		if c != nil {
			entry.ToolCount = len(c.Tools)
			entry.RsrcCount = len(c.Resources)
			entry.PromptCount = len(c.Prompts)

			names := make([]string, len(c.Tools))
			for i, t := range c.Tools {
				names[i] = t.Name
			}
			sort.Strings(names)
			entry.ToolNames = names
		}

		info, hasInfo := a.registry.GetServerInfo(serverName)
		var issuer string
		if hasInfo && info.AuthInfo != nil {
			issuer = info.AuthInfo.Issuer
			entry.Issuer = issuer
		}

		if p, ok := poolSnap[serverName]; ok {
			entry.Pooled = true
			entry.CreatedAt = p.CreatedAt
			entry.LastUsedAt = p.LastUsedAt
			entry.TokenExpiry = p.TokenExpiry
		}

		detail.Servers = append(detail.Servers, entry)

		if !oauthEnabled || !hasInfo {
			continue
		}

		switch {
		case ShouldUseTokenExchange(info):
			// RFC 8693 exchange results aren't persisted in the oauth token
			// store — the pool is the only place they live.
			if p, ok := poolSnap[serverName]; ok && p.ExchangedToken != "" {
				detail.Tokens = append(detail.Tokens, admin.SessionToken{
					Label: fmt.Sprintf("muster → %s (exchanged token)", serverName),
					Raw:   p.ExchangedToken,
				})
			}
		case ShouldUseTokenForwarding(info):
			if sessionIDToken == "" {
				continue
			}
			detail.Tokens = append(detail.Tokens, admin.SessionToken{
				Label: fmt.Sprintf("muster → %s (forwarded id_token)", serverName),
				Raw:   sessionIDToken,
			})
		default:
			if issuer != "" && !seenIssuers[issuer] {
				if tok := oauthHandler.GetFullTokenByIssuer(sessionID, issuer); tok != nil && tok.IDToken != "" {
					detail.Tokens = append(detail.Tokens, admin.SessionToken{
						Label: fmt.Sprintf("muster → %s (id_token)", serverName),
						Raw:   tok.IDToken,
					})
					seenIssuers[issuer] = true
				}
			}
		}
	}

	// When no per-server token surfaced (e.g. session hasn't touched a
	// downstream server yet), show the user's own login token so the detail
	// page always has at least one JWT to decode.
	if oauthEnabled && len(detail.Tokens) == 0 && sessionIDToken != "" {
		detail.Tokens = append(detail.Tokens, admin.SessionToken{
			Label: "session (id_token)",
			Raw:   sessionIDToken,
		})
	}

	return detail, true, nil
}

// adminListMCPServers returns a summary row for every MCP server registered
// with the aggregator. This is the global view: it reflects the server
// registry, not per-session capability caches.
func (a *AggregatorServer) adminListMCPServers(_ context.Context) ([]admin.MCPSummary, error) {
	all := a.registry.GetAllServers()
	out := make([]admin.MCPSummary, 0, len(all))
	for _, info := range all {
		out = append(out, mcpSummaryFromServerInfo(info))
	}
	return out, nil
}

// adminGetMCPDetail returns the full detail view for a single MCP server,
// or nil + false when the server is unknown.
func (a *AggregatorServer) adminGetMCPDetail(_ context.Context, name string) (*admin.MCPDetail, bool, error) {
	info, ok := a.registry.GetServerInfo(name)
	if !ok {
		return nil, false, nil
	}

	detail := &admin.MCPDetail{
		MCPSummary: mcpSummaryFromServerInfo(info),
		ToolPrefix: info.ToolPrefix,
	}
	if info.AuthInfo != nil {
		detail.Scope = info.AuthInfo.Scope
	}
	return detail, true, nil
}

// mcpSummaryFromServerInfo projects a ServerInfo into the admin summary view.
func mcpSummaryFromServerInfo(info *ServerInfo) admin.MCPSummary {
	summary := admin.MCPSummary{
		Name:         info.Name,
		URL:          info.URL,
		Namespace:    info.GetNamespace(),
		Status:       string(info.GetStatus()),
		RequiresAuth: info.RequiresSessionAuth(),
		LastUpdate:   info.LastUpdate,
	}
	if info.AuthInfo != nil {
		summary.Issuer = info.AuthInfo.Issuer
	}
	return summary
}

// adminDeleteSession performs the full teardown for a single session: oauth
// tokens, auth store, capability cache, pooled connections, and the subject
// tracker entry.
func (a *AggregatorServer) adminDeleteSession(ctx context.Context, sessionID string) error {
	if oauthHandler := api.GetOAuthHandler(); oauthHandler != nil && oauthHandler.IsEnabled() {
		oauthHandler.DeleteTokensBySession(sessionID)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	a.tearDownSession(timeoutCtx, sessionID)

	logging.InfoWithAttrs("Admin", "Session deleted via admin UI",
		slog.String("sessionID", logging.TruncateIdentifier(sessionID)))
	return nil
}

// adminReconnectServer tears down all per-server state for one session and
// immediately re-runs the SSO connection flow. On success the session ends
// up with a freshly-exchanged (or freshly-forwarded) bearer, a new pooled
// client, and a repopulated capability cache — indistinguishable from a
// server that was just auth'd for the first time.
func (a *AggregatorServer) adminReconnectServer(ctx context.Context, sessionID, serverName string) error {
	info, ok := a.registry.GetServerInfo(serverName)
	if !ok {
		// Already gone — treat as a no-op so the admin UI can recover from
		// stale state without error.
		return nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	a.tearDownSessionServer(timeoutCtx, sessionID, info)

	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return nil
	}
	subject := ""
	if a.subjectSessions != nil {
		subject = a.subjectSessions.OAuthSubject(sessionID)
	}
	if subject == "" {
		logging.InfoWithAttrs("Admin", "Reconnect skipped SSO re-init — no subject for session",
			slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
			slog.String("server", serverName))
		return nil
	}

	tok := oauthHandler.FindTokenWithIDToken(sessionID)
	if tok == nil || tok.IDToken == "" {
		logging.InfoWithAttrs("Admin", "Reconnect skipped SSO re-init — no ID token stored",
			slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
			slog.String("server", serverName))
		return nil
	}

	ssoCtx := api.WithSubject(timeoutCtx, subject)
	ssoCtx = api.WithSessionID(ssoCtx, sessionID)
	ssoCtx = server.ContextWithIDToken(ssoCtx, tok.IDToken)

	a.establishSSOConnection(ssoCtx, info, a.getMusterIssuer())

	logging.InfoWithAttrs("Admin", "Server reconnected via admin UI",
		slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
		slog.String("server", serverName))
	return nil
}
