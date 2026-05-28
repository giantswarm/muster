package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	oauthserver "github.com/giantswarm/mcp-oauth/server"

	"github.com/giantswarm/muster/internal/config"
)

func TestNewOAuthServerConfig_EnableJWTMode(t *testing.T) {
	t.Parallel()

	t.Run("JWT mode enabled", func(t *testing.T) {
		t.Parallel()
		cfg := config.OAuthServerConfig{
			BaseURL:       "https://muster.example.com",
			EnableJWTMode: true,
		}
		got := newOAuthServerConfig(cfg, time.Hour)
		require.Equal(t, oauthserver.AccessTokenFormatJWT, got.AccessTokenFormat)
	})

	t.Run("JWT mode disabled", func(t *testing.T) {
		t.Parallel()
		cfg := config.OAuthServerConfig{
			BaseURL:       "https://muster.example.com",
			EnableJWTMode: false,
		}
		got := newOAuthServerConfig(cfg, time.Hour)
		require.Empty(t, got.AccessTokenFormat)
	})
}

func TestParseCIDRs(t *testing.T) {
	t.Parallel()

	t.Run("valid CIDRs parse", func(t *testing.T) {
		t.Parallel()
		got, err := parseCIDRs([]string{"10.0.0.0/8", "192.168.1.0/24"})
		require.NoError(t, err)
		require.Len(t, got, 2)
	})

	t.Run("invalid CIDR returns error", func(t *testing.T) {
		t.Parallel()
		_, err := parseCIDRs([]string{"not-a-cidr"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid CIDR")
	})

	t.Run("empty slice returns empty", func(t *testing.T) {
		t.Parallel()
		got, err := parseCIDRs([]string{})
		require.NoError(t, err)
		require.Empty(t, got)
	})
}

func TestNewDPoPReplayCache_MemoryFallback(t *testing.T) {
	t.Parallel()

	// Memory storage → in-process cache, no network required.
	storageCfg := config.OAuthStorageConfig{
		Type: "memory",
	}
	cache, client, err := newDPoPReplayCache(storageCfg)
	require.NoError(t, err)
	require.NotNil(t, cache)
	require.Nil(t, client)
}

func TestBuildOAuthServerOptions_NoErrorWhenFieldsSet(t *testing.T) {
	t.Parallel()

	cfg := config.OAuthServerConfig{
		BaseURL: "https://muster.example.com",
		TrustedIssuers: []config.TrustedIssuerConfig{
			{
				Issuer:        "https://idp.example.com",
				JwksURL:       "https://idp.example.com/jwks",
				AllowedClaims: map[string]string{"sub": "system:serviceaccount:ai-platform:*"},
			},
		},
		TrustedProxyCIDRs: []string{"127.0.0.1/32"},
	}
	opts, err := buildOAuthServerOptions(cfg, nil)
	require.NoError(t, err)
	require.NotEmpty(t, opts)
}

func TestBuildOAuthServerOptions_NoErrorWhenFieldsAbsent(t *testing.T) {
	t.Parallel()

	cfg := config.OAuthServerConfig{
		BaseURL: "https://muster.example.com",
	}
	opts, err := buildOAuthServerOptions(cfg, nil)
	require.NoError(t, err)
	require.NotEmpty(t, opts)
}

func TestBuildOAuthServerOptions_InvalidCIDRReturnsError(t *testing.T) {
	t.Parallel()

	cfg := config.OAuthServerConfig{
		BaseURL:           "https://muster.example.com",
		TrustedProxyCIDRs: []string{"not-a-cidr"},
	}
	_, err := buildOAuthServerOptions(cfg, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid CIDR")
}
