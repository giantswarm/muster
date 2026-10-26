package server

import (
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/url"
	"testing"

	oauth "github.com/giantswarm/mcp-oauth"
	"github.com/giantswarm/mcp-oauth/providers"
	"github.com/giantswarm/mcp-oauth/providers/mock"
	"github.com/giantswarm/mcp-oauth/storage/memory"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
)

// The sign-in check runs mcp-oauth's identity flow: the browser comes back to
// the OAuth proxy start endpoint, and the code it brings yields the subject.
func TestOAuthHTTPServer_BrowserIdentity(t *testing.T) {
	provider := mock.NewProvider()
	var providerState string
	authorizationURL := provider.AuthorizationURLFunc
	provider.AuthorizationURLFunc = func(state, challenge, method string, scopes []string, opts *providers.AuthorizationURLOptions) string {
		providerState = state
		return authorizationURL(state, challenge, method, scopes, opts)
	}
	oauthSrv, err := oauth.NewServerWithCombined(provider, memory.New(), &oauth.ServerConfig{
		Issuer:                      "https://muster.example.com",
		DisableNonceEchoRequirement: true,
	}, slog.Default())
	require.NoError(t, err)
	s := &OAuthHTTPServer{config: config.OAuthServerConfig{BaseURL: "https://muster.example.com"}, oauthServer: oauthSrv}
	returnURL, err := url.Parse("https://muster.example.com" + config.DefaultOAuthProxyStartPath)
	require.NoError(t, err)

	verifier := "identity-verifier-identity-verifier-identity-verifier"
	sum := sha256.Sum256([]byte(verifier))
	state := "identity-state-identity-state"

	signIn, err := s.SignInURL(t.Context(), returnURL, state, base64.RawURLEncoding.EncodeToString(sum[:]))
	require.NoError(t, err)
	require.NotEmpty(t, signIn)

	code, clientState, err := oauthSrv.HandleProviderCallback(t.Context(), providerState, "provider-code")
	require.NoError(t, err)
	require.Equal(t, state, clientState)
	require.Equal(t, returnURL.String(), code.RedirectURI)

	identity, err := s.Identity(t.Context(), returnURL, code.Code, verifier)
	require.NoError(t, err)
	require.Equal(t, "https://muster.example.com", identity.Issuer)
	require.NotEmpty(t, identity.Subject)
}

// Until OIDC discovery completed the check refuses, so no sign-in completes
// unchecked while muster runs degraded.
func TestLazyOAuthHTTPServer_BrowserIdentityRefusesUntilReady(t *testing.T) {
	l := &LazyOAuthHTTPServer{}

	returnURL := &url.URL{Scheme: "https", Host: "muster.example.com", Path: config.DefaultOAuthProxyStartPath}
	_, err := l.SignInURL(t.Context(), returnURL, "state", "challenge")
	require.Error(t, err)
	_, err = l.Identity(t.Context(), returnURL, "code", "verifier")
	require.Error(t, err)
}

// A trusted issuer's token names its issuer and raw sub, whatever its
// subjectClaim maps the session subject to; any other bearer was issued for a
// sign-in at muster's own identity provider. A JWT access token muster issued
// itself names none.
func TestOAuthHTTPServer_SignInIdentity(t *testing.T) {
	s := &OAuthHTTPServer{config: config.OAuthServerConfig{
		Provider: OAuthProviderDex,
		Dex:      config.DexConfig{IssuerURL: "https://dex.example.com"},
	}}

	require.Equal(t, api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIGZ2l0aHVi"},
		s.signInIdentity(&providers.UserInfo{ID: "CgVhbGljZRIGZ2l0aHVi", Email: "alice@example.com"}))
	require.Equal(t, api.SignInIdentity{Issuer: "https://dex.other.example.com", Subject: "CgVhbGljZRIGZ2l0aHVi"},
		s.signInIdentity(&providers.UserInfo{ID: "CgVhbGljZRIGZ2l0aHVi", Email: "alice@example.com",
			Issuer: "https://dex.other.example.com", TokenSource: providers.TokenSourceTrustedIssuer}))
	require.Zero(t, s.signInIdentity(&providers.UserInfo{ID: "alice@example.com", TokenSource: providers.TokenSourceJWT}))
}
