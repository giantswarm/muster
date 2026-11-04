package server

import (
	"context"
	"fmt"
	"net/url"

	"github.com/giantswarm/mcp-oauth/providers"
	"github.com/giantswarm/mcp-oauth/providers/dex"
	"github.com/giantswarm/mcp-oauth/providers/oidc"
	oauthserver "github.com/giantswarm/mcp-oauth/server"

	"github.com/giantswarm/muster/v5/internal/api"
)

// signInIdentity is who a validated bearer was issued to. mcp-oauth names the
// issuer of a trusted-issuer token; every other bearer was issued for a
// sign-in at muster's own identity provider. ID is the raw sub claim on both
// paths, never the trusted issuer's subjectClaim mapping.
func (s *OAuthHTTPServer) signInIdentity(userInfo *providers.UserInfo) api.SignInIdentity {
	issuer := userInfo.Issuer
	if issuer == "" {
		issuer = s.musterIssuer()
	}
	return api.SignInIdentity{Issuer: issuer, Subject: userInfo.ID}
}

// SignInURL implements api.BrowserIdentity with mcp-oauth's identity flow:
// a sign-in at muster's identity provider that grants no token and starts no
// session. With Dex it signs in through the connector of the expected
// subject, which differs from the configured connector when a trusted
// issuer's session came from another login method of the same Dex.
func (s *OAuthHTTPServer) SignInURL(ctx context.Context, returnURL *url.URL, state, codeChallenge string, expected api.SignInIdentity) (string, error) {
	var opts []oauthserver.IdentityFlowOption
	if connector := s.LoginMethod(expected); connector != "" {
		opts = append(opts, oauthserver.WithIdentityConnector(connector))
	}
	return s.oauthServer.StartIdentityFlow(ctx, returnURL, codeChallenge, state, opts...)
}

// LoginMethod implements api.BrowserIdentity: the Dex connector a subject of
// muster's Dex was issued through.
func (s *OAuthHTTPServer) LoginMethod(identity api.SignInIdentity) string {
	if s.config.Provider != OAuthProviderDex || identity.Issuer != s.musterIssuer() {
		return ""
	}
	connector, ok := dex.ConnectorFromSubject(identity.Subject)
	if !ok || oidc.ValidateConnectorID(connector) != nil {
		return ""
	}
	return connector
}

// Identity implements api.BrowserIdentity: the person who signed in at
// muster's identity provider.
func (s *OAuthHTTPServer) Identity(ctx context.Context, returnURL *url.URL, code, codeVerifier string) (api.SignInIdentity, error) {
	subject, err := s.oauthServer.RedeemIdentityCode(ctx, code, returnURL, codeVerifier)
	if err != nil {
		return api.SignInIdentity{}, err
	}
	return api.SignInIdentity{Issuer: s.musterIssuer(), Subject: subject}, nil
}

// SignInURL implements api.BrowserIdentity once OIDC discovery completed;
// until then every sign-in check fails, so no flow completes unchecked.
func (l *LazyOAuthHTTPServer) SignInURL(ctx context.Context, returnURL *url.URL, state, codeChallenge string, expected api.SignInIdentity) (string, error) {
	inner, err := l.readyInner()
	if err != nil {
		return "", err
	}
	return inner.SignInURL(ctx, returnURL, state, codeChallenge, expected)
}

// LoginMethod implements api.BrowserIdentity once OIDC discovery completed.
func (l *LazyOAuthHTTPServer) LoginMethod(identity api.SignInIdentity) string {
	inner, err := l.readyInner()
	if err != nil {
		return ""
	}
	return inner.LoginMethod(identity)
}

// Identity implements api.BrowserIdentity once OIDC discovery completed.
func (l *LazyOAuthHTTPServer) Identity(ctx context.Context, returnURL *url.URL, code, codeVerifier string) (api.SignInIdentity, error) {
	inner, err := l.readyInner()
	if err != nil {
		return api.SignInIdentity{}, err
	}
	return inner.Identity(ctx, returnURL, code, codeVerifier)
}

func (l *LazyOAuthHTTPServer) readyInner() (*OAuthHTTPServer, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.inner == nil {
		return nil, fmt.Errorf("OIDC discovery not yet complete, cannot check who is signing in")
	}
	return l.inner, nil
}
