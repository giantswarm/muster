package aggregator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// errSecretNotBound is how the Secret reader refuses a Secret not bound to
// the MCPServer that names it (secretBinding.mode enforce).
var errSecretNotBound = fmt.Errorf("MCPServer org-a/github may not read Secret org-a/github-oauth-client: %w", api.ErrSecretNotBound)

// tenantGithub is the MCPServer github of org-a, as the registry holds it.
func tenantGithub(secretRef *api.ClientCredentialsSecretRef) *ServerInfo {
	info := githubStyleServer(secretRef, api.GrantScopeSubject)
	info.Name, info.Namespace, info.UID = "org-a/github", "org-a", "uid-github"
	return info
}

var tenantGithubOwner = api.SecretOwner{Name: "github", Namespace: "org-a", UID: "uid-github"}

// TestPinReadsTheSecretForTheMCPServer: the client Secret of
// spec.auth.authorizationServer is read for the MCPServer, and a refused
// Secret pins nothing.
func TestPinReadsTheSecretForTheMCPServer(t *testing.T) {
	handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })
	ref := &api.ClientCredentialsSecretRef{Name: "github-oauth-client"}

	secrets := &mockSecretCredentialsHandler{credentials: &api.ClientCredentials{ClientID: "id", ClientSecret: "shh"}}
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(nil) })

	require.NoError(t, bareAggregator().pinAuthorizationServer(context.Background(), tenantGithub(ref)))
	require.NotNil(t, secrets.lastOwner)
	assert.Equal(t, tenantGithubOwner, *secrets.lastOwner)
	assert.Equal(t, api.SecretFieldAuthorizationServer, secrets.lastField)
	assert.Zero(t, secrets.configCalls, "an MCPServer's Secret is never read as configuration")

	handler.pins = map[string]api.IssuerPin{}
	api.RegisterSecretCredentialsHandler(&mockSecretCredentialsHandler{err: errSecretNotBound})
	err := bareAggregator().pinAuthorizationServer(context.Background(), tenantGithub(ref))
	require.ErrorIs(t, err, api.ErrSecretNotBound)
	assert.Empty(t, handler.pins, "a refused Secret is never pinned")
}

// TestPinSecretReadableChecksTheBinding: the sign-in diagnosis reads the
// Secret the way the pin does, so it reports the same refusal.
func TestPinSecretReadableChecksTheBinding(t *testing.T) {
	secrets := &mockSecretCredentialsHandler{err: errSecretNotBound}
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(nil) })

	definition := &api.MCPServerInfo{Name: "github", Namespace: "org-a", UID: "uid-github", Auth: &api.MCPServerAuth{
		Type: "oauth",
		AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
			Issuer:                     "https://github.com/login/oauth",
			ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{Name: "github-oauth-client"},
		},
	}}
	err := pinSecretReadable(context.Background(), "org-a/github", definition)
	require.ErrorIs(t, err, api.ErrSecretNotBound)
	assert.Contains(t, err.Error(), "load client credentials for server org-a/github")
	require.NotNil(t, secrets.lastOwner)
	assert.Equal(t, tenantGithubOwner, *secrets.lastOwner)
	assert.Equal(t, api.SecretFieldAuthorizationServer, secrets.lastField)
}

// TestTokenExchangeSecretIsReadForTheMCPServer: the client Secret of
// spec.auth.tokenExchange is read for the MCPServer, and a refused Secret
// stops the exchange before anything is posted to the token endpoint.
func TestTokenExchangeSecretIsReadForTheMCPServer(t *testing.T) {
	subjectToken := unsignedJWT(t, map[string]any{"sub": "alice", "exp": time.Now().Add(time.Hour).Unix()})
	exchanges := 0
	api.RegisterOAuthHandler(&issuerMockOAuthHandler{
		enabled:          true,
		getFullTokenFunc: func(string, string) *api.OAuthToken { return &api.OAuthToken{IDToken: subjectToken} },
		exchangeFunc: func(context.Context, string, string, *api.TokenExchangeConfig) (string, error) {
			exchanges++
			return "", errors.New("must not be reached")
		},
	})
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })
	secrets := &mockSecretCredentialsHandler{err: errSecretNotBound}
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(nil) })

	serverInfo := &ServerInfo{
		Name: "org-a/remote", Namespace: "org-a", UID: "uid-remote", URL: "http://127.0.0.1:1/mcp",
		AuthConfig: &api.MCPServerAuth{TokenExchange: &api.TokenExchangeConfig{
			Enabled:                    true,
			DexTokenEndpoint:           "https://dex.remote.example/token",
			ConnectorID:                "remote",
			ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{Name: "creds"},
		}},
	}
	ctx := api.WithSessionID(api.WithSubject(context.Background(), "alice"), "sess-1")
	_, err := EstablishConnectionWithTokenExchange(ctx, &AggregatorServer{}, serverInfo, "https://muster.example.com")

	require.ErrorIs(t, err, api.ErrSecretNotBound)
	assert.Zero(t, exchanges, "a refused Secret's credentials never reach the token endpoint")
	require.NotNil(t, secrets.lastOwner)
	assert.Equal(t, api.SecretOwner{Name: "remote", Namespace: "org-a", UID: "uid-remote"}, *secrets.lastOwner)
	assert.Equal(t, api.SecretFieldTokenExchange, secrets.lastField)
}

// TestSessionHeadersReadForTheMCPServer: spec.headersFrom of a session-scoped
// client is read for the MCPServer, and a refused Secret fails the connect.
func TestSessionHeadersReadForTheMCPServer(t *testing.T) {
	secrets := &sessionHeaderSecrets{values: map[string]string{"api-key": "k3y"}}
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(nil) })
	info := &ServerInfo{Name: "org-a/github", Namespace: "org-a", UID: "uid-github", HeadersFrom: []api.MCPServerHeaderFrom{{
		Name:      "X-API-Key",
		ValueFrom: api.MCPServerValueSource{Type: api.ValueSourceTypeSecret, Name: "github-token", Key: "api-key"},
	}}}

	_, err := sessionHeaders(context.Background(), info)
	require.NoError(t, err)
	assert.Equal(t, []api.SecretOwner{tenantGithubOwner}, secrets.owners)

	info.HeadersFrom[0].ValueFrom.Key = "refused"
	headers, err := sessionHeaders(context.Background(), info)
	require.Error(t, err)
	assert.Nil(t, headers, "no header of a refused Secret is sent")
}

// TestPinDeferredIsLoggedOncePerReason: the pin is retried at every
// registration, definition sync and login; the same refusal is logged once,
// a new reason or a deferral after a successful pin again.
func TestPinDeferredIsLoggedOncePerReason(t *testing.T) {
	var logs bytes.Buffer
	logging.InitForCLI(logging.LevelInfo, &logs)
	t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, os.Stderr) })
	info := tenantGithub(nil)
	t.Cleanup(func() { forgetPinDeferral(info) })

	for range 5 {
		logPinDeferred(info, errSecretNotBound)
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "pin deferred"))

	logPinDeferred(info, errors.New("secret not found"))
	assert.Equal(t, 2, strings.Count(logs.String(), "pin deferred"), "a new reason is logged")

	forgetPinDeferral(info)
	logPinDeferred(info, errors.New("secret not found"))
	assert.Equal(t, 3, strings.Count(logs.String(), "pin deferred"), "a deferral after a pin is logged")

	other := tenantGithub(nil)
	other.UID = "uid-recreated"
	t.Cleanup(func() { forgetPinDeferral(other) })
	logPinDeferred(other, errors.New("secret not found"))
	assert.Equal(t, 4, strings.Count(logs.String(), "pin deferred"), "a recreated MCPServer is logged afresh")
}

// TestPinDeferralsGoWithTheServer: a deleted server's record goes, so the
// record does not grow with every server that comes and goes.
func TestPinDeferralsGoWithTheServer(t *testing.T) {
	a := newSuspensionTestServer(t)
	info := tenantGithub(nil)
	other := &ServerInfo{Name: "org-a/github-two", UID: "u2"}
	t.Cleanup(func() { forgetPinDeferral(other) })
	logPinDeferred(info, errSecretNotBound)
	logPinDeferred(other, errSecretNotBound)

	_ = a.DeregisterServer(info.Name)

	_, kept := pinDeferrals.Load(pinDeferralKey(info))
	assert.False(t, kept, "the deleted server's record is gone")
	_, kept = pinDeferrals.Load(pinDeferralKey(other))
	assert.True(t, kept, "a server whose name starts the same keeps its record")
}
