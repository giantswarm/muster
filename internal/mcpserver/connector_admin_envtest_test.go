package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/giantswarm/muster/v5/internal/callerwrite"
	kubernetesclient "github.com/giantswarm/muster/v5/internal/client/kubernetes"
	"github.com/giantswarm/muster/v5/internal/server"
)

// TestConnectorAdminEnvtest checks IsConnectorAdmin against a real
// kube-apiserver with RBAC: a user bound to an mcpserver-editor style Role in
// a namespace is connector admin of the servers there and of no other
// namespace's; a user without the binding is connector admin of none.
//
// Requires envtest binaries: run via `make test-envtest`, which resolves
// KUBEBUILDER_ASSETS with setup-envtest. Skipped otherwise.
func TestConnectorAdminEnvtest(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run via `make test-envtest`")
	}

	adminToken := testJWT(t, callerwrite.DefaultKubernetesAudience)
	otherToken := testJWT(t, []string{callerwrite.DefaultKubernetesAudience, "muster"})
	tokenFile := filepath.Join(t.TempDir(), "tokens.csv")
	tokens := fmt.Sprintf("%s,connector-admin,1000,\n%s,someone-else,1001,\n", adminToken, otherToken)
	require.NoError(t, os.WriteFile(tokenFile, []byte(tokens), 0o600))

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "helm", "muster", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	testEnv.ControlPlane.GetAPIServer().Configure().Set("token-auth-file", tokenFile)
	cfg, err := testEnv.Start()
	require.NoError(t, err, "start envtest")
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})
	saClient, err := kubernetesclient.New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = saClient.Close() })

	ctx := context.Background()
	for _, ns := range []string{"muster", "team-a"} {
		require.NoError(t, saClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}))
	}
	require.NoError(t, saClient.Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "mcpserver-editor", Namespace: "muster"},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{"muster.giantswarm.io"},
			Resources: []string{"mcpservers"},
			Verbs:     []string{"create", "update", "patch", "delete"},
		}},
	}))
	require.NoError(t, saClient.Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "mcpserver-editor", Namespace: "muster"},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: "connector-admin"}},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "mcpserver-editor"},
	}))

	adapter := NewAdapterWithClient(saClient, "muster")
	adapter.EnableWritesAsCaller(callerwrite.NewKubernetesClientFactory(cfg), "")

	tests := []struct {
		name  string
		token string
		key   string
		want  bool
	}{
		{name: "bound user, server in muster's namespace", token: adminToken, key: "github", want: true},
		{name: "bound user, server of another namespace", token: adminToken, key: "team-a/github", want: false},
		{name: "unbound user", token: otherToken, key: "github", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			admin, err := adapter.IsConnectorAdmin(server.ContextWithIDToken(ctx, tt.token), tt.key)
			require.NoError(t, err)
			require.Equal(t, tt.want, admin)
		})
	}
}
