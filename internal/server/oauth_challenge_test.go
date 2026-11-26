package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	oauth "github.com/giantswarm/mcp-oauth"
	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers/mock"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/giantswarm/mcp-oauth/storage/memory"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

func TestMCPDiscoveryScopes(t *testing.T) {
	configuration := config.OAuthServerConfig{
		BaseURL:            "https://muster.example.com",
		ResourceIdentifier: "https://muster.example.com/mcp",
	}
	store := memory.New()
	underlying, err := oauthserver.NewWithCombined(mock.NewProvider(), store, newOAuthServerConfig(configuration, oauthTokenLifetimes{}), nil)
	if err != nil {
		store.Stop()
	}
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, underlying.Shutdown(context.WithoutCancel(t.Context()))) })
	server := &OAuthHTTPServer{
		config:       configuration,
		oauthServer:  underlying,
		oauthHandler: oauthhandler.New(underlying, nil),
		mcpHandler:   http.NotFoundHandler(),
	}
	transport := httptest.NewServer(server.CreateMux())
	t.Cleanup(transport.Close)
	for _, path := range []string{"/mcp", "/sse", "/message"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, transport.URL+path, nil)
		require.NoError(t, err)
		response, err := transport.Client().Do(request)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, http.StatusUnauthorized, response.StatusCode)
		require.Contains(t, response.Header.Get("WWW-Authenticate"), `scope="openid profile email"`)
		require.Contains(t, response.Header.Get("WWW-Authenticate"), `resource_metadata="https://muster.example.com/.well-known/oauth-protected-resource/mcp"`)
	}
	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, transport.URL+path, nil)
		require.NoError(t, err)
		response, err := transport.Client().Do(request)
		require.NoError(t, err)
		var metadata map[string]any
		require.NoError(t, json.NewDecoder(response.Body).Decode(&metadata))
		require.NoError(t, response.Body.Close())
		if path == "/.well-known/oauth-protected-resource/mcp" {
			require.Equal(t, []any{"openid", "profile", "email"}, metadata["scopes_supported"])
		} else {
			require.NotContains(t, metadata, "scopes_supported", "resource discovery must not restrict authorization-server scopes")
		}
	}
}

func TestInsufficientScopeChallengeUsesResourceIdentifier(t *testing.T) {
	server := &OAuthHTTPServer{
		config:      config.OAuthServerConfig{BaseURL: "https://muster.example.com", ResourceIdentifier: "https://muster.example.com/mcp"},
		oauthServer: &oauth.Server{Config: &oauthserver.Config{Issuer: "https://muster.example.com", ResourceIdentifier: "https://muster.example.com/mcp"}},
	}
	challenge := server.insufficientScopeChallenge("the token grants no toolset")
	require.Contains(t, challenge, `resource_metadata="https://muster.example.com/.well-known/oauth-protected-resource/mcp"`)
	require.NotContains(t, challenge, `scope=`, "the required toolset is caller-specific")
}

func TestMCPMetadataPreservesDefaultResource(t *testing.T) {
	configuration := newOAuthServerConfig(config.OAuthServerConfig{BaseURL: "https://muster.example.com"}, oauthTokenLifetimes{})
	metadata := configuration.ResourceMetadataByPath["/mcp"]
	require.Equal(t, "https://muster.example.com", configuration.GetResourceIdentifier())
	require.Equal(t, "https://muster.example.com", configuration.PathResourceIdentifier("/mcp", metadata))
}
