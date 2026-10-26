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

	verifier := "identity-verifier-identity-verifier-identity-verifier"
	sum := sha256.Sum256([]byte(verifier))
	state := "identity-state-identity-state"

	signIn, err := s.SignInURL(t.Context(), state, base64.RawURLEncoding.EncodeToString(sum[:]))
	require.NoError(t, err)
	require.NotEmpty(t, signIn)

	code, clientState, err := oauthSrv.HandleProviderCallback(t.Context(), providerState, "provider-code")
	require.NoError(t, err)
	require.Equal(t, state, clientState)
	returnURL, err := url.Parse(code.RedirectURI)
	require.NoError(t, err)
	require.Equal(t, "https://muster.example.com"+config.DefaultOAuthProxyStartPath, returnURL.String())

	subject, err := s.Subject(t.Context(), code.Code, verifier)
	require.NoError(t, err)
	require.NotEmpty(t, subject)
}

// Until OIDC discovery completed the check refuses, so no sign-in completes
// unchecked while muster runs degraded.
func TestLazyOAuthHTTPServer_BrowserIdentityRefusesUntilReady(t *testing.T) {
	l := &LazyOAuthHTTPServer{}

	_, err := l.SignInURL(t.Context(), "state", "challenge")
	require.Error(t, err)
	_, err = l.Subject(t.Context(), "code", "verifier")
	require.Error(t, err)
}
