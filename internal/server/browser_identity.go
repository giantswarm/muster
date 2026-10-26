package server

import (
	"context"
	"fmt"
	"net/url"

	"github.com/giantswarm/mcp-oauth/providers"

	"github.com/giantswarm/muster/v5/internal/api"
)

// signInIdentity is who a validated bearer was issued to. mcp-oauth names the
// issuer of a trusted-issuer token; every other bearer was issued for a
// sign-in at muster's own identity provider. ID is the raw sub claim on both
// paths, never the trusted issuer's subjectClaim mapping. A JWT access token
// muster issued itself carries no identity: its sub may be that mapping.
func (s *OAuthHTTPServer) signInIdentity(userInfo *providers.UserInfo) api.SignInIdentity {
	if userInfo.TokenSource == providers.TokenSourceJWT {
		return api.SignInIdentity{}
	}
	issuer := userInfo.Issuer
	if issuer == "" {
		issuer = s.musterIssuer()
	}
	return api.SignInIdentity{Issuer: issuer, Subject: userInfo.ID}
}

// SignInURL implements api.BrowserIdentity with mcp-oauth's identity flow:
// a sign-in at muster's identity provider that grants no token and starts no
// session.
func (s *OAuthHTTPServer) SignInURL(ctx context.Context, returnURL *url.URL, state, codeChallenge string) (string, error) {
	return s.oauthServer.StartIdentityFlow(ctx, returnURL, codeChallenge, state)
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
func (l *LazyOAuthHTTPServer) SignInURL(ctx context.Context, returnURL *url.URL, state, codeChallenge string) (string, error) {
	inner, err := l.readyInner()
	if err != nil {
		return "", err
	}
	return inner.SignInURL(ctx, returnURL, state, codeChallenge)
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
