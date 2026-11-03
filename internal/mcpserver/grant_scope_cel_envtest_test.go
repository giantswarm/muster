package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	kubernetesclient "github.com/giantswarm/muster/v5/internal/client/kubernetes"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// TestGrantScopeCELEnvtest applies MCPServers to a real kube-apiserver with
// the generated CRD: spec.auth.grantScope is admitted on an OAuth server that
// discovers its authorization server (no authorizationServer block) and next
// to a pin that agrees with it, and refused without type oauth, next to
// forwardToken or tokenExchange, and against a pin that says otherwise.
// api.ValidateGrantScope is the same rule for filesystem mode
// (TestValidateGrantScope).
//
// Requires envtest binaries: run via `make test-envtest`, which resolves
// KUBEBUILDER_ASSETS with setup-envtest. Skipped otherwise.
func TestGrantScopeCELEnvtest(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run via `make test-envtest`")
	}

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "helm", "muster", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err, "start envtest")
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})
	k8s, err := kubernetesclient.New(cfg)
	require.NoError(t, err)

	pin := func(grantScope string) *musterv1alpha1.MCPServerAuthAuthorizationServer {
		return &musterv1alpha1.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth", GrantScope: grantScope}
	}
	const needsOAuth = "grantScope decides whom the grant of a per-server OAuth sign-in belongs to"
	const disagree = "grantScope and authorizationServer.grantScope disagree"

	tests := []struct {
		name     string
		auth     *musterv1alpha1.MCPServerAuth
		rejected string
	}{
		{
			name: "discovered-subject",
			auth: &musterv1alpha1.MCPServerAuth{Type: "oauth", GrantScope: "subject"},
		},
		{
			name: "discovered-session",
			auth: &musterv1alpha1.MCPServerAuth{Type: "oauth", GrantScope: "session"},
		},
		{
			name: "subject-next-to-a-plain-pin",
			auth: &musterv1alpha1.MCPServerAuth{Type: "oauth", GrantScope: "subject", AuthorizationServer: pin("")},
		},
		{
			name: "subject-next-to-an-agreeing-pin",
			auth: &musterv1alpha1.MCPServerAuth{Type: "oauth", GrantScope: "subject", AuthorizationServer: pin("subject")},
		},
		{
			name:     "session-against-a-subject-pin",
			auth:     &musterv1alpha1.MCPServerAuth{Type: "oauth", GrantScope: "session", AuthorizationServer: pin("subject")},
			rejected: disagree,
		},
		{
			name:     "type-defaulted-to-none",
			auth:     &musterv1alpha1.MCPServerAuth{GrantScope: "subject"},
			rejected: needsOAuth,
		},
		{
			name:     "next-to-forward-token",
			auth:     &musterv1alpha1.MCPServerAuth{Type: "oauth", GrantScope: "subject", ForwardToken: true},
			rejected: needsOAuth,
		},
		{
			name: "next-to-token-exchange",
			auth: &musterv1alpha1.MCPServerAuth{Type: "oauth", GrantScope: "subject", TokenExchange: &musterv1alpha1.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex.example.com/token",
				ConnectorID:      "local",
			}},
			rejected: needsOAuth,
		},
		{
			name:     "unknown-value",
			auth:     &musterv1alpha1.MCPServerAuth{Type: "oauth", GrantScope: "person"},
			rejected: "supported values",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &musterv1alpha1.MCPServer{
				ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: "default"},
				Spec: musterv1alpha1.MCPServerSpec{
					Type: "streamable-http",
					URL:  "https://connector.example.com/mcp",
					Auth: tt.auth,
				},
			}
			err := k8s.Create(context.Background(), server)
			if tt.rejected == "" {
				require.NoError(t, err)
				return
			}
			require.True(t, apierrors.IsInvalid(err), "want an admission rejection, got %v", err)
			require.ErrorContains(t, err, tt.rejected)
		})
	}
}
