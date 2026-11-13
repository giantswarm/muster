package server

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	"github.com/giantswarm/mcp-oauth/providers"
	"github.com/giantswarm/mcp-oauth/providers/dex"
	"github.com/giantswarm/mcp-oauth/providers/oidc"
	oauthserver "github.com/giantswarm/mcp-oauth/server"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
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
// session.
func (s *OAuthHTTPServer) SignInURL(ctx context.Context, returnURL *url.URL, state, codeChallenge string, expected api.SignInIdentity) (string, error) {
	var opts []oauthserver.IdentityFlowOption
	if connector := s.identityCheckConnector(expected); connector != "" {
		opts = append(opts, oauthserver.WithIdentityConnector(connector))
	}
	return s.oauthServer.StartIdentityFlow(ctx, returnURL, codeChallenge, state, opts...)
}

// identityCheckConnector returns the Dex connector the sign-in check signs in
// through for a link issued to expected: the connector expected's subject was
// issued through, when dex.identityCheckConnectors lists it. Empty keeps the
// configured connector. Dex encodes the connector in the subject, so a
// session of another connector can pass the check only through its own.
func (s *OAuthHTTPServer) identityCheckConnector(expected api.SignInIdentity) string {
	connector, listed := s.sessionConnector(expected)
	if connector != "" && !listed {
		logging.Warn("OAuth", "The sign-in check uses connector %q: the session's connector %q is not in dex.identityCheckConnectors, so its subject cannot match",
			s.config.Dex.ConnectorID, connector)
		return ""
	}
	return connector
}

// sessionConnector returns the Dex connector expected's subject was issued
// through when it is not the configured connector, and whether
// dex.identityCheckConnectors lists it. A subject of another issuer or
// provider, or one Dex encoded no connector in, gives "".
func (s *OAuthHTTPServer) sessionConnector(expected api.SignInIdentity) (string, bool) {
	if s.config.Provider != OAuthProviderDex || expected.Issuer != s.musterIssuer() {
		return "", false
	}
	connector, ok := dex.ConnectorFromSubject(expected.Subject)
	if !ok || connector == s.config.Dex.ConnectorID {
		return "", false
	}
	return connector, slices.Contains(s.config.Dex.IdentityCheckConnectors, connector)
}

// CheckRefusal implements api.BrowserIdentity. The check signs the browser in
// at muster's issuer and compares issuer and sub, so it cannot pass for a
// session without a subject, of another issuer, or of a Dex connector it may
// not sign in through. Without dex.connectorId Dex lets the person choose,
// so every connector can pass. Whether a listed connector can sign a browser
// in at all is not known from muster's configuration.
func (s *OAuthHTTPServer) CheckRefusal(expected api.SignInIdentity) error {
	switch {
	case expected.Subject == "":
		logging.Warn("OAuth", "Refusing a sign-in: the session carries no sign-in identity to check the browser against")
		return &api.SignInCheckError{Reason: "muster cannot tell which account your session belongs to, so it cannot confirm that you are the person signing in"}
	case expected.Issuer != s.musterIssuer():
		logging.Warn("OAuth", "Refusing a sign-in: the session's identity was issued by %q, the sign-in check signs in at %q",
			expected.Issuer, s.musterIssuer())
		return &api.SignInCheckError{Reason: "your session comes from another identity provider than the one muster confirms who is signing in with"}
	}
	if connector, listed := s.sessionConnector(expected); connector != "" && !listed && s.config.Dex.ConnectorID != "" {
		logging.Warn("OAuth", "Refusing a sign-in: the session's Dex connector %q is not dex.connectorId (%q) nor in dex.identityCheckConnectors",
			connector, s.config.Dex.ConnectorID)
		return &api.SignInCheckError{Reason: "muster cannot confirm that you are the person signing in with the login method you used; ask your platform team to allow that login method for muster's sign-in check"}
	}
	return nil
}

// validateIdentityCheckConnectors refuses dex.identityCheckConnectors without
// the dex provider, or with a connector ID Dex would refuse.
func validateIdentityCheckConnectors(cfg config.OAuthServerConfig) error {
	if len(cfg.Dex.IdentityCheckConnectors) == 0 {
		return nil
	}
	if cfg.Provider != OAuthProviderDex {
		return fmt.Errorf("dex.identityCheckConnectors needs the dex provider (provider is %q)", cfg.Provider)
	}
	for _, connector := range cfg.Dex.IdentityCheckConnectors {
		if err := oidc.ValidateConnectorID(connector); err != nil {
			return fmt.Errorf("dex.identityCheckConnectors: connector %q: %w", connector, err)
		}
	}
	return nil
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

// CheckRefusal implements api.BrowserIdentity once OIDC discovery completed.
// Before then muster's issuer is unknown and nothing is refused here; the
// link's check fails in SignInURL instead.
func (l *LazyOAuthHTTPServer) CheckRefusal(expected api.SignInIdentity) error {
	inner, err := l.readyInner()
	if err != nil {
		return nil
	}
	return inner.CheckRefusal(expected)
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
