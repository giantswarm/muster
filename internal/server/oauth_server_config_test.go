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

func TestBuildOAuthServerOptions_AllowedClaimsPropagated(t *testing.T) {
	t.Parallel()

	base := config.OAuthServerConfig{BaseURL: "https://muster.example.com"}
	baseOpts, err := buildOAuthServerOptions(base, nil)
	require.NoError(t, err)

	withClaims := config.OAuthServerConfig{
		BaseURL: "https://muster.example.com",
		TrustedIssuers: []config.TrustedIssuerConfig{
			{
				Issuer:        "https://idp.example.com",
				JwksURL:       "https://idp.example.com/jwks",
				AllowedClaims: map[string]string{"sub": "system:serviceaccount:ai-platform:*"},
			},
		},
	}
	claimsOpts, err := buildOAuthServerOptions(withClaims, nil)
	require.NoError(t, err)
	require.Greater(t, len(claimsOpts), len(baseOpts), "TrustedIssuers with AllowedClaims should add a server option")
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

func TestK8sSASubPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		namespaces  []string
		sas         []string
		wantPattern string
		wantErr     string
	}{
		{
			name:        "both empty → no restriction",
			wantPattern: "",
		},
		{
			name:        "single namespace → wildcard SA pattern",
			namespaces:  []string{"team-a"},
			wantPattern: "system:serviceaccount:team-a:*",
		},
		{
			name:        "single SA in ns/name form → exact pattern",
			sas:         []string{"team-a/agent"},
			wantPattern: "system:serviceaccount:team-a:agent",
		},
		{
			name:        "single SA with matching namespace → exact pattern",
			namespaces:  []string{"team-a"},
			sas:         []string{"team-a/agent"},
			wantPattern: "system:serviceaccount:team-a:agent",
		},
		{
			name:       "multi namespaces → error",
			namespaces: []string{"team-a", "team-b"},
			wantErr:    "allowedNamespaces supports at most one entry",
		},
		{
			name:    "multi SAs → error",
			sas:     []string{"team-a/agent", "team-b/agent"},
			wantErr: "allowedServiceAccounts supports at most one entry",
		},
		{
			name:    "SA without namespace separator → error",
			sas:     []string{"just-a-name"},
			wantErr: "must be in namespace/name format",
		},
		{
			name:    "SA with empty namespace → error",
			sas:     []string{"/agent"},
			wantErr: "must be in namespace/name format",
		},
		{
			name:    "SA with empty name → error",
			sas:     []string{"team-a/"},
			wantErr: "must be in namespace/name format",
		},
		{
			name:       "SA namespace conflicts with allowedNamespaces → error",
			namespaces: []string{"team-a"},
			sas:        []string{"team-b/agent"},
			wantErr:    "conflicts with allowedNamespaces",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := k8sSASubPattern(tc.namespaces, tc.sas)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantPattern, got)
		})
	}
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
