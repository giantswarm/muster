package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

func TestNewOAuthServerConfig_TrustedPublicRegistrationRedirectURIs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "nil passes through as nil",
			in:   nil,
			want: nil,
		},
		{
			name: "empty passes through as empty",
			in:   []string{},
			want: []string{},
		},
		{
			name: "single URI preserved",
			in:   []string{"https://claude.ai/api/mcp/auth_callback"},
			want: []string{"https://claude.ai/api/mcp/auth_callback"},
		},
		{
			name: "multiple URIs preserved in order",
			in: []string{
				"https://claude.ai/api/mcp/auth_callback",
				"https://example.com/cb",
			},
			want: []string{
				"https://claude.ai/api/mcp/auth_callback",
				"https://example.com/cb",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.OAuthServerConfig{
				BaseURL:                               "https://muster.example.com",
				TrustedPublicRegistrationRedirectURIs: tc.in,
			}

			got := newOAuthServerConfig(cfg, oauthTokenLifetimes{refreshTokenTTL: time.Hour})

			require.Equal(t, tc.want, got.TrustedPublicRegistrationRedirectURIs)
		})
	}
}

func TestNewOAuthServerConfig_PreservesAdjacentFields(t *testing.T) {
	t.Parallel()

	cfg := config.OAuthServerConfig{
		BaseURL:                               "https://muster.example.com",
		AllowPublicClientRegistration:         false,
		RegistrationToken:                     "tok",
		EnableCIMD:                            true,
		AllowLocalhostRedirectURIs:            true,
		AllowPrivateIPClientMetadata:          true,
		AllowPrivateIPRedirectURIs:            true,
		TrustedPublicRegistrationSchemes:      []string{"cursor", "vscode"},
		TrustedPublicRegistrationRedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"},
		TrustedAudiences:                      []string{"upstream-client-id"},
	}

	got := newOAuthServerConfig(cfg, oauthTokenLifetimes{refreshTokenTTL: time.Hour})

	require.Equal(t, "https://muster.example.com", got.Issuer)
	require.False(t, got.AllowPublicClientRegistration)
	require.Equal(t, "tok", got.RegistrationAccessToken)
	require.True(t, got.EnableClientIDMetadataDocuments)
	require.True(t, got.AllowLocalhostRedirectURIs)
	require.True(t, got.AllowPrivateIPClientMetadata)
	require.True(t, got.AllowPrivateIPRedirectURIs)
	require.Equal(t, []string{"cursor", "vscode"}, got.TrustedPublicRegistrationSchemes)
	require.Equal(t, []string{"https://claude.ai/api/mcp/auth_callback"}, got.TrustedPublicRegistrationRedirectURIs)
	require.Equal(t, []string{"upstream-client-id"}, got.TrustedAudiences)
	require.Equal(t, DefaultMaxClientsPerIP, got.MaxClientsPerIP)
	require.Equal(t, int64(time.Hour/time.Second), got.RefreshTokenTTL)
}

func TestNewOAuthServerConfig_AllowedOriginsSplitAndTrimmed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty disables CORS", in: "", want: nil},
		{name: "single origin", in: "https://app.example.com", want: []string{"https://app.example.com"}},
		{name: "multiple origins split on comma", in: "https://a.example.com,https://b.example.com", want: []string{"https://a.example.com", "https://b.example.com"}},
		{name: "spaces around commas trimmed", in: "https://a.example.com, https://b.example.com", want: []string{"https://a.example.com", "https://b.example.com"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.OAuthServerConfig{
				BaseURL:        "https://muster.example.com",
				AllowedOrigins: tc.in,
			}

			got := newOAuthServerConfig(cfg, oauthTokenLifetimes{refreshTokenTTL: time.Hour})

			require.Equal(t, tc.want, got.CORS.AllowedOrigins)
		})
	}
}

func TestNewOAuthServerConfig_AllowPrivateIPJWKSMirrorsDexFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   bool
		want bool
	}{
		{name: "private-IP Dex enables forwarded-token JWKS private IPs", in: true, want: true},
		{name: "public Dex leaves forwarded-token JWKS private IPs disabled", in: false, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.OAuthServerConfig{
				BaseURL: "https://muster.example.com",
				Dex:     config.DexConfig{AllowPrivateIPOIDC: tc.in},
			}

			got := newOAuthServerConfig(cfg, oauthTokenLifetimes{refreshTokenTTL: time.Hour})

			require.Equal(t, tc.want, got.AllowPrivateIPJWKS,
				"AllowPrivateIPJWKS must mirror Dex.AllowPrivateIPOIDC")
		})
	}
}

func TestNewOAuthServerConfig_AllowPrivateIPClientMetadataDefaultsOff(t *testing.T) {
	t.Parallel()

	got := newOAuthServerConfig(config.OAuthServerConfig{BaseURL: "https://muster.example.com"}, oauthTokenLifetimes{refreshTokenTTL: time.Hour})

	require.False(t, got.AllowPrivateIPClientMetadata, "the CIMD SSRF guard must stay on unless the operator opts out")
	require.False(t, got.AllowPrivateIPRedirectURIs, "the redirect-URI private-IP guard must stay on unless the operator opts out")
}

func TestParseOAuthTokenLifetimes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cfg     config.OAuthServerConfig
		want    oauthTokenLifetimes
		wantErr string
	}{
		{
			name: "empty keeps the defaults",
			want: oauthTokenLifetimes{refreshTokenTTL: DefaultRefreshTokenTTL},
		},
		{
			name: "both set",
			cfg:  config.OAuthServerConfig{SessionDuration: "168h", ProviderTokenRefreshThreshold: "25m"},
			want: oauthTokenLifetimes{refreshTokenTTL: 168 * time.Hour, providerTokenRefreshThreshold: 25 * time.Minute},
		},
		{
			name:    "invalid session duration",
			cfg:     config.OAuthServerConfig{SessionDuration: "30d"},
			wantErr: "invalid sessionDuration",
		},
		{
			name:    "invalid threshold",
			cfg:     config.OAuthServerConfig{ProviderTokenRefreshThreshold: "soon"},
			wantErr: "invalid providerTokenRefreshThreshold",
		},
		{
			name:    "negative threshold",
			cfg:     config.OAuthServerConfig{ProviderTokenRefreshThreshold: "-5m"},
			wantErr: "must be at least 1s",
		},
		{
			name:    "sub-second threshold would silently mean the default",
			cfg:     config.OAuthServerConfig{ProviderTokenRefreshThreshold: "500ms"},
			wantErr: "must be at least 1s",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseOAuthTokenLifetimes(tc.cfg)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestNewOAuthServerConfig_ProviderTokenRefreshThreshold(t *testing.T) {
	t.Parallel()

	cfg := config.OAuthServerConfig{BaseURL: "https://muster.example.com"}

	t.Run("unset leaves the mcp-oauth default", func(t *testing.T) {
		t.Parallel()
		got := newOAuthServerConfig(cfg, oauthTokenLifetimes{refreshTokenTTL: time.Hour})
		require.Zero(t, got.TokenRefreshThreshold)
	})

	t.Run("set maps to seconds", func(t *testing.T) {
		t.Parallel()
		got := newOAuthServerConfig(cfg, oauthTokenLifetimes{
			refreshTokenTTL:               time.Hour,
			providerTokenRefreshThreshold: 25 * time.Minute,
		})
		require.Equal(t, int64(1500), got.TokenRefreshThreshold)
		require.Equal(t, int64(3600), got.RefreshTokenTTL)
	})
}
