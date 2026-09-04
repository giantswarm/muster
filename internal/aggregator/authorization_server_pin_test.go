package aggregator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

// pinCaptureHandler is an OAuth handler that records authorization server pins.
type pinCaptureHandler struct {
	*mockOAuthHandler
	pins map[string]api.IssuerPin
}

func (p *pinCaptureHandler) PinIssuer(issuer string, pin api.IssuerPin) {
	p.pins[issuer] = pin
}

var _ api.IssuerPinner = (*pinCaptureHandler)(nil)

func githubStyleServer(secretRef *api.ClientCredentialsSecretRef, grantScope string) *ServerInfo {
	return &ServerInfo{
		Name:      "github",
		Namespace: "agent-platform",
		URL:       "https://api.githubcopilot.com/mcp/",
		AuthConfig: &api.MCPServerAuth{
			Type: "oauth",
			AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
				Issuer:                     "https://github.com/login/oauth/",
				Scopes:                     "repo read:org",
				AuthorizationEndpoint:      "https://github.com/login/oauth/authorize",
				TokenEndpoint:              "https://github.com/login/oauth/access_token",
				ClientCredentialsSecretRef: secretRef,
				GrantScope:                 grantScope,
			},
		},
	}
}

func TestPinAuthorizationServer(t *testing.T) {
	ctx := context.Background()
	secretRef := &api.ClientCredentialsSecretRef{Name: "github-oauth-client"}

	t.Run("nothing to pin for a plain issuer override or no auth", func(t *testing.T) {
		require.NoError(t, pinAuthorizationServer(ctx, nil))
		require.NoError(t, pinAuthorizationServer(ctx, &ServerInfo{Name: "plain"}))
		plain := &ServerInfo{Name: "plain", AuthConfig: &api.MCPServerAuth{
			Type:                "oauth",
			AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: "https://cf.mcp.example.com"},
		}}
		// No handler registered at all: a plain override never needs one.
		api.RegisterOAuthHandler(nil)
		require.NoError(t, pinAuthorizationServer(ctx, plain))
	})

	t.Run("hands endpoints, secret credentials and grant scope to the handler", func(t *testing.T) {
		handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
		api.RegisterOAuthHandler(handler)
		defer api.RegisterOAuthHandler(nil)
		secrets := &mockSecretCredentialsHandler{credentials: &api.ClientCredentials{ClientID: "Iv23liApp", ClientSecret: "shh"}}
		api.RegisterSecretCredentialsHandler(secrets)
		defer api.RegisterSecretCredentialsHandler(nil)

		require.NoError(t, pinAuthorizationServer(ctx, githubStyleServer(secretRef, api.GrantScopeSubject)))

		pin, ok := handler.pins["https://github.com/login/oauth"]
		require.True(t, ok, "pin is filed under the normalized issuer, got %v", handler.pins)
		assert.Equal(t, "https://github.com/login/oauth/authorize", pin.AuthorizationEndpoint)
		assert.Equal(t, "https://github.com/login/oauth/access_token", pin.TokenEndpoint)
		assert.Equal(t, "Iv23liApp", pin.ClientID)
		assert.Equal(t, "shh", pin.ClientSecret)
		assert.True(t, pin.SubjectScoped)
		assert.Equal(t, 1, secrets.loadCalls)
		assert.Equal(t, secretRef, secrets.lastSecretRef)
		assert.Equal(t, "agent-platform", secrets.lastDefaultNS, "the MCPServer's namespace is the Secret's default")
	})

	t.Run("pins endpoints without a secret", func(t *testing.T) {
		handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
		api.RegisterOAuthHandler(handler)
		defer api.RegisterOAuthHandler(nil)

		require.NoError(t, pinAuthorizationServer(ctx, githubStyleServer(nil, "")))
		pin := handler.pins["https://github.com/login/oauth"]
		assert.Empty(t, pin.ClientID)
		assert.False(t, pin.SubjectScoped)
		assert.Equal(t, "https://github.com/login/oauth/access_token", pin.TokenEndpoint)
	})

	t.Run("fails when the secret cannot be read", func(t *testing.T) {
		handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
		api.RegisterOAuthHandler(handler)
		defer api.RegisterOAuthHandler(nil)
		api.RegisterSecretCredentialsHandler(&mockSecretCredentialsHandler{err: errors.New("secret not found")})
		defer api.RegisterSecretCredentialsHandler(nil)

		err := pinAuthorizationServer(ctx, githubStyleServer(secretRef, api.GrantScopeSubject))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "secret not found")
		assert.Empty(t, handler.pins, "nothing is pinned on failure")
	})

	t.Run("fails when the OAuth proxy is disabled or cannot pin", func(t *testing.T) {
		api.RegisterOAuthHandler(newMockOAuthHandler(false))
		defer api.RegisterOAuthHandler(nil)
		require.Error(t, pinAuthorizationServer(ctx, githubStyleServer(nil, api.GrantScopeSubject)))

		api.RegisterOAuthHandler(newMockOAuthHandler(true)) // enabled but no IssuerPinner
		require.Error(t, pinAuthorizationServer(ctx, githubStyleServer(nil, api.GrantScopeSubject)))
	})
}
