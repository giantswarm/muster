package aggregator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

// TestIsServerSSOBased covers the per-session auth modes that must skip global
// registration in the event handler. LocalMint is session-based (a token is
// minted per caller; there is no global persistent client), so it must be
// treated like ForwardToken and TokenExchange.
func TestIsServerSSOBased(t *testing.T) {
	cases := []struct {
		name string
		auth *api.MCPServerAuth
		want bool
	}{
		{"nil auth config", nil, false},
		{"no sso mode", &api.MCPServerAuth{}, false},
		{"forward token", &api.MCPServerAuth{ForwardToken: true}, true},
		{"token exchange", &api.MCPServerAuth{TokenExchange: &api.TokenExchangeConfig{Enabled: true}}, true},
		{"token exchange disabled", &api.MCPServerAuth{TokenExchange: &api.TokenExchangeConfig{Enabled: false}}, false},
		{"local mint enabled", &api.MCPServerAuth{LocalMint: &api.LocalMintConfig{Enabled: true, Audience: "cluster-b"}}, true},
		{"local mint disabled", &api.MCPServerAuth{LocalMint: &api.LocalMintConfig{Enabled: false}}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewServerRegistry("test")
			registry.servers["backend"] = &ServerInfo{Name: "backend", AuthConfig: tc.auth}
			am := &AggregatorManager{aggregatorServer: &AggregatorServer{registry: registry}}

			require.Equal(t, tc.want, am.isServerSSOBased("backend"))
		})
	}
}

func TestIsServerSSOBased_UnknownServer(t *testing.T) {
	am := &AggregatorManager{aggregatorServer: &AggregatorServer{registry: NewServerRegistry("test")}}
	require.False(t, am.isServerSSOBased("missing"))
}

func TestIsServerSSOBased_NoAggregatorServer(t *testing.T) {
	am := &AggregatorManager{}
	require.False(t, am.isServerSSOBased("backend"))
}
