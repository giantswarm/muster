package server

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/giantswarm/muster/v5/internal/config"
)

// browserIdentityReturnURL is where an identity sign-in sends the browser
// back: the OAuth proxy start endpoint, on muster's issuer origin.
func (s *OAuthHTTPServer) browserIdentityReturnURL() (*url.URL, error) {
	return url.Parse(strings.TrimSuffix(s.config.BaseURL, "/") + config.DefaultOAuthProxyStartPath)
}

// SignInURL implements api.BrowserIdentity with mcp-oauth's identity flow:
// a sign-in at muster's identity provider that grants no token and starts no
// session.
func (s *OAuthHTTPServer) SignInURL(ctx context.Context, state, codeChallenge string) (string, error) {
	returnURL, err := s.browserIdentityReturnURL()
	if err != nil {
		return "", fmt.Errorf("browser identity return URL: %w", err)
	}
	return s.oauthServer.StartIdentityFlow(ctx, returnURL, codeChallenge, state)
}

// Subject implements api.BrowserIdentity: the subject of the person who
// signed in, the provider's user ID that muster's sessions are keyed by.
func (s *OAuthHTTPServer) Subject(ctx context.Context, code, codeVerifier string) (string, error) {
	returnURL, err := s.browserIdentityReturnURL()
	if err != nil {
		return "", fmt.Errorf("browser identity return URL: %w", err)
	}
	return s.oauthServer.RedeemIdentityCode(ctx, code, returnURL, codeVerifier)
}

// SignInURL implements api.BrowserIdentity once OIDC discovery completed;
// until then every sign-in check fails, so no flow completes unchecked.
func (l *LazyOAuthHTTPServer) SignInURL(ctx context.Context, state, codeChallenge string) (string, error) {
	inner, err := l.readyInner()
	if err != nil {
		return "", err
	}
	return inner.SignInURL(ctx, state, codeChallenge)
}

// Subject implements api.BrowserIdentity once OIDC discovery completed.
func (l *LazyOAuthHTTPServer) Subject(ctx context.Context, code, codeVerifier string) (string, error) {
	inner, err := l.readyInner()
	if err != nil {
		return "", err
	}
	return inner.Subject(ctx, code, codeVerifier)
}

func (l *LazyOAuthHTTPServer) readyInner() (*OAuthHTTPServer, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.inner == nil {
		return nil, fmt.Errorf("OIDC discovery not yet complete, cannot check who is signing in")
	}
	return l.inner, nil
}
