package server

import (
	"context"
	"fmt"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
)

// SessionSubject implements api.SessionSubjectResolver in the order
// mcp-oauth's ValidateToken accepts an externally issued token: as a
// forwarded ID token of a trustedAudiences client first, which serves the
// session as the token's raw sub, then as a trusted-issuer token, which
// serves it as the issuer's subjectClaim mapping. muster sets no
// RequireResourceAccessTokens, so the forwarded path is always tried.
func (s *OAuthHTTPServer) SessionSubject(ctx context.Context, bearer string) (string, error) {
	acceptance, forwardedErr := acceptForwardedIDToken(s.oauthServer, ctx, bearer)
	if forwardedErr != nil {
		var err error
		acceptance, err = acceptTrustedIssuerToken(s.oauthServer, ctx, bearer)
		if err != nil {
			return "", fmt.Errorf("neither a forwarded ID token (%v) nor a trusted-issuer token: %w", forwardedErr, err)
		}
	}
	if acceptance == nil || acceptance.Subject == "" {
		return "", fmt.Errorf("the token yields no subject")
	}
	return acceptance.Subject, nil
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

// musterIssuerOf is the issuer of the bearers muster's own identity provider
// issues: the Dex issuer with the dex provider, muster's base URL otherwise.
func musterIssuerOf(cfg config.OAuthServerConfig) string {
	if cfg.Provider == OAuthProviderDex && cfg.Dex.IssuerURL != "" {
		return cfg.Dex.IssuerURL
	}
	return cfg.BaseURL
}
