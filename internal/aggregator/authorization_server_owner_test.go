package aggregator

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

const ownerTestIssuer = "https://as.example.com"

var ownerTestEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// describingServer is a server created createdAfter the epoch that pins
// ownerTestIssuer with the endpoints at host and the client in secret.
func describingServer(name string, createdAfter time.Duration, host, secret string) *ServerInfo {
	return &ServerInfo{
		Name:      name,
		CreatedAt: ownerTestEpoch.Add(createdAfter),
		AuthConfig: &api.MCPServerAuth{
			Type: "oauth",
			AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
				Issuer:                     ownerTestIssuer,
				AuthorizationEndpoint:      "https://" + host + "/authorize",
				TokenEndpoint:              "https://" + host + "/token",
				ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{Name: secret},
			},
		},
	}
}

func registerDescribing(t *testing.T, a *AggregatorServer, info *ServerInfo) {
	t.Helper()
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: info.Name, CreatedAt: info.CreatedAt},
		URL:                "http://" + info.Name + ".local/mcp",
		AuthInfo:           &AuthInfo{},
		AuthConfig:         info.AuthConfig,
	}))
	t.Cleanup(func() { require.NoError(t, a.registry.Deregister(info.Name)) })
}

func TestIssuerOwner(t *testing.T) {
	plain := &ServerInfo{Name: "a-plain", AuthConfig: &api.MCPServerAuth{
		Type:                "oauth",
		AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: ownerTestIssuer},
	}}
	tenant := describingServer("org-a/oldest", -time.Hour, "tenant.example.com", "tenant")

	tests := []struct {
		name      string
		described []*ServerInfo
		want      string
	}{
		{
			name:      "the oldest MCPServer owns the key",
			described: []*ServerInfo{describingServer("a", time.Minute, "a.example.com", "a"), describingServer("b", 0, "b.example.com", "b")},
			want:      "b",
		},
		{
			name:      "the first by name breaks a tie",
			described: []*ServerInfo{describingServer("b", 0, "b.example.com", "b"), describingServer("a", 0, "a.example.com", "a")},
			want:      "a",
		},
		{
			name:      "a server that pins nothing and a server of another key own nothing",
			described: []*ServerInfo{plain, tenant, describingServer("b", time.Minute, "b.example.com", "b")},
			want:      "b",
		},
		{
			name:      "no owner without a server that pins",
			described: []*ServerInfo{plain},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			described := make(map[string]*ServerInfo)
			for _, info := range tc.described {
				described[info.Name] = info
			}
			owner := issuerOwner(described, ownerTestIssuer)
			if tc.want == "" {
				require.Nil(t, owner)
				return
			}
			require.NotNil(t, owner)
			assert.Equal(t, tc.want, owner.Name)
		})
	}
}

func TestPinAuthorizationServer_OneDescriptionPerIssuerKey(t *testing.T) {
	handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
	api.RegisterOAuthHandler(handler)
	defer api.RegisterOAuthHandler(nil)
	secrets := &mockSecretCredentialsHandler{credentials: &api.ClientCredentials{ClientID: "owner-client", ClientSecret: "owner-secret"}}
	api.RegisterSecretCredentialsHandler(secrets)
	defer api.RegisterSecretCredentialsHandler(nil)

	a := bareAggregator()
	owner := describingServer("owner", 0, "owner.example.com", "owner-client")
	registerDescribing(t, a, owner)

	t.Run("a newer server with another description is refused and pins nothing", func(t *testing.T) {
		handler.pins = map[string]api.IssuerPin{}
		newer := describingServer("newer", time.Minute, "elsewhere.example.com", "newer-client")
		registerDescribing(t, a, newer)

		err := a.pinAuthorizationServer(t.Context(), newer)
		require.ErrorIs(t, err, errAuthorizationServerConflict)
		assert.Contains(t, err.Error(), "server owner")
		assert.Empty(t, handler.pins)

		_, err = a.resolveServerAuthInfo(t.Context(), newer)
		require.ErrorIs(t, err, errAuthorizationServerConflict, "the newer server's sign-in is refused")
	})

	t.Run("the owner keeps its description while a conflicting server exists", func(t *testing.T) {
		handler.pins = map[string]api.IssuerPin{}
		newer := describingServer("newer", time.Minute, "elsewhere.example.com", "newer-client")
		registerDescribing(t, a, newer)

		require.NoError(t, a.pinAuthorizationServer(t.Context(), owner))
		assert.Equal(t, "https://owner.example.com/token", handler.pins[ownerTestIssuer].TokenEndpoint)
		assert.Equal(t, "owner-client", handler.pins[ownerTestIssuer].ClientID)
	})

	t.Run("a server with the same description shares the owner's pin", func(t *testing.T) {
		handler.pins = map[string]api.IssuerPin{}
		same := describingServer("same", time.Minute, "owner.example.com", "owner-client")
		registerDescribing(t, a, same)

		require.NoError(t, a.pinAuthorizationServer(t.Context(), same))
		assert.Equal(t, "https://owner.example.com/token", handler.pins[ownerTestIssuer].TokenEndpoint)
		require.NotNil(t, secrets.lastOwner)
		assert.Equal(t, "owner", secrets.lastOwner.Name, "the owner's Secret binding is the one read")
	})
}

func TestReleaseAuthorizationServerPin_HandsTheKeyToTheNextOwner(t *testing.T) {
	handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
	api.RegisterOAuthHandler(handler)
	defer api.RegisterOAuthHandler(nil)
	secrets := &mockSecretCredentialsHandler{credentials: &api.ClientCredentials{ClientID: "next-client"}}
	api.RegisterSecretCredentialsHandler(secrets)
	defer api.RegisterSecretCredentialsHandler(nil)

	a := bareAggregator()
	gone := describingServer("gone", 0, "gone.example.com", "gone-client")
	registerDescribing(t, a, describingServer("later", 2*time.Minute, "later.example.com", "later-client"))
	registerDescribing(t, a, describingServer("next", time.Minute, "next.example.com", "next-client"))
	handler.pins[ownerTestIssuer] = api.IssuerPin{TokenEndpoint: "https://gone.example.com/token"}

	a.releaseAuthorizationServerPin(t.Context(), gone.Name, gone.AuthConfig)

	assert.Equal(t, "https://next.example.com/token", handler.pins[ownerTestIssuer].TokenEndpoint, "the next oldest server owns the key")
	assert.Empty(t, handler.unpinned, "the key is never left without a pin while an owner exists")

	t.Run("unpinned when the next owner's description cannot be pinned", func(t *testing.T) {
		secrets.err = errors.New("secret not found")
		defer func() { secrets.err = nil }()
		handler.pins[ownerTestIssuer] = api.IssuerPin{TokenEndpoint: "https://gone.example.com/token"}

		a.releaseAuthorizationServerPin(t.Context(), gone.Name, gone.AuthConfig)

		_, still := handler.pins[ownerTestIssuer]
		assert.False(t, still, "the released description must not keep standing")
	})
}
