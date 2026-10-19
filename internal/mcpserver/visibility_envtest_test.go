package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/callerwrite"
	kubernetesclient "github.com/giantswarm/muster/v5/internal/client/kubernetes"
	"github.com/giantswarm/muster/v5/internal/server"
)

// TestListsServersAsTheCallerEnvtest runs agent-permissions D9 against a real
// kube-apiserver with RBAC, in Kubernetes mode with two organization
// namespaces next to muster's own: muster lists the servers with each
// caller's token, and a caller sees exactly the servers of the namespaces
// where RBAC lets them list mcpservers. A namespace that is not an
// organization namespace is never served. A token the API server rejects, or
// no token, sees no server and is told to sign in again.
//
// Requires envtest binaries: run via `make test-envtest`. Skipped otherwise.
func TestListsServersAsTheCallerEnvtest(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run via `make test-envtest`")
	}
	const musterNS = "muster"
	aud := callerwrite.DefaultKubernetesAudience
	// Distinct JWT-shaped tokens with the API server's audience, one per user.
	tokens := map[string]string{
		"everyone":       testJWT(t, []string{aud, "everyone"}),
		"org-a-member":   testJWT(t, []string{aud, "org-a-member"}),
		"cluster-reader": testJWT(t, []string{aud, "cluster-reader"}),
		"outsider":       testJWT(t, []string{aud, "outsider"}),
	}
	var tokenLines strings.Builder
	uid := 1000
	for user, token := range tokens {
		fmt.Fprintf(&tokenLines, "%s,%s,%d,\n", token, user, uid)
		uid++
	}
	tokenFile := filepath.Join(t.TempDir(), "tokens.csv")
	if err := os.WriteFile(tokenFile, []byte(tokenLines.String()), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "helm", "muster", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	testEnv.ControlPlane.GetAPIServer().Configure().Set("token-auth-file", tokenFile)
	adminCfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})
	saClient, err := kubernetesclient.New(adminCfg)
	if err != nil {
		t.Fatalf("create kubernetes muster client: %v", err)
	}
	t.Cleanup(func() { _ = saClient.Close() })
	ctx := context.Background()

	for _, ns := range []string{musterNS, "org-a", "org-b", "team-x"} {
		if err := saClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
			t.Fatalf("create namespace %s: %v", ns, err)
		}
		srv := &musterv1alpha1.MCPServer{
			ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: ns},
			Spec:       musterv1alpha1.MCPServerSpec{Type: "streamable-http", URL: "http://example.com/mcp"},
		}
		if err := saClient.Create(ctx, srv); err != nil {
			t.Fatalf("create MCPServer in %s: %v", ns, err)
		}
	}

	// The roles of the agent-permissions plan: list mcpservers in muster's
	// namespace for every signed-in person, in an organization namespace for
	// its members, and in every namespace for a cluster-wide reader.
	viewer := rbacv1.PolicyRule{APIGroups: []string{"muster.giantswarm.io"}, Resources: []string{"mcpservers"}, Verbs: []string{"get", "list", "watch"}}
	bind := func(namespace string, users ...string) {
		role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "mcpserver-viewer", Namespace: namespace}, Rules: []rbacv1.PolicyRule{viewer}}
		binding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "mcpserver-viewer", Namespace: namespace},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "mcpserver-viewer"},
		}
		for _, user := range users {
			binding.Subjects = append(binding.Subjects, rbacv1.Subject{Kind: rbacv1.UserKind, Name: user})
		}
		if err := saClient.Create(ctx, role); err != nil {
			t.Fatalf("create role in %s: %v", namespace, err)
		}
		if err := saClient.Create(ctx, binding); err != nil {
			t.Fatalf("create rolebinding in %s: %v", namespace, err)
		}
	}
	bind(musterNS, "everyone", "org-a-member")
	bind("org-a", "org-a-member")
	clusterRole := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "mcpserver-reader"}, Rules: []rbacv1.PolicyRule{viewer}}
	clusterBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "mcpserver-reader"},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: "cluster-reader"}},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "mcpserver-reader"},
	}
	if err := saClient.Create(ctx, clusterRole); err != nil {
		t.Fatalf("create clusterrole: %v", err)
	}
	if err := saClient.Create(ctx, clusterBinding); err != nil {
		t.Fatalf("create clusterrolebinding: %v", err)
	}

	adapter := NewAdapterWithClient(saClient, musterNS)
	adapter.ServeAllNamespaces()
	api.RegisterMCPServerVisibility(NewVisibility(saClient, callerwrite.NewKubernetesClientFactory(adminCfg), "", musterNS))
	t.Cleanup(func() { api.RegisterMCPServerVisibility(nil) })

	listed := func(t *testing.T, callerCtx context.Context) ([]string, string) {
		t.Helper()
		result, err := adapter.ExecuteTool(callerCtx, "mcpserver_list", map[string]interface{}{"showAll": true})
		if err != nil || result.IsError {
			t.Fatalf("mcpserver_list: err=%v result=%s", err, resultText(t, result))
		}
		content, _ := result.Content[0].(map[string]interface{})
		servers, _ := content["mcpServers"].([]api.MCPServerInfo)
		var keys []string
		for _, s := range servers {
			keys = append(keys, s.ServerKey())
		}
		slices.Sort(keys)
		return keys, api.CallerVisibility(callerCtx).Reason
	}

	tests := []struct {
		name       string
		token      string
		want       []string
		wantReason string
	}{
		{name: "a person of no tenant sees the servers for everyone", token: tokens["everyone"], want: []string{"github"}},
		{name: "a member of org-a also sees org-a's server", token: tokens["org-a-member"], want: []string{"github", "org-a/github"}},
		{name: "a cluster-wide reader sees every organization, never another namespace", token: tokens["cluster-reader"], want: []string{"github", "org-a/github", "org-b/github"}},
		{name: "a person with no binding sees nothing", token: tokens["outsider"], want: nil},
		{name: "a token the API server rejects sees nothing and signs in again", token: testJWT(t, []string{aud, "unknown"}), want: nil, wantReason: "sign in again"},
		{name: "no token sees nothing and signs in again", token: "", want: nil, wantReason: "sign in again"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			callerCtx := server.ContextWithIDToken(ctx, tc.token)
			got, reason := listed(t, callerCtx)
			if !slices.Equal(got, tc.want) {
				t.Errorf("listed %v, want %v", got, tc.want)
			}
			if tc.wantReason != "" && !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason %q, want it to say %q", reason, tc.wantReason)
			}
		})
	}

	t.Run("core_mcpserver_get of another organization's server answers as for a missing one", func(t *testing.T) {
		result, err := adapter.ExecuteTool(server.ContextWithIDToken(ctx, tokens["org-a-member"]), "mcpserver_get", map[string]interface{}{"name": "org-b/github"})
		if err != nil {
			t.Fatalf("mcpserver_get: %v", err)
		}
		if !result.IsError || !strings.Contains(resultText(t, result), "not found") {
			t.Errorf("mcpserver_get = %s, want not found", resultText(t, result))
		}
	})
}
