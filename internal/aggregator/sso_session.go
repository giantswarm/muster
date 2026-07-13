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

// canBootstrapSSO reports whether the session has a usable token for
// establishing session-scoped backend connections. The upstream ID token
// serves the human login path; a forwardable (decodable JWT) inbound bearer
// serves callers that arrive with a muster-issued access token (agent OBO
// sessions) and is what the aggregator forwards downstream. An opaque bearer
// does not count: it cannot be forwarded, so a session holding only one has
// lost its upstream credential and the caller treats it as a broken refresh
// chain.
func (s ssoSession) canBootstrapSSO() bool {
	return s.tokens.IDToken != "" || isForwardableToken(s.tokens.Bearer)
}

// LogValue implements slog.LogValuer so ssoSession can be passed directly to
// structured log calls.
func (s ssoSession) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("userID", logging.TruncateIdentifier(s.userID)),
		slog.String("sessionID", logging.TruncateIdentifier(s.sessionID)),
		slog.Int("idTokenLen", len(s.tokens.IDToken)),
		slog.Int("bearerLen", len(s.tokens.Bearer)),
		slog.String("tokenSource", string(s.tokenSource)),
	)
}
