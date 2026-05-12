package brokerhttp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/config"
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

			got := newOAuthServerConfig(cfg, time.Hour)

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
		TrustedPublicRegistrationSchemes:      []string{"cursor", "vscode"},
		TrustedPublicRegistrationRedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"},
		TrustedAudiences:                      []string{"upstream-client-id"},
	}

	got := newOAuthServerConfig(cfg, time.Hour)

	require.Equal(t, "https://muster.example.com", got.Issuer)
	require.False(t, got.AllowPublicClientRegistration)
	require.Equal(t, "tok", got.RegistrationAccessToken)
	require.True(t, got.EnableClientIDMetadataDocuments)
	require.True(t, got.AllowLocalhostRedirectURIs)
	require.Equal(t, []string{"cursor", "vscode"}, got.TrustedPublicRegistrationSchemes)
	require.Equal(t, []string{"https://claude.ai/api/mcp/auth_callback"}, got.TrustedPublicRegistrationRedirectURIs)
	require.Equal(t, []string{"upstream-client-id"}, got.TrustedAudiences)
	require.Equal(t, DefaultMaxClientsPerIP, got.MaxClientsPerIP)
	require.Equal(t, int64(time.Hour/time.Second), got.RefreshTokenTTL)
}
