package server

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

func TestClientManagementConfiguration(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		configuration := config.OAuthServerConfig{EnableClientManagementEndpoint: enabled}
		require.Equal(t, enabled, newOAuthServerConfig(configuration, oauthTokenLifetimes{}).EnableClientManagementEndpoint)
	}
}
