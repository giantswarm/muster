package server

import (
	"context"
	"errors"
	"testing"

	oauth "github.com/giantswarm/mcp-oauth"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

// The session subject follows the order mcp-oauth accepts an external token
// in: a forwarded ID token keeps its raw sub, any other token is a
// trusted-issuer token served as its mapped subject.
func TestOAuthHTTPServer_SessionSubject(t *testing.T) {
	forwarded := func(_ context.Context, bearer string) (*oauthserver.ForwardedIDTokenAcceptance, error) {
		if bearer == "portal-token" {
			return &oauthserver.ForwardedIDTokenAcceptance{Subject: "CgVhbGljZRIGZ2l0aHVi"}, nil
		}
		return nil, oauth.ErrTrustedAudienceMismatch
	}
	trusted := func(_ context.Context, bearer string) (*oauthserver.ForwardedIDTokenAcceptance, error) {
		if bearer == "kagent-token" || bearer == "portal-token" {
			return &oauthserver.ForwardedIDTokenAcceptance{Subject: "alice@example.com"}, nil
		}
		return nil, errors.New("untrusted issuer")
	}
	defer stubAcceptForwardedIDToken(forwarded)()
	defer stubAcceptTrustedIssuerToken(trusted)()
	s := &OAuthHTTPServer{}

	sub, err := s.SessionSubject(t.Context(), "portal-token")
	require.NoError(t, err)
	require.Equal(t, "CgVhbGljZRIGZ2l0aHVi", sub, "a trustedAudiences token is served as its raw sub even when its issuer maps the email")

	sub, err = s.SessionSubject(t.Context(), "kagent-token")
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", sub)

	_, err = s.SessionSubject(t.Context(), "stranger-token")
	require.ErrorContains(t, err, "untrusted issuer")
}

// Until OIDC discovery completed no subject is resolved.
func TestLazyOAuthHTTPServer_SessionSubjectRefusesUntilReady(t *testing.T) {
	_, err := (&LazyOAuthHTTPServer{}).SessionSubject(t.Context(), "token")
	require.Error(t, err)
}

func TestMusterIssuerOf(t *testing.T) {
	require.Equal(t, "https://dex.example.com", musterIssuerOf(config.OAuthServerConfig{
		Provider: OAuthProviderDex, BaseURL: "https://muster.example.com", Dex: config.DexConfig{IssuerURL: "https://dex.example.com"},
	}))
	require.Equal(t, "https://muster.example.com", musterIssuerOf(config.OAuthServerConfig{Provider: OAuthProviderGoogle, BaseURL: "https://muster.example.com"}))
}
