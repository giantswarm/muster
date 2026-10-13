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

// TestMaxCallDurationCELEnvtest applies MCPServers to a real kube-apiserver
// with the generated CRD: maxCallDuration is admitted from timeout, or the
// timeout default when unset, up to 3600. api.ValidateMaxCallDuration is the
// same rule for filesystem mode (TestValidateMaxCallDuration).
//
// Requires envtest binaries: run via `make test-envtest`, which resolves
// KUBEBUILDER_ASSETS with setup-envtest. Skipped otherwise.
func TestMaxCallDurationCELEnvtest(t *testing.T) {
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

	const belowTimeout = "maxCallDuration must be at least timeout"

	tests := []struct {
		name            string
		timeout         int
		maxCallDuration int
		rejected        string
	}{
		{name: "unset", timeout: 120},
		{name: "above-timeout", timeout: 120, maxCallDuration: 900},
		{name: "equal-to-timeout", timeout: 120, maxCallDuration: 120},
		{name: "above-the-default-timeout", maxCallDuration: 30},
		{name: "below-timeout", timeout: 120, maxCallDuration: 60, rejected: belowTimeout},
		{name: "below-the-default-timeout", maxCallDuration: 10, rejected: belowTimeout},
		{name: "above-the-limit", timeout: 120, maxCallDuration: 3601, rejected: "should be less than or equal to 3600"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &musterv1alpha1.MCPServer{
				ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: "default"},
				Spec: musterv1alpha1.MCPServerSpec{
					Type:            "streamable-http",
					URL:             "https://marge.example.com/mcp",
					Timeout:         tt.timeout,
					MaxCallDuration: tt.maxCallDuration,
				},
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
}
