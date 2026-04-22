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

// adminListSessions enumerates every tracked subject/session pair and
// summarises it using the capability store counts.
func (a *AggregatorServer) adminListSessions(ctx context.Context) ([]admin.SessionSummary, error) {
	if a.subjectSessions == nil {
		return nil, nil
	}

	var out []admin.SessionSummary
	for subject, sids := range a.subjectSessions.Snapshot() {
		for _, sid := range sids {
			summary := admin.SessionSummary{
				SessionID: sid,
				Subject:   subject,
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
	}
	return out, nil
}

// adminGetSessionDetail returns the full detail view for a session. It
// combines data from the capability store (which servers and what tools),
// the connection pool (live transport/expiry metadata), the server registry
// (issuer info), and the OAuth handler (raw JWTs, never the signature).
func (a *AggregatorServer) adminGetSessionDetail(ctx context.Context, sessionID string) (*admin.SessionDetail, bool, error) {
	if a.subjectSessions == nil || a.capabilityStore == nil {
		return nil, false, nil
	}

	// The tracker's reverse map is private; recover the subject by scanning
	// the snapshot. This is fine for admin-scale session counts.
	subject := ""
	for sub, sids := range a.subjectSessions.Snapshot() {
		for _, sid := range sids {
			if sid == sessionID {
				subject = sub
				break
			}
		}
		if subject != "" {
			break
		}
	}
	if subject == "" {
		return nil, false, nil
	}

	caps, err := a.capabilityStore.GetAll(ctx, sessionID)
	if err != nil {
		return nil, false, fmt.Errorf("capability store: %w", err)
	}

	poolSnap := map[string]PooledInfo{}
	if a.connPool != nil {
		for _, info := range a.connPool.Snapshot(sessionID) {
			poolSnap[info.ServerName] = info
		}
	}

	oauthHandler := api.GetOAuthHandler()

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
		if oauthHandler != nil && oauthHandler.IsEnabled() && issuer != "" {
			if tok := oauthHandler.GetFullTokenByIssuer(sessionID, issuer); tok != nil && tok.IDToken != "" {
				detail.Tokens = append(detail.Tokens, admin.SessionToken{
					Label: fmt.Sprintf("muster → %s (id_token)", serverName),
					Raw:   tok.IDToken,
				})
			}
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
