package aggregator

import (
	"context"
	"log/slog"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"

	"github.com/giantswarm/muster/internal/server"
	"github.com/giantswarm/muster/pkg/logging"
)

// ssoSession captures the token state for a single authenticated request,
// providing a stable snapshot for the session bootstrap decision.
type ssoSession struct {
	userID      string
	sessionID   string
	tokens      server.CallerTokens
	tokenSource providers.TokenSource
}

// ssoSessionFromContext extracts the SSO-relevant token state from an
// authenticated request context.
func ssoSessionFromContext(ctx context.Context, sessionID string) ssoSession {
	userInfo, _ := oauthhandler.UserInfoFromContext(ctx)
	var tokenSource providers.TokenSource
	if userInfo != nil {
		tokenSource = userInfo.TokenSource
	}
	return ssoSession{
		userID:      getUserSubjectFromContext(ctx),
		sessionID:   sessionID,
		tokens:      server.CallerTokensFromContext(ctx),
		tokenSource: tokenSource,
	}
}

// canBootstrapSSO reports whether the session has a usable subject token for
// establishing localMint backend connections. Returns false when neither an
// upstream ID token nor a delegated actor token is present: the session cannot
// drive a token exchange and bootstrapping would fail immediately.
func (s ssoSession) canBootstrapSSO() bool {
	return s.tokens.IDToken != "" || s.tokens.Actor != ""
}

// LogValue implements slog.LogValuer so ssoSession can be passed directly to
// structured log calls.
func (s ssoSession) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("userID", logging.TruncateIdentifier(s.userID)),
		slog.String("sessionID", logging.TruncateIdentifier(s.sessionID)),
		slog.Int("idTokenLen", len(s.tokens.IDToken)),
		slog.Int("bearerLen", len(s.tokens.Bearer)),
		slog.Int("actorTokenLen", len(s.tokens.Actor)),
		slog.String("tokenSource", string(s.tokenSource)),
	)
}
