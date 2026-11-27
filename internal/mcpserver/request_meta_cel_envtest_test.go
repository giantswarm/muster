package mcpserver

import (
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

// TestRequestMetaCELEnvtest applies MCPServers to a real kube-apiserver with
// the generated CRD, so the requestMeta CEL rules are checked as the apiserver
// compiles and runs them. api.ValidateRequestMeta is the same set of rules for
// filesystem mode (TestValidateRequestMeta).
//
// Requires envtest binaries: run via `make test-envtest`, which resolves
// KUBEBUILDER_ASSETS with setup-envtest. Skipped otherwise.
func TestRequestMetaCELEnvtest(t *testing.T) {
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

	sigv4 := &musterv1alpha1.MCPServerAuth{Type: "sigv4", SigV4: &musterv1alpha1.MCPServerSigV4{Region: "eu-central-1"}}
	region := map[string]string{"AWS_REGION": "eu-central-1"}

	tests := []struct {
		name     string
		spec     musterv1alpha1.MCPServerSpec
		rejected string
	}{
		{name: "sigv4-with-region", spec: musterv1alpha1.MCPServerSpec{Auth: sigv4, RequestMeta: region}},
		{name: "sigv4-with-deprecated-meta", spec: musterv1alpha1.MCPServerSpec{Auth: sigv4, Meta: region}}, //nolint:staticcheck // SA1019: covers the deprecated name
		{
			name:     "sigv4-without-region",
			spec:     musterv1alpha1.MCPServerSpec{Auth: sigv4},
			rejected: "a sigv4 server needs requestMeta.AWS_REGION",
		},
		{
			name:     "sigv4-with-an-empty-region",
			spec:     musterv1alpha1.MCPServerSpec{Auth: sigv4, RequestMeta: map[string]string{"AWS_REGION": ""}},
			rejected: "a sigv4 server needs requestMeta.AWS_REGION",
		},
		{
			name:     "both-names",
			spec:     musterv1alpha1.MCPServerSpec{RequestMeta: region, Meta: region}, //nolint:staticcheck // SA1019: covers the deprecated name
			rejected: "set requestMeta only",
		},
		{name: "prefixed-key", spec: musterv1alpha1.MCPServerSpec{RequestMeta: map[string]string{"com.example/tenant-id": "a"}}},
		{
			name:     "malformed-key",
			spec:     musterv1alpha1.MCPServerSpec{RequestMeta: map[string]string{"region_": "a"}},
			rejected: "a key must follow the MCP _meta key format",
		},
		{
			name:     "reserved-prefix",
			spec:     musterv1alpha1.MCPServerSpec{RequestMeta: map[string]string{"io.modelcontextprotocol/clientInfo": "a"}},
			rejected: "reserved for MCP",
		},
		{
			name:     "reserved-key",
			spec:     musterv1alpha1.MCPServerSpec{RequestMeta: map[string]string{"traceparent": "a"}},
			rejected: "set by muster and OpenTelemetry",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := tt.spec
			spec.Type = "streamable-http"
			spec.URL = "https://aws-mcp.eu-central-1.api.aws/mcp"
			server := &musterv1alpha1.MCPServer{
				ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: "default"},
				Spec:       spec,
			}
			err := k8s.Create(t.Context(), server)
			if tt.rejected == "" {
				require.NoError(t, err)
				return
			}
			require.True(t, apierrors.IsInvalid(err), "want an admission rejection, got %v", err)
			require.ErrorContains(t, err, tt.rejected)
		})
	}

	t.Run("requestMeta-on-stdio", func(t *testing.T) {
		server := &musterv1alpha1.MCPServer{
			ObjectMeta: metav1.ObjectMeta{Name: "stdio-with-request-meta", Namespace: "default"},
			Spec:       musterv1alpha1.MCPServerSpec{Type: "stdio", Command: "echo", RequestMeta: region},
		}
		err := k8s.Create(t.Context(), server)
		require.True(t, apierrors.IsInvalid(err), "want an admission rejection, got %v", err)
		require.ErrorContains(t, err, "requestMeta field is only allowed")
	})
}
