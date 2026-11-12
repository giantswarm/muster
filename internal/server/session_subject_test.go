package server

import (
	"context"
	"errors"
	"testing"

	oauth "github.com/giantswarm/mcp-oauth"
	"github.com/giantswarm/mcp-oauth/providers"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

func stubValidateBearer(fn func(context.Context, string) (*providers.UserInfo, error)) func() {
	prev := validateBearer
	validateBearer = func(_ *oauth.Server, ctx context.Context, bearer string) (*providers.UserInfo, error) {
		return fn(ctx, bearer)
	}
	return func() { validateBearer = prev }
}

// The session subject is the one the MCP endpoint serves the token as: the
// token's own sub for a forwarded ID token and a delegated token, the
// issuer's subjectClaim mapping for any other trusted-issuer token.
func TestOAuthHTTPServer_SessionSubject(t *testing.T) {
	const dexIssuer = "https://dex.example.com"
	validated := map[string]*providers.UserInfo{
		"portal-token":    {ID: "CgVhbGljZRIGZ2l0aHVi", TokenSource: providers.TokenSourceSSO},
		"kagent-token":    {ID: "CgVhbGljZRIGZ2l0aHVi", Issuer: dexIssuer, TokenSource: providers.TokenSourceTrustedIssuer},
		"delegated-token": {ID: "CgVhbGljZRIGZ2l0aHVi", Issuer: dexIssuer, ActorSubject: "system:serviceaccount:agents:a", TokenSource: providers.TokenSourceTrustedIssuer},
		"muster-token":    {ID: "alice", TokenSource: providers.TokenSourceOAuth},
	}
	defer stubValidateBearer(func(_ context.Context, bearer string) (*providers.UserInfo, error) {
		if info, ok := validated[bearer]; ok {
			return info, nil
		}
		return nil, errors.New("untrusted issuer")
	})()
	defer stubAcceptTrustedIssuerToken(func(_ context.Context, bearer string) (*oauthserver.ForwardedIDTokenAcceptance, error) {
		return &oauthserver.ForwardedIDTokenAcceptance{Subject: "alice@example.com"}, nil
	})()
	s := &OAuthHTTPServer{config: config.OAuthServerConfig{TrustedIssuers: []config.TrustedIssuerConfig{{
		Issuer:     dexIssuer,
		Delegation: &config.TrustedIssuerDelegationConfig{ActorIssuers: []string{"https://kubernetes.default.svc"}},
	}}}}

	for bearer, want := range map[string]string{
		"portal-token":    "CgVhbGljZRIGZ2l0aHVi",
		"kagent-token":    "alice@example.com",
		"delegated-token": "CgVhbGljZRIGZ2l0aHVi",
	} {
		sub, err := s.SessionSubject(t.Context(), bearer)
		require.NoError(t, err, bearer)
		require.Equal(t, want, sub, bearer)
	}

	_, err := s.SessionSubject(t.Context(), "muster-token")
	require.ErrorContains(t, err, "not an externally issued token")

	_, err = s.SessionSubject(t.Context(), "stranger-token")
	require.ErrorContains(t, err, "untrusted issuer")
}

// Until OIDC discovery completed no subject is resolved.
func TestLazyOAuthHTTPServer_SessionSubjectRefusesUntilReady(t *testing.T) {
	_, err := (&LazyOAuthHTTPServer{}).SessionSubject(t.Context(), "token")
	require.Error(t, err)
}

func TestDefaultSubjectIssuer(t *testing.T) {
	require.Equal(t, "https://dex.example.com", defaultSubjectIssuer(config.OAuthServerConfig{
		Provider: OAuthProviderDex, BaseURL: "https://muster.example.com", Dex: config.DexConfig{IssuerURL: "https://dex.example.com"},
	}))
	require.Empty(t, defaultSubjectIssuer(config.OAuthServerConfig{Provider: OAuthProviderGoogle, BaseURL: "https://muster.example.com"}))
}
