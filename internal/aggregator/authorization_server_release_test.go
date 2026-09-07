package aggregator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

func pinnedAuth(issuer, authEP, tokenEP string) *api.MCPServerAuth {
	return &api.MCPServerAuth{
		Type: "oauth",
		AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
			Issuer:                issuer,
			AuthorizationEndpoint: authEP,
			TokenEndpoint:         tokenEP,
		},
	}
}

func TestReleaseAuthorizationServerPin(t *testing.T) {
	ctx := context.Background()
	const issuer = "https://muster.example.com"
	handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
	api.RegisterOAuthHandler(handler)
	defer api.RegisterOAuthHandler(nil)

	newServer := func(reg *ServerRegistry, name string, auth *api.MCPServerAuth) {
		require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: name},
			URL:                "http://" + name + ".local/mcp",
			AuthInfo:           &AuthInfo{},
			AuthConfig:         auth,
		}))
	}

	t.Run("a pin the server no longer carries is forgotten", func(t *testing.T) {
		a := &AggregatorServer{registry: NewServerRegistry("x")}
		previous := pinnedAuth(issuer, "https://dex.example.com/auth", "https://dex.example.com/token")
		handler.pins[issuer] = api.IssuerPin{AuthorizationEndpoint: "https://dex.example.com/auth"}
		newServer(a.registry, "fixture", &api.MCPServerAuth{Type: "oauth"}) // re-registered without the pin

		a.releaseAuthorizationServerPin(ctx, "fixture", previous)

		_, still := handler.pins[issuer]
		assert.False(t, still, "nobody describes the issuer any more")
	})

	t.Run("a changed description drops the old one before the caller applies the new", func(t *testing.T) {
		a := &AggregatorServer{registry: NewServerRegistry("x")}
		previous := pinnedAuth(issuer, "https://dex.example.com/auth", "https://dex.example.com/token")
		handler.pins[issuer] = api.IssuerPin{AuthorizationEndpoint: "https://dex.example.com/auth"}
		newServer(a.registry, "fixture", pinnedAuth(issuer, "", "")) // same issuer, discovery instead of endpoints

		a.releaseAuthorizationServerPin(ctx, "fixture", previous)

		_, still := handler.pins[issuer]
		assert.False(t, still, "the endpoints of the old description must not survive")
	})

	t.Run("an unchanged description is left alone", func(t *testing.T) {
		a := &AggregatorServer{registry: NewServerRegistry("x")}
		previous := pinnedAuth(issuer, "https://dex.example.com/auth", "https://dex.example.com/token")
		handler.pins[issuer] = api.IssuerPin{AuthorizationEndpoint: "https://dex.example.com/auth"}
		newServer(a.registry, "fixture", pinnedAuth(issuer+"/", "https://dex.example.com/auth", "https://dex.example.com/token"))

		a.releaseAuthorizationServerPin(ctx, "fixture", previous)

		_, still := handler.pins[issuer]
		assert.True(t, still, "the same description (modulo trailing slash) keeps its pin")
	})

	t.Run("another server's description of the issuer takes over", func(t *testing.T) {
		a := &AggregatorServer{registry: NewServerRegistry("x")}
		previous := pinnedAuth(issuer, "https://old.example.com/auth", "https://old.example.com/token")
		handler.pins[issuer] = api.IssuerPin{AuthorizationEndpoint: "https://old.example.com/auth"}
		newServer(a.registry, "fixture", &api.MCPServerAuth{Type: "oauth"})
		newServer(a.registry, "sibling", pinnedAuth(issuer, "https://sibling.example.com/auth", "https://sibling.example.com/token"))

		a.releaseAuthorizationServerPin(ctx, "fixture", previous)

		pin, still := handler.pins[issuer]
		require.True(t, still, "the sibling still describes the issuer")
		assert.Equal(t, "https://sibling.example.com/auth", pin.AuthorizationEndpoint, "the sibling's description replaced the released one")
	})

	t.Run("nothing to release without a previous pin", func(t *testing.T) {
		a := &AggregatorServer{registry: NewServerRegistry("x")}
		handler.pins[issuer] = api.IssuerPin{ClientID: "keep"}
		a.releaseAuthorizationServerPin(ctx, "fixture", &api.MCPServerAuth{Type: "oauth"})
		a.releaseAuthorizationServerPin(ctx, "fixture", nil)
		_, still := handler.pins[issuer]
		assert.True(t, still)
	})
}

func TestApplyAuthorizationServerPin(t *testing.T) {
	pin := &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
		Issuer: "https://dex.example.com/", Scopes: "openid profile",
	}}

	t.Run("the pinned issuer replaces the advertised one", func(t *testing.T) {
		info := &AuthInfo{Issuer: "https://muster.example.com", Scope: "mcp:read", Resource: "https://backend/mcp"}
		assert.True(t, applyAuthorizationServerPin(info, pin))
		assert.Equal(t, "https://dex.example.com", info.Issuer)
		assert.Equal(t, "openid profile", info.Scope)
		assert.Equal(t, "https://backend/mcp", info.Resource, "the resource is not the pin's business")
	})

	t.Run("a pin without scopes keeps the discovered scope", func(t *testing.T) {
		info := &AuthInfo{Issuer: "", Scope: "mcp:read"}
		assert.True(t, applyAuthorizationServerPin(info, &api.MCPServerAuth{AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: "https://dex.example.com"}}))
		assert.Equal(t, "https://dex.example.com", info.Issuer)
		assert.Equal(t, "mcp:read", info.Scope)
	})

	t.Run("no pin, no change", func(t *testing.T) {
		info := &AuthInfo{Issuer: "https://muster.example.com"}
		assert.False(t, applyAuthorizationServerPin(info, &api.MCPServerAuth{Type: "oauth"}))
		assert.False(t, applyAuthorizationServerPin(info, nil))
		assert.False(t, applyAuthorizationServerPin(nil, pin))
		assert.Equal(t, "https://muster.example.com", info.Issuer)
	})

	t.Run("already the pinned values", func(t *testing.T) {
		info := &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid profile"}
		assert.False(t, applyAuthorizationServerPin(info, pin))
	})
}

func TestRegisterPendingAuth_PinIsTheIssuer(t *testing.T) {
	reg := NewServerRegistry("x")
	probe := &AuthInfo{Issuer: "https://muster.example.com", Scope: "mcp:read", ResourceMetadataURL: "http://backend/.well-known/oauth-protected-resource"}
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "fixture"},
		URL:                "http://backend/mcp",
		AuthInfo:           probe,
		AuthConfig:         &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: "https://dex.example.com", Scopes: "openid"}},
	}))
	info, ok := reg.GetServerInfo("fixture")
	require.True(t, ok)
	assert.Equal(t, "https://dex.example.com", info.GetAuthInfo().Issuer, "the registry files the server under the pinned issuer")
	assert.Equal(t, "openid", info.GetAuthInfo().Scope)
	assert.Equal(t, probe.ResourceMetadataURL, info.GetAuthInfo().ResourceMetadataURL)
	assert.Equal(t, "https://muster.example.com", probe.Issuer, "the caller's AuthInfo is not mutated")
	assert.Equal(t, "https://dex.example.com", knownServerIssuer(info))

	// Without a pin the probe's issuer stands.
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "plain"},
		URL:                "http://plain/mcp",
		AuthInfo:           &AuthInfo{Issuer: "https://as.example.com"},
		AuthConfig:         &api.MCPServerAuth{Type: "oauth"},
	}))
	plain, _ := reg.GetServerInfo("plain")
	assert.Equal(t, "https://as.example.com", knownServerIssuer(plain))
}
