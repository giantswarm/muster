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
	idToken     string
	bearer      string
	actorToken  string
	tokenSource providers.TokenSource
}

// ssoSessionFromContext extracts the SSO-relevant token state from an
// authenticated request context.
func ssoSessionFromContext(ctx context.Context, sessionID string) ssoSession {
	tokens := server.CallerTokensFromContext(ctx)
	userInfo, _ := oauthhandler.UserInfoFromContext(ctx)
	var tokenSource providers.TokenSource
	if userInfo != nil {
		tokenSource = userInfo.TokenSource
	}
	return ssoSession{
		userID:      getUserSubjectFromContext(ctx),
		sessionID:   sessionID,
		idToken:     tokens.IDToken,
		bearer:      tokens.Bearer,
		actorToken:  tokens.Actor,
		tokenSource: tokenSource,
	}
}

// callerTokens reconstructs the credential bundle this session carries, for
// re-injection into a context rebuilt off the request path.
func (s ssoSession) callerTokens() server.CallerTokens {
	return server.CallerTokens{IDToken: s.idToken, Bearer: s.bearer, Actor: s.actorToken}
}

// canBootstrapSSO reports whether the session has a usable subject token for
// establishing localMint backend connections. Returns false when neither an
// upstream ID token nor a delegated OBO bearer is present — the session cannot
// drive a token exchange and bootstrapping would fail immediately.
func (s ssoSession) canBootstrapSSO() bool {
	return s.idToken != "" || s.tokenSource == providers.TokenSourceOBO
}

// LogValue implements slog.LogValuer so ssoSession can be passed directly to
// structured log calls.
func (s ssoSession) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("userID", logging.TruncateIdentifier(s.userID)),
		slog.String("sessionID", logging.TruncateIdentifier(s.sessionID)),
		slog.Int("idTokenLen", len(s.idToken)),
		slog.Int("bearerLen", len(s.bearer)),
		slog.Int("actorTokenLen", len(s.actorToken)),
		slog.String("tokenSource", string(s.tokenSource)),
	)
}
