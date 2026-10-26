package server

import (
	"context"
	"fmt"
	"net/url"
)

// SignInURL implements api.BrowserIdentity with mcp-oauth's identity flow:
// a sign-in at muster's identity provider that grants no token and starts no
// session.
func (s *OAuthHTTPServer) SignInURL(ctx context.Context, returnURL *url.URL, state, codeChallenge string) (string, error) {
	return s.oauthServer.StartIdentityFlow(ctx, returnURL, codeChallenge, state)
}

// Subject implements api.BrowserIdentity: the subject of the person who
// signed in, the provider's user ID that muster's sessions are keyed by.
func (s *OAuthHTTPServer) Subject(ctx context.Context, returnURL *url.URL, code, codeVerifier string) (string, error) {
	return s.oauthServer.RedeemIdentityCode(ctx, code, returnURL, codeVerifier)
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

// Subject implements api.BrowserIdentity once OIDC discovery completed.
func (l *LazyOAuthHTTPServer) Subject(ctx context.Context, returnURL *url.URL, code, codeVerifier string) (string, error) {
	inner, err := l.readyInner()
	if err != nil {
		return "", err
	}
	return inner.Subject(ctx, returnURL, code, codeVerifier)
}

func (l *LazyOAuthHTTPServer) readyInner() (*OAuthHTTPServer, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.inner == nil {
		return nil, fmt.Errorf("OIDC discovery not yet complete, cannot check who is signing in")
	}
	return l.inner, nil
}
