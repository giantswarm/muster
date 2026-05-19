package translate_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/reconciler/agentgateway/translate"
	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

func TestInfoToMCPServerSpec_NoAuth(t *testing.T) {
	t.Parallel()

	info := &api.MCPServerInfo{
		Name:        "demo",
		Type:        "stdio",
		ToolPrefix:  "demo_",
		Description: "demo server",
		AutoStart:   true,
		Command:     "/usr/local/bin/demo",
		Args:        []string{"--flag", "value"},
		URL:         "https://example.com/mcp",
		Env:         map[string]string{"FOO": "bar"},
		Headers:     map[string]string{"X-Demo": "true"},
		Timeout:     5,
	}

	spec := translate.InfoToMCPServerSpec(info)

	require.Equal(t, info.Type, spec.Type)
	require.Equal(t, info.ToolPrefix, spec.ToolPrefix)
	require.Equal(t, info.Description, spec.Description)
	require.Equal(t, info.AutoStart, spec.AutoStart)
	require.Equal(t, info.Command, spec.Command)
	require.Equal(t, info.Args, spec.Args)
	require.Equal(t, info.URL, spec.URL)
	require.Equal(t, info.Env, spec.Env)
	require.Equal(t, info.Headers, spec.Headers)
	require.Equal(t, info.Timeout, spec.Timeout)
	require.Nil(t, spec.Auth)
}

func TestInfoToMCPServerSpec_WithFullAuth(t *testing.T) {
	t.Parallel()

	info := &api.MCPServerInfo{
		Name: "auth-server",
		Type: "streamable-http",
		URL:  "https://api.example.com/mcp",
		Auth: &api.MCPServerAuth{
			Type:              "oauth",
			ForwardToken:      true,
			RequiredAudiences: []string{"dex-k8s", "platform-api"},
			AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
				Issuer: "https://dex.example.com",
				Scopes: "openid profile",
			},
		},
	}

	spec := translate.InfoToMCPServerSpec(info)

	require.NotNil(t, spec.Auth)
	require.Equal(t, "oauth", spec.Auth.Type)
	require.True(t, spec.Auth.ForwardToken)
	require.Equal(t, []string{"dex-k8s", "platform-api"}, spec.Auth.RequiredAudiences)
	require.NotNil(t, spec.Auth.AuthorizationServer)
	require.Equal(t, musterv1alpha1.IssuerURL("https://dex.example.com"), spec.Auth.AuthorizationServer.Issuer)
	require.Equal(t, "openid profile", spec.Auth.AuthorizationServer.Scopes)
}

func TestMCPServerAuthFromAPI_TokenExchange(t *testing.T) {
	t.Parallel()

	auth := &api.MCPServerAuth{
		Type: "oauth",
		TokenExchange: &api.TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://dex.example.com/token",
			ExpectedIssuer:   "https://dex.example.com",
			ConnectorID:      "kubernetes",
			Scopes:           "openid audience:server:client_id:dex-k8s",
			ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{
				Name:            "dex-credentials",
				Namespace:       "muster-system",
				ClientIDKey:     "client-id",
				ClientSecretKey: "client-secret",
			},
		},
	}

	out := translate.MCPServerAuthFromAPI(auth)

	require.NotNil(t, out.TokenExchange)
	require.True(t, out.TokenExchange.Enabled)
	require.Equal(t, "https://dex.example.com/token", out.TokenExchange.DexTokenEndpoint)
	require.Equal(t, "https://dex.example.com", out.TokenExchange.ExpectedIssuer)
	require.Equal(t, "kubernetes", out.TokenExchange.ConnectorID)
	require.Equal(t, "openid audience:server:client_id:dex-k8s", out.TokenExchange.Scopes)

	require.NotNil(t, out.TokenExchange.ClientCredentialsSecretRef)
	ref := out.TokenExchange.ClientCredentialsSecretRef
	require.Equal(t, "dex-credentials", ref.Name)
	require.Equal(t, "muster-system", ref.Namespace)
	require.Equal(t, "client-id", ref.ClientIDKey)
	require.Equal(t, "client-secret", ref.ClientSecretKey)
}

func TestTokenExchangeFromAPI_NoSecretRef(t *testing.T) {
	t.Parallel()

	tx := &api.TokenExchangeConfig{
		Enabled:          true,
		DexTokenEndpoint: "https://dex.example.com/token",
	}

	out := translate.TokenExchangeFromAPI(tx)

	require.True(t, out.Enabled)
	require.Equal(t, "https://dex.example.com/token", out.DexTokenEndpoint)
	require.Nil(t, out.ClientCredentialsSecretRef)
}

func TestInfoToMCPServerSpec_TableDriven(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		info *api.MCPServerInfo
	}{
		{
			name: "stdio minimal",
			info: &api.MCPServerInfo{Name: "a", Type: "stdio", Command: "/bin/a"},
		},
		{
			name: "remote streamable-http",
			info: &api.MCPServerInfo{Name: "b", Type: "streamable-http", URL: "https://b.example.com/mcp"},
		},
		{
			name: "sse with headers",
			info: &api.MCPServerInfo{
				Name:    "c",
				Type:    "sse",
				URL:     "https://c.example.com/sse",
				Headers: map[string]string{"X-API-Key": "secret"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := translate.InfoToMCPServerSpec(tc.info)
			require.Equal(t, tc.info.Type, spec.Type)
			require.Equal(t, tc.info.URL, spec.URL)
			require.Equal(t, tc.info.Command, spec.Command)
			require.Equal(t, tc.info.Headers, spec.Headers)
		})
	}
}
