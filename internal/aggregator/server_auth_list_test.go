package aggregator

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

func TestListServersRequiringAuth(t *testing.T) {
	t.Run("returns non-SSO auth-required servers", func(t *testing.T) {
		reg := NewServerRegistry("x")
		err := reg.RegisterPendingAuth(
			"plain-oauth",
			"https://plain.example.com",
			"plain",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		)
		require.NoError(t, err)

		agg := &AggregatorServer{registry: reg}

		result := agg.ListServersRequiringAuth(context.Background())
		require.Len(t, result, 1)
		assert.Equal(t, "plain-oauth", result[0].Name)
		assert.Equal(t, "auth_required", result[0].Status)
		assert.Equal(t, "core_auth_login", result[0].AuthTool)
	})

	t.Run("skips token-forwarding SSO server", func(t *testing.T) {
		reg := NewServerRegistry("x")
		err := reg.RegisterPendingAuthWithConfig(
			"sso-fwd",
			"https://sso-fwd.example.com",
			"ssofwd",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{ForwardToken: true},
		)
		require.NoError(t, err)

		agg := &AggregatorServer{registry: reg}

		result := agg.ListServersRequiringAuth(context.Background())
		assert.Empty(t, result)
	})

	t.Run("skips token-exchange SSO server", func(t *testing.T) {
		reg := NewServerRegistry("x")
		err := reg.RegisterPendingAuthWithConfig(
			"sso-exchange",
			"https://sso-exchange.example.com",
			"ssoex",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{
				TokenExchange: &api.TokenExchangeConfig{
					Enabled:          true,
					DexTokenEndpoint: "https://dex.remote.example.com/token",
					ConnectorID:      "local-oidc",
				},
			},
		)
		require.NoError(t, err)

		agg := &AggregatorServer{registry: reg}

		result := agg.ListServersRequiringAuth(context.Background())
		assert.Empty(t, result)
	})

	t.Run("skips SSO server from auth required list", func(t *testing.T) {
		reg := NewServerRegistry("x")
		err := reg.RegisterPendingAuthWithConfig(
			"sso-pending",
			"https://sso-pending.example.com",
			"ssopending",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{ForwardToken: true},
		)
		require.NoError(t, err)

		agg := &AggregatorServer{registry: reg, ssoTracker: newSSOTracker()}

		result := agg.ListServersRequiringAuth(context.Background())
		assert.Empty(t, result, "manual login cannot perform SSO")
	})

	t.Run("always skips SSO server regardless of failure state", func(t *testing.T) {
		reg := NewServerRegistry("x")
		err := reg.RegisterPendingAuthWithConfig(
			"sso-failed",
			"https://sso-failed.example.com",
			"ssofailed",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{ForwardToken: true},
		)
		require.NoError(t, err)

		tracker := newSSOTracker()
		tracker.MarkSSOFailed("user@example.com", "sso-failed")

		agg := &AggregatorServer{registry: reg, ssoTracker: tracker}

		result := agg.ListServersRequiringAuth(context.Background())
		assert.Empty(t, result, "manual login cannot fix SSO failures")
	})

	t.Run("skips connected servers", func(t *testing.T) {
		reg := NewServerRegistry("x")
		client := &mockMCPClient{
			tools: []mcp.Tool{{Name: "t1"}},
		}

		ctx := context.Background()
		err := reg.Register(ctx, "connected-server", client, "conn")
		require.NoError(t, err)

		agg := &AggregatorServer{registry: reg}

		result := agg.ListServersRequiringAuth(ctx)
		assert.Empty(t, result)
	})

	t.Run("skips servers already in capability cache", func(t *testing.T) {
		reg := NewServerRegistry("x")
		err := reg.RegisterPendingAuth(
			"cached-server",
			"https://cached.example.com",
			"cached",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		)
		require.NoError(t, err)

		store := NewInMemoryCapabilityStore(30 * time.Minute)
		defer store.Stop()
		_ = store.Set(context.Background(), "test-session", "cached-server",
			&Capabilities{Tools: []mcp.Tool{{Name: "t"}}})

		ctx := api.WithSessionID(context.Background(), "test-session")
		agg := &AggregatorServer{
			registry:        reg,
			capabilityStore: store,
		}

		result := agg.ListServersRequiringAuth(ctx)
		assert.Empty(t, result)
	})

	t.Run("mixed SSO and non-SSO servers", func(t *testing.T) {
		reg := NewServerRegistry("x")

		require.NoError(t, reg.RegisterPendingAuth(
			"manual-oauth-1",
			"https://manual1.example.com",
			"m1",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		))

		require.NoError(t, reg.RegisterPendingAuthWithConfig(
			"sso-fwd-server",
			"https://sso-fwd.example.com",
			"ssofwd",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{ForwardToken: true},
		))

		require.NoError(t, reg.RegisterPendingAuthWithConfig(
			"sso-exchange-server",
			"https://sso-ex.example.com",
			"ssoex",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{
				TokenExchange: &api.TokenExchangeConfig{
					Enabled:          true,
					DexTokenEndpoint: "https://dex.remote.example.com/token",
					ConnectorID:      "local-oidc",
				},
			},
		))

		require.NoError(t, reg.RegisterPendingAuth(
			"manual-oauth-2",
			"https://manual2.example.com",
			"m2",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		))

		agg := &AggregatorServer{registry: reg}

		result := agg.ListServersRequiringAuth(context.Background())
		require.Len(t, result, 2)

		sort.Slice(result, func(i, j int) bool {
			return result[i].Name < result[j].Name
		})
		assert.Equal(t, "manual-oauth-1", result[0].Name)
		assert.Equal(t, "core_auth_login", result[0].AuthTool)
		assert.Equal(t, "manual-oauth-2", result[1].Name)
		assert.Equal(t, "core_auth_login", result[1].AuthTool)
	})

	t.Run("no servers returns empty slice", func(t *testing.T) {
		reg := NewServerRegistry("x")
		agg := &AggregatorServer{registry: reg}

		result := agg.ListServersRequiringAuth(context.Background())
		assert.Empty(t, result)
	})

	t.Run("incomplete token exchange config treated as non-SSO", func(t *testing.T) {
		reg := NewServerRegistry("x")
		err := reg.RegisterPendingAuthWithConfig(
			"partial-exchange",
			"https://partial.example.com",
			"partial",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{
				TokenExchange: &api.TokenExchangeConfig{
					Enabled:          true,
					DexTokenEndpoint: "",
					ConnectorID:      "",
				},
			},
		)
		require.NoError(t, err)

		agg := &AggregatorServer{registry: reg}

		result := agg.ListServersRequiringAuth(context.Background())
		require.Len(t, result, 1, "incomplete token exchange config should not be treated as SSO")
		assert.Equal(t, "partial-exchange", result[0].Name)
		assert.Equal(t, "core_auth_login", result[0].AuthTool)
	})

	t.Run("disabled token exchange treated as non-SSO", func(t *testing.T) {
		reg := NewServerRegistry("x")
		err := reg.RegisterPendingAuthWithConfig(
			"disabled-exchange",
			"https://disabled.example.com",
			"disabled",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			&api.MCPServerAuth{
				TokenExchange: &api.TokenExchangeConfig{
					Enabled:          false,
					DexTokenEndpoint: "https://dex.remote.example.com/token",
					ConnectorID:      "local-oidc",
				},
			},
		)
		require.NoError(t, err)

		agg := &AggregatorServer{registry: reg}

		result := agg.ListServersRequiringAuth(context.Background())
		require.Len(t, result, 1, "disabled token exchange should not be treated as SSO")
		assert.Equal(t, "disabled-exchange", result[0].Name)
	})
}
