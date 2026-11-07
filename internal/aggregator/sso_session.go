package aggregator

import (
	"context"
	"log/slog"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/server"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// ssoSession captures the token state for a single authenticated request,
// providing a stable snapshot for the session bootstrap decision.
type ssoSession struct {
	userID      string
	sessionID   string
	tokens      server.CallerTokens
	tokenSource providers.TokenSource
	// delegated is the request's delegated caller, nil for any other caller.
	// Its token is the session's subject for token exchange, never forwarded.
	delegated *api.DelegatedCaller
}

// ssoSessionFromContext extracts the SSO-relevant token state from an
// authenticated request context.
func ssoSessionFromContext(ctx context.Context, sessionID string) ssoSession {
	userInfo, _ := oauthhandler.UserInfoFromContext(ctx)
	var tokenSource providers.TokenSource
	if userInfo != nil {
		tokenSource = userInfo.TokenSource
	}
	sso := ssoSession{
		userID:      getUserSubjectFromContext(ctx),
		sessionID:   sessionID,
		tokens:      server.CallerTokensFromContext(ctx),
		tokenSource: tokenSource,
	}
	if caller, ok := api.DelegatedCallerFromContext(ctx); ok {
		sso.delegated = &caller
	}
	return sso
}

// connectContext is the context a background connect of the session runs on:
// base carrying the session's subject, session ID, caller tokens and, for a
// delegated caller, the delegated caller.
func (s ssoSession) connectContext(base context.Context) context.Context {
	ctx := api.WithSubject(base, s.userID)
	ctx = api.WithSessionID(ctx, s.sessionID)
	ctx = server.ContextWithCallerTokens(ctx, s.tokens)
	if s.delegated != nil {
		ctx = api.WithDelegatedCaller(ctx, *s.delegated)
	}
	return ctx
}

// canBootstrapSSO requires a provider credential or an explicitly delegated
// caller eligible for token exchange. A resource bearer grants no backend access.
func (s ssoSession) canBootstrapSSO() bool {
	return s.tokens.IDToken != "" || s.delegated != nil
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
		slog.Bool("delegated", s.delegated != nil),
	)
}

// String implements fmt.Stringer so that %v, %+v and %s of an ssoSession print
// the same redacted view as LogValue. Without it, fmt falls back to the struct
// layout and prints the caller's ID token and bearer verbatim -- one stray %v
// is enough to land two live JWTs in the pod logs and every pipeline that
// collects them. Rendering LogValue keeps the two views from ever disagreeing
// about what is safe to print.
func (s ssoSession) String() string {
	return logging.FormatGroup("ssoSession", s.LogValue())
}

// GoString implements fmt.GoStringer so that %#v redacts as well.
func (s ssoSession) GoString() string {
	return s.String()
}
