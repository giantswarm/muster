package reconciler

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	kubernetesclient "github.com/giantswarm/muster/v5/internal/client/kubernetes"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// TestDetectorReconcilesANamespaceWhoseSelectionChangesEnvtest: with a
// namespace selector that reads labels, the detector reconciles every server
// of a namespace whose labels move it in or out of the selection, and of a
// namespace a server of muster's namespace starts or stops shadowing; a
// label change of a server that moves it in or out of the server selector is
// reconciled too.
//
// Requires envtest binaries: run via `make test-envtest`. Skipped otherwise.
func TestDetectorReconcilesANamespaceWhoseSelectionChangesEnvtest(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run via `make test-envtest`")
	}
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "helm", "muster", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = testEnv.Stop() })

	ctx := t.Context()
	c, err := kubernetesclient.New(cfg)
	require.NoError(t, err)
	for _, ns := range []string{testMusterNamespace, "team-a"} {
		require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}))
	}
	server := func(namespace, name string) *musterv1alpha1.MCPServer {
		return &musterv1alpha1.MCPServer{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec:       musterv1alpha1.MCPServerSpec{Type: "streamable-http", URL: "https://mcp.example.com/mcp"},
		}
	}
	require.NoError(t, c.Create(ctx, server("team-a", "github")))

	d, err := NewKubernetesDetector(cfg, testMusterNamespace)
	require.NoError(t, err)
	d.WatchMCPServerScope(labels.SelectorFromSet(labels.Set{"team": "true"}), labels.SelectorFromSet(labels.Set{"served": "true"}))
	require.NoError(t, d.AddResourceType(ResourceTypeMCPServer))
	changes := make(chan ChangeEvent, 64)
	require.NoError(t, d.Start(ctx, changes))
	t.Cleanup(func() { _ = d.Stop() })

	expect := func(t *testing.T, operation ChangeOperation, namespace, name string) {
		t.Helper()
		deadline := time.After(20 * time.Second)
		for {
			select {
			case event := <-changes:
				if event.Operation == operation && event.Namespace == namespace && event.Name == name {
					return
				}
			case <-deadline:
				t.Fatalf("no %s event for %s/%s", operation, namespace, name)
			}
		}
	}
	expect(t, OperationCreate, "team-a", "github")

	t.Run("a namespace labelled into the selection", func(t *testing.T) {
		var ns corev1.Namespace
		require.NoError(t, c.Get(ctx, ctrlclient.ObjectKey{Name: "team-a"}, &ns))
		ns.Labels = map[string]string{"team": "true"}
		require.NoError(t, c.Update(ctx, &ns))
		expect(t, OperationUpdate, "team-a", "github")
	})

	t.Run("a server of muster's namespace that shadows the namespace", func(t *testing.T) {
		require.NoError(t, c.Create(ctx, server(testMusterNamespace, "team-a")))
		expect(t, OperationCreate, testMusterNamespace, "team-a")
		expect(t, OperationUpdate, "team-a", "github")

		require.NoError(t, c.Delete(ctx, server(testMusterNamespace, "team-a")))
		expect(t, OperationDelete, testMusterNamespace, "team-a")
		expect(t, OperationUpdate, "team-a", "github")
	})

	t.Run("a server labelled into the server selection", func(t *testing.T) {
		var obj musterv1alpha1.MCPServer
		require.NoError(t, c.Get(ctx, ctrlclient.ObjectKey{Namespace: "team-a", Name: "github"}, &obj))
		obj.Labels = map[string]string{"served": "true"}
		require.NoError(t, c.Update(ctx, &obj))
		expect(t, OperationUpdate, "team-a", "github")
	})
}
