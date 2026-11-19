package server

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/giantswarm/muster/v5/internal/config"
)

func newPinnedCIMDServer(t *testing.T, cfg config.OAuthServerConfig) *oauthServerComponents {
	t.Helper()
	cfg.BaseURL = "https://muster.example.com"
	cfg.Provider = OAuthProviderGoogle
	cfg.Google = config.GoogleConfig{ClientID: "muster", ClientSecret: "secret"}
	cfg.EnableCIMD = true
	components, err := createOAuthServer(cfg, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = components.server.Shutdown(context.Background()) })
	return components
}

// pinnedGateway's host does not resolve, so accepting it proves the pin was
// served without a fetch.
var pinnedGateway = config.ClientMetadataDocument{
	ClientID:                "https://agentgateway.pinned.invalid/auth/slack/client.json",
	ClientName:              "klaus-gateway",
	RedirectURIs:            []string{"https://agentgateway.pinned.invalid/auth/slack/callback"},
	GrantTypes:              []string{"authorization_code", "refresh_token"},
	ResponseTypes:           []string{"code"},
	TokenEndpointAuthMethod: "none",
}

func TestCreateOAuthServer_PinnedClientMetadata(t *testing.T) {
	t.Run("a pinned client resolves without a fetch", func(t *testing.T) {
		components := newPinnedCIMDServer(t, config.OAuthServerConfig{
			PreRegisteredClientMetadata: []config.ClientMetadataDocument{pinnedGateway},
		})

		_, err := components.server.ValidateRedirectURIForAuthorization(t.Context(), pinnedGateway.ClientID, pinnedGateway.RedirectURIs[0])
		require.NoError(t, err)
	})
	t.Run("a pinned client keeps its redirect URIs", func(t *testing.T) {
		components := newPinnedCIMDServer(t, config.OAuthServerConfig{
			PreRegisteredClientMetadata: []config.ClientMetadataDocument{pinnedGateway},
		})

		_, err := components.server.ValidateRedirectURIForAuthorization(t.Context(), pinnedGateway.ClientID, "https://attacker.example/callback")
		require.Error(t, err)
	})
	t.Run("restrict mode refuses an unpinned, unlisted client before any lookup", func(t *testing.T) {
		components := newPinnedCIMDServer(t, config.OAuthServerConfig{
			PreRegisteredClientMetadata: []config.ClientMetadataDocument{pinnedGateway},
			RestrictClientMetadata:      true,
		})

		_, err := components.server.ValidateRedirectURIForAuthorization(t.Context(), "https://other.pinned.invalid/client.json", "https://other.pinned.invalid/callback")
		require.ErrorContains(t, err, "invalid_client")
		require.NotContains(t, err.Error(), "DNS")
	})
	t.Run("restrict mode with no pin and no host is refused at startup", func(t *testing.T) {
		_, err := createOAuthServer(config.OAuthServerConfig{
			BaseURL:                "https://muster.example.com",
			Provider:               OAuthProviderGoogle,
			Google:                 config.GoogleConfig{ClientID: "muster", ClientSecret: "secret"},
			EnableCIMD:             true,
			RestrictClientMetadata: true,
		}, nil)
		require.Error(t, err)
	})
}

// TestChartPublicClientMetadataIsAccepted loads the public clients' documents
// the chart pins and hands them to mcp-oauth, which refuses an invalid pin at
// startup.
func TestChartPublicClientMetadataIsAccepted(t *testing.T) {
	raw, err := os.ReadFile("../../helm/muster/values.yaml")
	require.NoError(t, err)
	var values struct {
		Muster struct {
			OAuth struct {
				Server struct {
					PublicClientMetadata []config.ClientMetadataDocument `yaml:"publicClientMetadata"`
				} `yaml:"server"`
			} `yaml:"oauth"`
		} `yaml:"muster"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &values))
	documents := values.Muster.OAuth.Server.PublicClientMetadata
	require.Len(t, documents, 4)

	components := newPinnedCIMDServer(t, config.OAuthServerConfig{PreRegisteredClientMetadata: documents})

	for _, d := range documents {
		_, err := components.server.ValidateRedirectURIForAuthorization(t.Context(), d.ClientID, d.RedirectURIs[len(d.RedirectURIs)-1])
		require.NoError(t, err, d.ClientID)
	}
}
