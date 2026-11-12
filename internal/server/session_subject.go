package server

import (
	"context"
	"errors"
	"fmt"

	oauth "github.com/giantswarm/mcp-oauth"
	"github.com/giantswarm/mcp-oauth/providers"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
)

// validateBearer is a test seam around (*oauth.Server).ValidateToken.
var validateBearer = func(s *oauth.Server, ctx context.Context, bearerToken string) (*providers.UserInfo, error) {
	return s.ValidateToken(ctx, bearerToken)
}

// SessionSubject implements api.SessionSubjectResolver with the decision the
// MCP endpoint makes for an externally issued bearer: mcp-oauth's
// ValidateToken picks the token source, and the endpoint serves a delegated
// token and a forwarded ID token as the token's own sub and any other
// trusted-issuer token as the issuer's subjectClaim mapping.
func (s *OAuthHTTPServer) SessionSubject(ctx context.Context, bearer string) (string, error) {
	userInfo, err := validateBearer(s.oauthServer, ctx, bearer)
	if err != nil {
		return "", err
	}
	if userInfo == nil || userInfo.ID == "" {
		return "", errors.New("the token yields no subject")
	}
	switch {
	case s.isDelegated(userInfo), userInfo.TokenSource == providers.TokenSourceSSO:
		return userInfo.ID, nil
	case userInfo.TokenSource == providers.TokenSourceTrustedIssuer:
		acceptance, err := acceptTrustedIssuerToken(s.oauthServer, ctx, bearer)
		if err != nil {
			return "", err
		}
		if acceptance.Subject == "" {
			return "", errors.New("the token yields no subject")
		}
		return acceptance.Subject, nil
	default:
		return "", fmt.Errorf("a %q token is not an externally issued token", userInfo.TokenSource)
	}
}

// SessionSubject implements api.SessionSubjectResolver once OIDC discovery
// completed.
func (l *LazyOAuthHTTPServer) SessionSubject(ctx context.Context, bearer string) (string, error) {
	inner, err := l.readyInner()
	if err != nil {
		return "", err
	}
	return inner.SessionSubject(ctx, bearer)
}

var (
	_ api.SessionSubjectResolver = (*OAuthHTTPServer)(nil)
	_ api.SessionSubjectResolver = (*LazyOAuthHTTPServer)(nil)
)

// defaultSubjectIssuer is the issuer whose subject tokens a grant target
// accepts when it lists no subjectIssuers: the Dex issuer of muster's own
// sign-in. Another provider has none, so its grant targets list
// subjectIssuers.
func defaultSubjectIssuer(cfg config.OAuthServerConfig) string {
	if cfg.Provider == OAuthProviderDex {
		return cfg.Dex.IssuerURL
	}
	return ""
}
