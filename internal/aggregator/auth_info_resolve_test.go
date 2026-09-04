package aggregator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

func TestResolveServerAuthInfo(t *testing.T) {
	ctx := context.Background()
	a := &AggregatorServer{}

	t.Run("fills the issuer from the pinned authorization server without a login and records it", func(t *testing.T) {
		handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
		api.RegisterOAuthHandler(handler)
		defer api.RegisterOAuthHandler(nil)
		secrets := &mockSecretCredentialsHandler{credentials: &api.ClientCredentials{ClientID: "Iv23liApp", ClientSecret: "shh"}}
		api.RegisterSecretCredentialsHandler(secrets)
		defer api.RegisterSecretCredentialsHandler(nil)

		// What the 401 at registration leaves behind after a restart: the
		// resource-metadata pointer, no issuer.
		server := githubStyleServer(&api.ClientCredentialsSecretRef{Name: "github-oauth-client"}, api.GrantScopeSubject)
		server.AuthInfo = &AuthInfo{ResourceMetadataURL: "https://api.githubcopilot.com/.well-known/oauth-protected-resource"}

		info, err := a.resolveServerAuthInfo(ctx, server)
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/login/oauth", info.Issuer)
		assert.Equal(t, "repo read:org", info.Scope)
		assert.NotEmpty(t, info.Resource)
		assert.Equal(t, "https://api.githubcopilot.com/.well-known/oauth-protected-resource", info.ResourceMetadataURL, "the 401-time fields are kept")

		assert.Equal(t, info, server.GetAuthInfo(), "recorded on the registry entry for every later caller")
		assert.Contains(t, handler.pins, "https://github.com/login/oauth", "the pin is applied on the way")
		assert.True(t, oauthProtected(server))
	})

	t.Run("leaves a complete AuthInfo alone and touches no handler", func(t *testing.T) {
		api.RegisterOAuthHandler(nil)
		complete := &AuthInfo{Issuer: "https://issuer.example", Scope: "read", Resource: "https://mcp.example/mcp"}
		server := &ServerInfo{Name: "done", URL: "https://mcp.example/mcp", AuthInfo: complete}

		info, err := a.resolveServerAuthInfo(ctx, server)
		require.NoError(t, err)
		assert.Equal(t, *complete, *info)
		assert.Same(t, complete, server.GetAuthInfo())
	})

	t.Run("reports a pin it cannot apply instead of guessing", func(t *testing.T) {
		api.RegisterOAuthHandler(nil)
		server := githubStyleServer(&api.ClientCredentialsSecretRef{Name: "github-oauth-client"}, api.GrantScopeSubject)

		_, err := a.resolveServerAuthInfo(ctx, server)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "OAuth proxy is disabled")
	})

	t.Run("a server without auth is not OAuth-protected", func(t *testing.T) {
		assert.False(t, oauthProtected(&ServerInfo{Name: "plain"}))
		assert.False(t, oauthProtected(&ServerInfo{Name: "fwd", AuthConfig: &api.MCPServerAuth{Type: "forward"}}))
		assert.True(t, oauthProtected(&ServerInfo{Name: "oauth", AuthConfig: &api.MCPServerAuth{Type: "oauth"}}))
	})
}
