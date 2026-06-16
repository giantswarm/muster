package server

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

			got := newOAuthServerConfig(cfg, time.Hour)

			require.Equal(t, tc.want, got.CORS.AllowedOrigins)
		})
	}
}

func TestNewOAuthServerConfig_WorkloadAudiences(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                        string
		workloadAudiences           map[string][]string
		wantEnableWorkload          bool
		wantWorkloadAudienceSubject string
		wantWorkloadAudienceAuds    []string
	}{
		{
			name:               "nil workloadAudiences disables workload exchange",
			workloadAudiences:  nil,
			wantEnableWorkload: false,
		},
		{
			name:               "empty workloadAudiences disables workload exchange",
			workloadAudiences:  map[string][]string{},
			wantEnableWorkload: false,
		},
		{
			name: "non-empty workloadAudiences enables workload exchange",
			workloadAudiences: map[string][]string{
				"system:serviceaccount:agent-ns:kagent": {"cluster-a"},
			},
			wantEnableWorkload:          true,
			wantWorkloadAudienceSubject: "system:serviceaccount:agent-ns:kagent",
			wantWorkloadAudienceAuds:    []string{"cluster-a"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.OAuthServerConfig{
				BaseURL: "https://muster.example.com",
				TokenExchangeBroker: config.TokenExchangeBrokerConfig{
					WorkloadAudiences: tc.workloadAudiences,
				},
			}

			got := newOAuthServerConfig(cfg, time.Hour)

			require.Equal(t, tc.wantEnableWorkload, got.EnableWorkloadTokenExchange)
			if tc.wantWorkloadAudienceSubject != "" {
				require.Equal(t, tc.wantWorkloadAudienceAuds, got.WorkloadAudiences[tc.wantWorkloadAudienceSubject])
			}
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

			got := newOAuthServerConfig(cfg, time.Hour)

			require.Equal(t, tc.want, got.AllowPrivateIPJWKS,
				"AllowPrivateIPJWKS must mirror Dex.AllowPrivateIPOIDC")
		})
	}
}
