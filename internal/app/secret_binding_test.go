package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	mcpserverPkg "github.com/giantswarm/muster/v5/internal/mcpserver"
)

// TestProtectedSecrets: only muster's own Secrets
// (secretBinding.protectedSecrets) are protected, in muster's namespace. The
// Secrets muster's configuration names are not: an MCPServer may share them.
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

// TestCredentialsOptions_SecretReferencePolicy: in Kubernetes mode an
// MCPServer's Secret is read only while the configured admission policy is in
// force, and never when none is configured; in filesystem mode there is no
// admission and no such check.
func TestCredentialsOptions_SecretReferencePolicy(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-token", Namespace: "muster"},
		Data:       map[string][]byte{"token": []byte("t0ken")},
	}).Build()
	read := func(kubernetesMode bool) error {
		cfg := &Config{MusterConfig: &config.MusterConfig{}}
		adapter := mcpserverPkg.NewCredentialsAdapter(k8s, credentialsOptions(cfg, "muster", kubernetesMode)...)
		_, err := adapter.LoadMCPServerSecretKey(t.Context(), api.SecretOwner{Name: "github", Namespace: "muster"},
			api.SecretFieldHeadersFrom, &api.ClientCredentialsSecretRef{Name: "github-token"}, "token")
		return err
	}

	require.ErrorIs(t, read(true), api.ErrSecretReferencePolicyInactive)
	require.NoError(t, read(false))
}
