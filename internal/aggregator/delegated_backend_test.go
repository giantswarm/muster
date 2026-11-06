package aggregator

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
)

func delegatedTestToken(t *testing.T, iss string, exp time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"iss": iss, "sub": "person", "exp": exp.Unix(), "act": map[string]any{"sub": "actor/kagent/a"}})
	require.NoError(t, err)
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + "." + enc([]byte("sig"))
}

func TestDelegatedForwardRefusal(t *testing.T) {
	exchange := &api.TokenExchangeConfig{Enabled: true, DexTokenEndpoint: "https://dex.example.com/token", ConnectorID: "local-dex"}
	tests := []struct {
		name    string
		auth    *api.MCPServerAuth
		refused bool
	}{
		{name: "forwardToken", auth: &api.MCPServerAuth{ForwardToken: true}, refused: true},
		{name: "forwardToken with token exchange", auth: &api.MCPServerAuth{ForwardToken: true, TokenExchange: exchange}},
		{name: "token exchange", auth: &api.MCPServerAuth{TokenExchange: exchange}},
		{name: "forwardIdentity", auth: &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, ForwardIdentity: true, AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth"}}, refused: true},
		{name: "oauth grant", auth: &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth}},
		{name: "no auth", auth: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := delegatedForwardRefusal(&ServerInfo{Name: "backend", AuthConfig: tc.auth})
			if tc.refused {
				require.ErrorContains(t, err, "never is")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestWithDelegation(t *testing.T) {
	actorFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(actorFile, []byte("muster-workload-token\n"), 0o600))
	a := &AggregatorServer{config: AggregatorConfig{OAuthServer: OAuthServerConfig{Config: config.OAuthServerConfig{
		Delegation: config.DelegationConfig{ActorTokenFile: actorFile},
	}}}}
	caller := api.DelegatedCaller{Issuer: "https://dex.example.com", Token: delegatedTestToken(t, "https://dex.example.com", time.Now().Add(10*time.Minute))}
	server := func(audiences ...string) *ServerInfo {
		return &ServerInfo{Name: "k8s", AuthConfig: &api.MCPServerAuth{RequiredAudiences: audiences, TokenExchange: &api.TokenExchangeConfig{Enabled: true, DexTokenEndpoint: "https://dex.example.com/token"}}}
	}
	resolve := func(s *ServerInfo) api.ResolvedTokenExchangeConfig {
		r, err := s.AuthConfig.TokenExchange.WithResolvedRuntime("muster", "secret", s.AuthConfig.RequiredAudiences)
		require.NoError(t, err)
		return r
	}

	t.Run("exchange at the token's own issuer", func(t *testing.T) {
		s := server("mcp-kubernetes")
		got, err := a.withDelegation(caller, s, resolve(s))
		require.NoError(t, err)
		require.Equal(t, &api.TokenExchangeDelegation{ActorToken: "muster-workload-token", Audience: "mcp-kubernetes"}, got.Delegation)
	})

	t.Run("another issuer is refused", func(t *testing.T) {
		s := server()
		s.AuthConfig.TokenExchange.DexTokenEndpoint = "https://dex.other.example.com/token"
		_, err := a.withDelegation(caller, s, resolve(s))
		require.ErrorContains(t, err, "can only be exchanged at its own issuer")
	})

	t.Run("more than one audience is refused", func(t *testing.T) {
		s := server("a", "b")
		_, err := a.withDelegation(caller, s, resolve(s))
		require.ErrorContains(t, err, "names one")
	})

	t.Run("an expired token is refused", func(t *testing.T) {
		expired := caller
		expired.Token = delegatedTestToken(t, "https://dex.example.com", time.Now().Add(-time.Minute))
		s := server()
		_, err := a.withDelegation(expired, s, resolve(s))
		require.ErrorContains(t, err, "delegated token")
	})

	t.Run("no actor token configured", func(t *testing.T) {
		none := &AggregatorServer{config: AggregatorConfig{OAuthServer: OAuthServerConfig{Config: config.OAuthServerConfig{}}}}
		s := server()
		_, err := none.withDelegation(caller, s, resolve(s))
		require.ErrorContains(t, err, "actorTokenFile is not set")
	})

	t.Run("cache key per session and server", func(t *testing.T) {
		require.NotEqual(t, delegatedCacheKey("ext-a", "k8s"), delegatedCacheKey("ext-b", "k8s"))
		require.NotEqual(t, delegatedCacheKey("ext-a", "k8s"), delegatedCacheKey("ext-a", "prom"))
	})
}

func TestShouldUseTokenExchange_ConnectorIDOptional(t *testing.T) {
	info := func(connector string) *ServerInfo {
		return &ServerInfo{AuthConfig: &api.MCPServerAuth{TokenExchange: &api.TokenExchangeConfig{Enabled: true, DexTokenEndpoint: "https://dex.example.com/token", ConnectorID: connector}}}
	}
	require.True(t, ShouldUseTokenExchange(info("")))
	require.True(t, ShouldUseTokenExchange(info("local-dex")))
	require.False(t, ShouldUseTokenExchange(&ServerInfo{AuthConfig: &api.MCPServerAuth{TokenExchange: &api.TokenExchangeConfig{Enabled: true}}}))
}

func TestDelegationExchangeConfig(t *testing.T) {
	actorFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(actorFile, []byte("muster-workload-token"), 0o600))
	a := &AggregatorServer{config: AggregatorConfig{OAuthServer: OAuthServerConfig{Config: config.OAuthServerConfig{
		Dex:        config.DexConfig{IssuerURL: "https://dex.example.com/", ClientID: "muster", ClientSecret: "secret"},
		Delegation: config.DelegationConfig{ActorTokenFile: actorFile},
	}}}}

	got, err := a.DelegationExchangeConfig(api.DelegatedCaller{Issuer: "https://dex.example.com"})
	require.NoError(t, err)
	require.Equal(t, api.TokenExchangeConfig{
		Enabled:          true,
		DexTokenEndpoint: "https://dex.example.com/token",
		ExpectedIssuer:   "https://dex.example.com",
		ClientID:         "muster",
		ClientSecret:     "secret",
		Delegation:       &api.TokenExchangeDelegation{ActorToken: "muster-workload-token"},
	}, got)

	_, err = a.DelegationExchangeConfig(api.DelegatedCaller{Issuer: "https://other.example.com"})
	require.ErrorContains(t, err, "not from muster's Dex")
}
