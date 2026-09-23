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

// TestForwardIdentityCELEnvtest applies MCPServers to a real kube-apiserver
// with the generated CRD: the forwardIdentity CEL rule admits the field only
// on an oauth server with an authorizationServer pin. api.ValidateForwardIdentity
// is the same rule for filesystem mode (TestValidateForwardIdentity).
//
// Requires envtest binaries: run via `make test-envtest`, which resolves
// KUBEBUILDER_ASSETS with setup-envtest. Skipped otherwise.
func TestForwardIdentityCELEnvtest(t *testing.T) {
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

	pin := &musterv1alpha1.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth"}
	const rejection = "forwardIdentity sends the session's ID token next to a pinned grant"

	tests := []struct {
		name     string
		auth     *musterv1alpha1.MCPServerAuth
		rejected string
	}{
		{
			name: "pinned-oauth",
			auth: &musterv1alpha1.MCPServerAuth{Type: "oauth", AuthorizationServer: pin, ForwardIdentity: true},
		},
		{
			name: "pinned-without-the-field",
			auth: &musterv1alpha1.MCPServerAuth{Type: "oauth", AuthorizationServer: pin},
		},
		{
			name:     "oauth-without-a-pin",
			auth:     &musterv1alpha1.MCPServerAuth{Type: "oauth", ForwardIdentity: true},
			rejected: rejection,
		},
		{
			name:     "type-defaulted-to-none",
			auth:     &musterv1alpha1.MCPServerAuth{ForwardIdentity: true},
			rejected: rejection,
		},
		{
			// forwardToken's own rule still keeps it apart from a pin: the two
			// fields are not interchangeable.
			name:     "forward-token-next-to-a-pin",
			auth:     &musterv1alpha1.MCPServerAuth{Type: "oauth", AuthorizationServer: pin, ForwardToken: true, ForwardIdentity: true},
			rejected: "forwardToken bypasses per-backend OAuth",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &musterv1alpha1.MCPServer{
				ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: "default"},
				Spec: musterv1alpha1.MCPServerSpec{
					Type: "streamable-http",
					URL:  "https://two-hats.example.com/mcp",
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
