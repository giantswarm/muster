package server

import (
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	oauth "github.com/giantswarm/mcp-oauth"
	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
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

	signIn, err := s.SignInURL(t.Context(), returnURL, state, base64.RawURLEncoding.EncodeToString(sum[:]), api.SignInIdentity{})
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

// With Dex, the sign-in check goes to the connector the expected subject was
// issued through, so a session from another login method than the configured
// connector can pass it. Any other identity keeps the configured connector.
func TestOAuthHTTPServer_SignInURLUsesTheExpectedLoginMethod(t *testing.T) {
	for name, tc := range map[string]struct {
		expected api.SignInIdentity
		want     string
	}{
		"subject of muster's Dex":    {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIIYXp1cmUtYWQ"}, want: "azure-ad"},
		"subject of another issuer":  {expected: api.SignInIdentity{Issuer: "https://dex.other.example.com", Subject: "CgVhbGljZRIIYXp1cmUtYWQ"}},
		"subject without connector":  {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZQ"}},
		"connector Dex would refuse": {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRINYmFkIGNvbm5lY3Rvcg"}},
		"no expected identity":       {},
	} {
		t.Run(name, func(t *testing.T) {
			provider := mock.NewProvider()
			var connector string
			authorizationURL := provider.AuthorizationURLFunc
			provider.AuthorizationURLFunc = func(state, challenge, method string, scopes []string, opts *providers.AuthorizationURLOptions) string {
				connector = opts.ConnectorID
				return authorizationURL(state, challenge, method, scopes, opts)
			}
			oauthSrv, err := oauth.NewServerWithCombined(provider, memory.New(), &oauth.ServerConfig{
				Issuer:                      "https://muster.example.com",
				DisableNonceEchoRequirement: true,
			}, slog.Default())
			require.NoError(t, err)
			s := &OAuthHTTPServer{config: config.OAuthServerConfig{
				BaseURL:  "https://muster.example.com",
				Provider: OAuthProviderDex,
				Dex:      config.DexConfig{IssuerURL: "https://dex.example.com", ConnectorID: "github"},
			}, oauthServer: oauthSrv}
			returnURL, err := url.Parse("https://muster.example.com" + config.DefaultOAuthProxyStartPath)
			require.NoError(t, err)
			sum := sha256.Sum256([]byte("identity-verifier-identity-verifier-identity-verifier"))

			_, err = s.SignInURL(t.Context(), returnURL, "identity-state-identity-state", base64.RawURLEncoding.EncodeToString(sum[:]), tc.expected)

			require.NoError(t, err)
			require.Equal(t, tc.want, connector, "empty: the provider's configured connector")
		})
	}
}

// Only a subject of muster's own Dex names a login method.
func TestOAuthHTTPServer_LoginMethod(t *testing.T) {
	dexServer := &OAuthHTTPServer{config: config.OAuthServerConfig{
		Provider: OAuthProviderDex,
		Dex:      config.DexConfig{IssuerURL: "https://dex.example.com"},
	}}
	require.Equal(t, "github", dexServer.LoginMethod(api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIGZ2l0aHVi"}))
	require.Empty(t, dexServer.LoginMethod(api.SignInIdentity{Issuer: "https://dex.other.example.com", Subject: "CgVhbGljZRIGZ2l0aHVi"}))
	require.Empty(t, dexServer.LoginMethod(api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "alice"}))

	googleServer := &OAuthHTTPServer{config: config.OAuthServerConfig{Provider: OAuthProviderGoogle, BaseURL: "https://muster.example.com"}}
	require.Empty(t, googleServer.LoginMethod(api.SignInIdentity{Issuer: "https://muster.example.com", Subject: "CgVhbGljZRIGZ2l0aHVi"}))

	require.Empty(t, (&LazyOAuthHTTPServer{}).LoginMethod(api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIGZ2l0aHVi"}))
}

// Until OIDC discovery completed the check refuses, so no sign-in completes
// unchecked while muster runs degraded.
func TestLazyOAuthHTTPServer_BrowserIdentityRefusesUntilReady(t *testing.T) {
	l := &LazyOAuthHTTPServer{}

	returnURL := &url.URL{Scheme: "https", Host: "muster.example.com", Path: config.DefaultOAuthProxyStartPath}
	_, err := l.SignInURL(t.Context(), returnURL, "state", "challenge", api.SignInIdentity{})
	require.Error(t, err)
	_, err = l.Identity(t.Context(), returnURL, "code", "verifier")
	require.Error(t, err)
}

// A trusted issuer's token names its issuer and raw sub, whatever its
// subjectClaim maps the session subject to; any other bearer was issued for a
// sign-in at muster's own identity provider.
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
}

// An API endpoint behind ValidateTokenWithSubject, such as the Session
// binding agent-manager makes as the person, gets the sign-in identity of the
// validated bearer beside its subject.
func TestOAuthHTTPServer_ValidatedSubjectCarriesTheSignInIdentity(t *testing.T) {
	s := &OAuthHTTPServer{config: config.OAuthServerConfig{
		Provider: OAuthProviderDex,
		Dex:      config.DexConfig{IssuerURL: "https://dex.example.com"},
	}}
	var subject string
	var identity api.SignInIdentity
	h := s.withValidatedSubject(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject = api.GetSubjectFromContext(r.Context())
		identity = api.SignInIdentityFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodPut, "/session-bindings", nil)
	req = req.WithContext(oauthhandler.ContextWithUserInfo(req.Context(), &providers.UserInfo{ID: "CgVhbGljZRIGZ2l0aHVi", Email: "alice@example.com"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "CgVhbGljZRIGZ2l0aHVi", subject)
	require.Equal(t, api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIGZ2l0aHVi"}, identity)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/session-bindings", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code, "no validated user, no identity")
}
