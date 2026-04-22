package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/giantswarm/muster/internal/admin"
	"github.com/giantswarm/muster/internal/api"
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
		DisconnectServer: a.adminDisconnectServer,
	}
}

// adminListSessions enumerates sessions from two complementary sources so
// that the admin UI stays useful both right after a restart and for sessions
// that haven't exercised a downstream OAuth server:
//
//   - capabilityStore.ListSessions: durable, survives restarts (Valkey), but
//     only records sessions that have fetched capabilities for at least one
//     server.
//   - subjectSessionTracker.OAuthSnapshot: in-memory, wiped on restart, but
//     populated on every tools/list call — catches sessions that only use
//     meta-tools.
//
// The two sources share the same ID space (OAuth token family ID), so the
// union is a straightforward set merge.
func (a *AggregatorServer) adminListSessions(ctx context.Context) ([]admin.SessionSummary, error) {
	seen := map[string]struct{}{}
	subjectBySession := map[string]string{}

	if a.subjectSessions != nil {
		for sid, sub := range a.subjectSessions.OAuthSnapshot() {
			seen[sid] = struct{}{}
			subjectBySession[sid] = sub
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
		summary := admin.SessionSummary{
			SessionID: sid,
			Subject:   subjectBySession[sid],
		}
		if summary.Subject == "" {
			summary.Subject = "unknown"
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

	// Recover the subject from the OAuth-keyed tracker map; this is the same
	// ID space as the capability store keys.
	subject := ""
	if a.subjectSessions != nil {
		subject = a.subjectSessions.OAuthSnapshot()[sessionID]
	}

	// Session is known only if at least one source recognizes it.
	if caps == nil && subject == "" {
		return nil, false, nil
	}
	if subject == "" {
		subject = "unknown"
	}

	poolSnap := map[string]PooledInfo{}
	if a.connPool != nil {
		for _, info := range a.connPool.Snapshot(sessionID) {
			poolSnap[info.ServerName] = info
		}
	}

	oauthHandler := api.GetOAuthHandler()
	oauthEnabled := oauthHandler != nil && oauthHandler.IsEnabled()

	// Track which issuers we've already surfaced a token for, so that two
	// servers sharing an issuer don't produce duplicate JWT rows.
	seenIssuers := map[string]bool{}

	detail := &admin.SessionDetail{SessionID: sessionID, Subject: subject}
	for serverName, c := range caps {
		entry := admin.ServerEntry{Name: serverName}
		if c != nil {
			entry.ToolCount = len(c.Tools)
			entry.RsrcCount = len(c.Resources)
			entry.PromptCount = len(c.Prompts)
		}

		var issuer string
		if info, ok := a.registry.GetServerInfo(serverName); ok && info.AuthInfo != nil {
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

		// Attach the decoded ID token for this server, if one exists.
		if oauthEnabled && issuer != "" && !seenIssuers[issuer] {
			if tok := oauthHandler.GetFullTokenByIssuer(sessionID, issuer); tok != nil && tok.IDToken != "" {
				detail.Tokens = append(detail.Tokens, admin.SessionToken{
					Label: fmt.Sprintf("muster → %s (id_token)", serverName),
					Raw:   tok.IDToken,
				})
				seenIssuers[issuer] = true
			}
		}
	}

	// Fallback: servers registered pending-auth via the new mcp-go sentinel
	// path land with an empty Issuer, so the per-server lookup above won't
	// yield anything. FindTokenWithIDToken searches the session for any stored
	// token that carries an ID token — typically the user's login token — so
	// the admin UI still has something to decode.
	if oauthEnabled && len(detail.Tokens) == 0 {
		if tok := oauthHandler.FindTokenWithIDToken(sessionID); tok != nil && tok.IDToken != "" {
			detail.Tokens = append(detail.Tokens, admin.SessionToken{
				Label: "session (id_token)",
				Raw:   tok.IDToken,
			})
		}
	}

	return detail, true, nil
}

// adminDeleteSession performs the full teardown for a single session. It
// mirrors handleUserTokensDeletion but scoped to one sessionID instead of
// all sessions for a user.
func (a *AggregatorServer) adminDeleteSession(ctx context.Context, sessionID string) error {
	if oauthHandler := api.GetOAuthHandler(); oauthHandler != nil && oauthHandler.IsEnabled() {
		oauthHandler.DeleteTokensBySession(sessionID)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if a.authStore != nil {
		if err := a.authStore.RevokeSession(timeoutCtx, sessionID); err != nil {
			logging.WarnWithAttrs("Admin", "Failed to revoke auth session",
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
				slog.String("error", err.Error()))
		}
	}
	if a.capabilityStore != nil {
		if err := a.capabilityStore.Delete(timeoutCtx, sessionID); err != nil {
			logging.WarnWithAttrs("Admin", "Failed to delete capability store entry",
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
				slog.String("error", err.Error()))
		}
	}
	if a.connPool != nil {
		a.connPool.EvictSession(sessionID)
	}

	logging.InfoWithAttrs("Admin", "Session deleted via admin UI",
		slog.String("sessionID", logging.TruncateIdentifier(sessionID)))
	return nil
}

// adminDisconnectServer performs a per-server disconnect for one session.
// It mirrors handleAuthServerDeletion without requiring a request context.
func (a *AggregatorServer) adminDisconnectServer(ctx context.Context, sessionID, serverName string) error {
	info, ok := a.registry.GetServerInfo(serverName)
	if !ok {
		// Already gone — treat disconnect as a no-op so the admin UI can
		// recover from stale state without error.
		return nil
	}

	if info.AuthInfo != nil && info.AuthInfo.Issuer != "" {
		if oauthHandler := api.GetOAuthHandler(); oauthHandler != nil && oauthHandler.IsEnabled() {
			oauthHandler.ClearTokenByIssuer(sessionID, info.AuthInfo.Issuer)
		}
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if a.authStore != nil {
		if err := a.authStore.Revoke(timeoutCtx, sessionID, serverName); err != nil {
			logging.WarnWithAttrs("Admin", "Failed to revoke auth",
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
				slog.String("server", serverName),
				slog.String("error", err.Error()))
		}
	}
	if a.capabilityStore != nil {
		if err := a.capabilityStore.DeleteEntry(timeoutCtx, sessionID, serverName); err != nil {
			logging.WarnWithAttrs("Admin", "Failed to delete capability entry",
				slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
				slog.String("server", serverName),
				slog.String("error", err.Error()))
		}
	}
	if a.connPool != nil {
		a.connPool.Evict(sessionID, serverName)
	}

	logging.InfoWithAttrs("Admin", "Server disconnected via admin UI",
		slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
		slog.String("server", serverName))
	return nil
}
