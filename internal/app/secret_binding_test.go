package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/types"

	"github.com/giantswarm/muster/v5/internal/config"
)

// TestProtectedSecrets: only muster's own Secrets
// (secretBinding.protectedSecrets) are protected, in muster's namespace. The
// Secrets muster's configuration names are not: an MCPServer may share them
// once they are bound to it.
func TestProtectedSecrets(t *testing.T) {
	cfg := &Config{MusterConfig: &config.MusterConfig{
		SecretBinding: config.SecretBindingConfig{ProtectedSecrets: []string{"muster-oauth", "valkey", ""}},
		Aggregator: config.AggregatorConfig{
			ProviderApps: []config.ProviderAppConfig{
				{Name: "github", ClientCredentialsSecretRef: &config.BrokerSecretRefConfig{Name: "github-oauth-client"}},
			},
			OAuth: config.OAuthConfig{Server: config.OAuthServerConfig{TokenExchangeBroker: config.TokenExchangeBrokerConfig{
				Targets: map[string]config.BrokerTargetConfig{
					"gazelle": {ClientCredentialsSecretRef: &config.BrokerSecretRefConfig{Name: "gazelle-token-exchange-credentials"}},
				},
			}}},
		},
	}}

	assert.Equal(t, []types.NamespacedName{
		{Namespace: "muster", Name: "muster-oauth"},
		{Namespace: "muster", Name: "valkey"},
	}, protectedSecrets(cfg, "muster"))
}
