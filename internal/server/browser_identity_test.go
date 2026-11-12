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

const (
	testIdentityIssuer    = "https://muster.example.com"
	testIdentityVerifier  = "identity-verifier-identity-verifier-identity-verifier"
	testIdentityStartPath = "https://muster.example.com" + config.DefaultOAuthProxyStartPath
)

// identityTestServer is an OAuthHTTPServer on a mock provider that records
// what the identity sign-in asked the provider for.
type identityTestServer struct {
	*OAuthHTTPServer
	providerState string
	connector     string
}

func newIdentityTestServer(t *testing.T, cfg config.OAuthServerConfig) *identityTestServer {
	t.Helper()
	ts := &identityTestServer{}
	provider := mock.NewProvider()
	authorizationURL := provider.AuthorizationURLFunc
	provider.AuthorizationURLFunc = func(state, challenge, method string, scopes []string, opts *providers.AuthorizationURLOptions) string {
		ts.providerState = state
		ts.connector = opts.ConnectorID
		return authorizationURL(state, challenge, method, scopes, opts)
	}
	store := memory.New()
	t.Cleanup(store.Stop)
	oauthSrv, err := oauth.NewServerWithCombined(provider, store, &oauth.ServerConfig{
		Issuer:                      testIdentityIssuer,
		DisableNonceEchoRequirement: true,
	}, slog.Default())
	require.NoError(t, err)
	cfg.BaseURL = testIdentityIssuer
	ts.OAuthHTTPServer = &OAuthHTTPServer{config: cfg, oauthServer: oauthSrv}
	return ts
}

func testIdentityReturnURL(t *testing.T) *url.URL {
	t.Helper()
	returnURL, err := url.Parse(testIdentityStartPath)
	require.NoError(t, err)
	return returnURL
}

func testIdentityChallenge() string {
	sum := sha256.Sum256([]byte(testIdentityVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// The sign-in check runs mcp-oauth's identity flow: the browser comes back to
// the OAuth proxy start endpoint, and the code it brings yields the subject.
func TestOAuthHTTPServer_BrowserIdentity(t *testing.T) {
	s := newIdentityTestServer(t, config.OAuthServerConfig{})
	returnURL := testIdentityReturnURL(t)
	state := "identity-state-identity-state"

	signIn, err := s.SignInURL(t.Context(), returnURL, state, testIdentityChallenge(), api.SignInIdentity{})
	require.NoError(t, err)
	require.NotEmpty(t, signIn)

	code, clientState, err := s.oauthServer.HandleProviderCallback(t.Context(), s.providerState, "provider-code")
	require.NoError(t, err)
	require.Equal(t, state, clientState)
	require.Equal(t, returnURL.String(), code.RedirectURI)

	identity, err := s.Identity(t.Context(), returnURL, code.Code, testIdentityVerifier)
	require.NoError(t, err)
	require.Equal(t, testIdentityIssuer, identity.Issuer)
	require.NotEmpty(t, identity.Subject)
}

// testDexIdentityConfig is muster's OAuth server on Dex with github as the
// configured connector and azure-ad allowed for the sign-in check.
var testDexIdentityConfig = config.OAuthServerConfig{
	Provider: OAuthProviderDex,
	Dex: config.DexConfig{
		IssuerURL:               "https://dex.example.com",
		ConnectorID:             "github",
		IdentityCheckConnectors: []string{"azure-ad"},
	},
}

// With Dex, the sign-in check goes to the connector the expected subject was
// issued through when the operator allows it for the check, so a session from
// another login method than the configured connector can pass it. Any other
// identity keeps the configured connector.
func TestOAuthHTTPServer_SignInURLUsesTheExpectedLoginMethod(t *testing.T) {
	for name, tc := range map[string]struct {
		expected api.SignInIdentity
		want     string
	}{
		"allowed connector":          {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIIYXp1cmUtYWQ"}, want: "azure-ad"},
		"configured connector":       {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIGZ2l0aHVi"}},
		"connector not allowed":      {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIEbGRhcA"}},
		"connector Dex would refuse": {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRINYmFkIGNvbm5lY3Rvcg"}},
		"subject of another issuer":  {expected: api.SignInIdentity{Issuer: "https://dex.other.example.com", Subject: "CgVhbGljZRIIYXp1cmUtYWQ"}},
		"subject without connector":  {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZQ"}},
		"no expected identity":       {},
	} {
		t.Run(name, func(t *testing.T) {
			s := newIdentityTestServer(t, testDexIdentityConfig)

			_, err := s.SignInURL(t.Context(), testIdentityReturnURL(t), "identity-state-identity-state", testIdentityChallenge(), tc.expected)

			require.NoError(t, err)
			require.Equal(t, tc.want, s.connector, "empty: the provider's configured connector")
		})
	}
}

// The check signs the browser in at muster's issuer and compares issuer and
// sub, so a sign-in is refused up front for every session it cannot pass for,
// and allowed for the configured connector, a listed one, and a subject Dex
// encoded no connector in.
func TestOAuthHTTPServer_CheckRefusal(t *testing.T) {
	cfg := testDexIdentityConfig
	cfg.Dex.IdentityCheckConnectors = []string{"azure-ad", "simple-oidc"}
	s := newIdentityTestServer(t, cfg)

	for name, tc := range map[string]struct {
		expected api.SignInIdentity
		refused  bool
	}{
		"configured connector":                       {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIGZ2l0aHVi"}},
		"listed connector":                           {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIIYXp1cmUtYWQ"}},
		"subject exchanged through listed connector": {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRILc2ltcGxlLW9pZGM"}},
		"subject without connector":                  {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZQ"}},
		"unlisted connector":                         {expected: api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIEbGRhcA"}, refused: true},
		"subject of another issuer":                  {expected: api.SignInIdentity{Issuer: "https://dex.other.example.com", Subject: "CgVhbGljZRIGZ2l0aHVi"}, refused: true},
		"no expected identity":                       {refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := s.CheckRefusal(tc.expected)
			if !tc.refused {
				require.NoError(t, err)
				return
			}
			require.True(t, api.IsSignInCheckRefused(err), "got %v", err)
		})
	}
}

// Without dex.connectorId the check shows Dex's connector chooser, so a
// session of any connector can pass it.
func TestOAuthHTTPServer_CheckRefusalWithoutConfiguredConnector(t *testing.T) {
	cfg := testDexIdentityConfig
	cfg.Dex.ConnectorID = ""
	s := newIdentityTestServer(t, cfg)

	require.NoError(t, s.CheckRefusal(api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIEbGRhcA"}))
}

// A muster that started before Dex answered refuses nothing up front: the
// link's check fails in SignInURL until discovery completed.
func TestLazyOAuthHTTPServer_CheckRefusal(t *testing.T) {
	require.NoError(t, (&LazyOAuthHTTPServer{}).CheckRefusal(api.SignInIdentity{}))

	s := newIdentityTestServer(t, testDexIdentityConfig)
	l := &LazyOAuthHTTPServer{inner: s.OAuthHTTPServer}
	require.True(t, api.IsSignInCheckRefused(l.CheckRefusal(api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIEbGRhcA"})))
}

// Without identityCheckConnectors the check keeps the configured connector,
// so it is never weaker than that connector.
func TestOAuthHTTPServer_SignInURLKeepsTheConfiguredConnectorByDefault(t *testing.T) {
	cfg := testDexIdentityConfig
	cfg.Dex.IdentityCheckConnectors = nil
	s := newIdentityTestServer(t, cfg)

	_, err := s.SignInURL(t.Context(), testIdentityReturnURL(t), "identity-state-identity-state", testIdentityChallenge(),
		api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIIYXp1cmUtYWQ"})

	require.NoError(t, err)
	require.Empty(t, s.connector)
}

// A muster that started before Dex answered hands the expected identity on
// once ready, so it picks the same connector.
func TestLazyOAuthHTTPServer_SignInURLPassesTheExpectedIdentity(t *testing.T) {
	s := newIdentityTestServer(t, testDexIdentityConfig)
	l := &LazyOAuthHTTPServer{inner: s.OAuthHTTPServer}

	_, err := l.SignInURL(t.Context(), testIdentityReturnURL(t), "identity-state-identity-state", testIdentityChallenge(),
		api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgVhbGljZRIIYXp1cmUtYWQ"})

	require.NoError(t, err)
	require.Equal(t, "azure-ad", s.connector)
}

// A connector Dex would refuse fails muster's start instead of every sign-in
// check it would be used for.
func TestValidateIdentityCheckConnectors(t *testing.T) {
	require.NoError(t, validateIdentityCheckConnectors(testDexIdentityConfig))
	require.NoError(t, validateIdentityCheckConnectors(config.OAuthServerConfig{Provider: OAuthProviderDex}))

	cfg := testDexIdentityConfig
	cfg.Dex.IdentityCheckConnectors = []string{"azure-ad", "bad connector"}
	require.ErrorContains(t, validateIdentityCheckConnectors(cfg), "bad connector")

	cfg = config.OAuthServerConfig{Provider: OAuthProviderGoogle, Dex: config.DexConfig{IdentityCheckConnectors: []string{"azure-ad"}}}
	require.ErrorContains(t, validateIdentityCheckConnectors(cfg), "dex")
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
