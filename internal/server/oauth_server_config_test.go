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

func TestNewOAuthServerConfig_TokenExchangeAllowedResources(t *testing.T) {
	t.Parallel()

	t.Run("local-mint targets extend the allowlist alongside the resource identifier", func(t *testing.T) {
		t.Parallel()
		cfg := config.OAuthServerConfig{
			BaseURL:            "https://muster.example.com",
			ResourceIdentifier: "https://muster.example.com/mcp",
			TokenExchangeBroker: config.TokenExchangeBrokerConfig{
				Targets: map[string]config.BrokerTargetConfig{
					"cluster-b": {Type: config.TargetTypeLocalMint},
					"cluster-c": {Type: config.TargetTypeLocalMint},
					// An oidc-exchange target routes through the host Exchanger, not
					// the self-issued path, so it must not appear in the allowlist.
					"cluster-oidc": {DexTokenEndpoint: "https://dex.example.com/token", ConnectorID: "main"},
				},
			},
		}
		got := newOAuthServerConfig(cfg, time.Hour)
		require.ElementsMatch(t, []string{"https://muster.example.com/mcp", "cluster-b", "cluster-c"}, got.TokenExchangeAllowedResources)
	})

	// The self-issued /oauth/token grant is inherent to JWT mode and cannot be
	// toggled off, and an empty TokenExchangeAllowedResources disables the check
	// (any resource accepted). The allowlist must therefore never be empty, even
	// when no local-mint target is configured, so the credential-less endpoint
	// stays constrained to muster's own resource identifier.
	t.Run("no local-mint targets still constrains to the resource identifier", func(t *testing.T) {
		t.Parallel()
		cfg := config.OAuthServerConfig{
			BaseURL:            "https://muster.example.com",
			ResourceIdentifier: "https://muster.example.com/mcp",
		}
		got := newOAuthServerConfig(cfg, time.Hour)
		require.Equal(t, []string{"https://muster.example.com/mcp"}, got.TokenExchangeAllowedResources)
	})

	t.Run("only oidc-exchange targets still constrains to the resource identifier", func(t *testing.T) {
		t.Parallel()
		cfg := config.OAuthServerConfig{
			BaseURL:            "https://muster.example.com",
			ResourceIdentifier: "https://muster.example.com/mcp",
			TokenExchangeBroker: config.TokenExchangeBrokerConfig{
				Targets: map[string]config.BrokerTargetConfig{
					"cluster-oidc": {DexTokenEndpoint: "https://dex.example.com/token", ConnectorID: "main"},
				},
			},
		}
		got := newOAuthServerConfig(cfg, time.Hour)
		require.Equal(t, []string{"https://muster.example.com/mcp"}, got.TokenExchangeAllowedResources)
	})

	t.Run("falls back to the issuer when no explicit resource identifier is set", func(t *testing.T) {
		t.Parallel()
		cfg := config.OAuthServerConfig{
			BaseURL: "https://muster.example.com",
		}
		got := newOAuthServerConfig(cfg, time.Hour)
		require.Equal(t, []string{"https://muster.example.com"}, got.TokenExchangeAllowedResources)
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
	opts, err := buildOAuthServerOptions(cfg, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, opts)
}

func TestBuildOAuthServerOptions_AllowPrivateIPJWKSNoError(t *testing.T) {
	t.Parallel()

	cfg := config.OAuthServerConfig{
		BaseURL: "https://muster.example.com",
		TrustedIssuers: []config.TrustedIssuerConfig{
			{
				Issuer:             "https://kubernetes.default.svc",
				JwksURL:            "https://kubernetes.default.svc/openid/v1/jwks",
				AllowPrivateIPJWKS: true,
			},
		},
	}
	opts, err := buildOAuthServerOptions(cfg, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, opts)
}

func TestBuildOAuthServerOptions_NoErrorWhenFieldsAbsent(t *testing.T) {
	t.Parallel()

	cfg := config.OAuthServerConfig{
		BaseURL: "https://muster.example.com",
	}
	opts, err := buildOAuthServerOptions(cfg, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, opts)
}

func TestToTrustedIssuer_MapsAllFields(t *testing.T) {
	t.Parallel()

	in := config.TrustedIssuerConfig{
		Issuer:                  "https://idp.example.com",
		JwksURL:                 "https://idp.example.com/jwks",
		AllowedAudiences:        []string{"aud1", "aud2"},
		AllowedScopes:           []string{"read", "write"},
		AllowedClaims:           map[string]string{"sub": "system:serviceaccount:ns:*"},
		SubjectClaim:            "email",
		AllowPrivateIPJWKS:      true,
		AllowPrivateIPJWKSHosts: []string{"dex.example.com"},
		AcceptedTypHeaders:      []string{""},
	}
	got := toTrustedIssuer(in, nil)
	require.Equal(t, in.Issuer, got.Issuer)
	require.Equal(t, in.JwksURL, got.JwksURL)
	require.Equal(t, in.AllowedAudiences, got.AllowedAudiences)
	require.Equal(t, in.AllowedScopes, got.AllowedScopes)
	require.Equal(t, in.AllowedClaims, got.AllowedClaims)
	require.Equal(t, in.SubjectClaim, got.SubjectClaim)
	require.True(t, got.AllowPrivateIPJWKS)
	require.Equal(t, in.AllowPrivateIPJWKSHosts, got.AllowPrivateIPJWKSHosts)
	require.Equal(t, in.AcceptedTypHeaders, got.AcceptedTypHeaders)
}

func TestNewOAuthServerConfig_MapsTokenExchangeClientAudiences(t *testing.T) {
	t.Parallel()

	allowlist := map[string][]string{
		"portal-backend": {"cluster-a", "cluster-b"},
	}
	cfg := config.OAuthServerConfig{
		BaseURL: "https://muster.example.com",
		TokenExchangeBroker: config.TokenExchangeBrokerConfig{
			ClientAudiences: allowlist,
		},
	}
	got := newOAuthServerConfig(cfg, time.Hour)
	require.Equal(t, allowlist, got.TokenExchangeClientAudiences)
}

func TestBuildOAuthServerOptions_BrokerRequiresTrustedIssuers(t *testing.T) {
	t.Parallel()

	cfg := config.OAuthServerConfig{
		BaseURL: "https://muster.example.com",
		TokenExchangeBroker: config.TokenExchangeBrokerConfig{
			Targets: map[string]config.BrokerTargetConfig{
				"cluster-a": {
					DexTokenEndpoint: "https://dex.cluster-a.example.com/token",
					ConnectorID:      "main-dex",
				},
			},
		},
	}
	_, err := buildOAuthServerOptions(cfg, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "trustedIssuers")

	cfg.TrustedIssuers = []config.TrustedIssuerConfig{
		{
			Issuer:           "https://dex.main.example.com",
			JwksURL:          "https://dex.main.example.com/keys",
			AllowedAudiences: []string{"portal-frontend"},
		},
	}
	opts, err := buildOAuthServerOptions(cfg, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, opts)
}

func TestBuildOAuthServerOptions_InvalidCIDRReturnsError(t *testing.T) {
	t.Parallel()

	cfg := config.OAuthServerConfig{
		BaseURL:           "https://muster.example.com",
		TrustedProxyCIDRs: []string{"not-a-cidr"},
	}
	_, err := buildOAuthServerOptions(cfg, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid CIDR")
}
