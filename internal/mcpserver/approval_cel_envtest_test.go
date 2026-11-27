package mcpserver

import (
	"fmt"
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

// TestApprovalCELEnvtest applies MCPServers to a real kube-apiserver with the
// generated CRD, so the spec.approval rules are checked as the apiserver
// compiles and runs them: the enums of rule and of each tool's setting, the
// size cap of tools, and no default written into an object without approval.
//
// Requires envtest binaries: run via `make test-envtest`, which resolves
// KUBEBUILDER_ASSETS with setup-envtest. Skipped otherwise.
func TestApprovalCELEnvtest(t *testing.T) {
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

	tooMany := map[string]string{}
	for i := range 513 {
		tooMany[fmt.Sprintf("tool_%d", i)] = "Ask"
	}

	tests := []struct {
		name     string
		approval *musterv1alpha1.MCPServerApproval
		rejected string
	}{
		{name: "no-approval"},
		{name: "empty-approval", approval: &musterv1alpha1.MCPServerApproval{}},
		{name: "rule-allow", approval: &musterv1alpha1.MCPServerApproval{Rule: "Allow"}},
		{
			name:     "every-setting",
			approval: &musterv1alpha1.MCPServerApproval{Rule: "Ask", Tools: map[string]string{"get": "Allow", "merge": "Ask", "delete": "Block"}},
		},
		{name: "rule-block", approval: &musterv1alpha1.MCPServerApproval{Rule: "Block"}},
		{
			name:     "rule-lowercase",
			approval: &musterv1alpha1.MCPServerApproval{Rule: "ask"},
			rejected: "rule must be Allow, Ask or Block",
		},
		{
			name:     "rule-unknown",
			approval: &musterv1alpha1.MCPServerApproval{Rule: "Deny"},
			rejected: "rule must be Allow, Ask or Block",
		},
		{
			name:     "tool-unknown-setting",
			approval: &musterv1alpha1.MCPServerApproval{Tools: map[string]string{"get": "Deny"}},
			rejected: "a tool's setting must be Allow, Ask or Block",
		},
		{
			name:     "too-many-tools",
			approval: &musterv1alpha1.MCPServerApproval{Tools: tooMany},
			rejected: "must have at most 512 items",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &musterv1alpha1.MCPServer{
				ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: "default"},
				Spec: musterv1alpha1.MCPServerSpec{
					Type:     "streamable-http",
					URL:      "https://mcp.example.com/mcp",
					Approval: tt.approval,
				},
			}
			err := k8s.Create(t.Context(), server)
			if tt.rejected != "" {
				require.True(t, apierrors.IsInvalid(err), "want an admission rejection, got %v", err)
				require.ErrorContains(t, err, tt.rejected)
				return
			}
			require.NoError(t, err)

			stored, err := k8s.GetMCPServer(t.Context(), tt.name, "default")
			require.NoError(t, err)
			if tt.approval == nil {
				require.Nil(t, stored.Spec.Approval, "no default may be written into an object without approval")
				return
			}
			require.Equal(t, tt.approval.Rule, stored.Spec.Approval.Rule, "no default rule may be written")
		})
	}
}
