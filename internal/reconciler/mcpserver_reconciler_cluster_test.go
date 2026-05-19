package reconciler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	agw "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	k8sapply "github.com/giantswarm/muster/internal/reconciler/agentgateway/k8s"
)

func clusterTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, gwv1.Install(s))
	s.AddKnownTypes(
		schema.GroupVersion{Group: "agentgateway.dev", Version: "v1alpha1"},
		&agw.AgentgatewayBackend{}, &agw.AgentgatewayBackendList{},
		&agw.AgentgatewayPolicy{}, &agw.AgentgatewayPolicyList{},
	)
	metav1.AddToGroupVersion(s, schema.GroupVersion{Group: "agentgateway.dev", Version: "v1alpha1"})
	return s
}

func newFakeClient(t *testing.T) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(clusterTestScheme(t)).Build()
}

func newClusterReconciler(t *testing.T, mgr MCPServerManager, registry api.ServiceRegistryHandler, updater StatusUpdater) *MCPServerReconciler {
	t.Helper()
	orchAPI := NewMockOrchestratorAPI()
	return NewMCPServerReconcilerCluster(
		orchAPI,
		mgr,
		registry,
		newFakeClient(t),
		k8sapply.Config{GatewayName: "agw", GatewayNamespace: "default"},
		updater,
		"default",
	)
}

func seedMCPServer(updater *MockStatusUpdater, name, namespace, uid string) {
	server := &musterv1alpha1.MCPServer{}
	server.Name = name
	server.Namespace = namespace
	server.UID = types.UID(uid)
	server.APIVersion = musterv1alpha1.GroupVersion.String()
	server.Kind = "MCPServer"
	updater.MCPServers[namespace+"/"+name] = server
}

func TestNewMCPServerReconcilerCluster_PanicsOnNilStatusUpdater(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		NewMCPServerReconcilerCluster(
			NewMockOrchestratorAPI(),
			NewMockMCPServerManager(),
			NewMockServiceRegistry(),
			newFakeClient(t),
			k8sapply.Config{GatewayName: "agw", GatewayNamespace: "default"},
			nil,
			"default",
		)
	})
}

func TestNewMCPServerReconcilerCluster_PanicsOnNilClient(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		NewMCPServerReconcilerCluster(
			NewMockOrchestratorAPI(),
			NewMockMCPServerManager(),
			NewMockServiceRegistry(),
			nil,
			k8sapply.Config{GatewayName: "agw", GatewayNamespace: "default"},
			NewMockStatusUpdater(),
			"default",
		)
	})
}

func TestResolveOwnerRef_PropagatesGetError(t *testing.T) {
	t.Parallel()

	mgr := NewMockMCPServerManager()
	registry := NewMockServiceRegistry()
	updater := NewMockStatusUpdater()
	updater.GetMCPServerError = errors.New("api server unreachable")

	r := newClusterReconciler(t, mgr, registry, updater)

	_, err := r.resolveOwnerRef(t.Context(), "demo", "default")
	require.Error(t, err)
	require.Contains(t, err.Error(), "api server unreachable")
}

func TestResolveOwnerRef_ErrorsWhenUIDEmpty(t *testing.T) {
	t.Parallel()

	mgr := NewMockMCPServerManager()
	registry := NewMockServiceRegistry()
	updater := NewMockStatusUpdater()

	r := newClusterReconciler(t, mgr, registry, updater)

	_, err := r.resolveOwnerRef(t.Context(), "demo", "default")
	require.Error(t, err)
	require.Contains(t, err.Error(), "UID is empty")
}

func TestApplierFor_CachesOwnerRefAcrossReconciles(t *testing.T) {
	t.Parallel()

	mgr := NewMockMCPServerManager()
	registry := NewMockServiceRegistry()
	updater := NewMockStatusUpdater()
	seedMCPServer(updater, "demo", "default", "uid-demo")

	r := newClusterReconciler(t, mgr, registry, updater)

	ctx := t.Context()
	_, err := r.applierFor(ctx, "demo", "default")
	require.NoError(t, err)
	_, err = r.applierFor(ctx, "demo", "default")
	require.NoError(t, err)

	r.ownerRefMu.RLock()
	require.Len(t, r.ownerRefs, 1)
	cached := r.ownerRefs[types.NamespacedName{Namespace: "default", Name: "demo"}]
	r.ownerRefMu.RUnlock()
	require.Equal(t, types.UID("uid-demo"), cached.UID)
}

func TestReconcileDelete_InvalidatesOwnerRefCache(t *testing.T) {
	t.Parallel()

	mgr := NewMockMCPServerManager()
	registry := NewMockServiceRegistry()
	updater := NewMockStatusUpdater()
	seedMCPServer(updater, "demo", "default", "uid-demo")

	r := newClusterReconciler(t, mgr, registry, updater)

	ctx := t.Context()
	_, err := r.applierFor(ctx, "demo", "default")
	require.NoError(t, err)

	r.ownerRefMu.RLock()
	require.Len(t, r.ownerRefs, 1)
	r.ownerRefMu.RUnlock()

	r.reconcileDelete(ctx, ReconcileRequest{Name: "demo", Namespace: "default"})

	r.ownerRefMu.RLock()
	require.Empty(t, r.ownerRefs)
	r.ownerRefMu.RUnlock()
}

func TestApplyConfig_StdioInClusterMode_SetsCondition(t *testing.T) {
	t.Parallel()

	mgr := NewMockMCPServerManager()
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "stdio-server",
		Type:      "stdio",
		Command:   "/bin/mcp",
		AutoStart: true,
	})

	registry := NewMockServiceRegistry()
	updater := NewMockStatusUpdater()
	seedMCPServer(updater, "stdio-server", "default", "uid-stdio")

	r := newClusterReconciler(t, mgr, registry, updater)

	req := ReconcileRequest{Type: ResourceTypeMCPServer, Name: "stdio-server", Namespace: "default", Attempt: 1}
	result := r.Reconcile(t.Context(), req)

	require.NoError(t, result.Error)
	require.Equal(t, DefaultStatusSyncInterval, result.RequeueAfter)

	server := updater.MCPServers["default/stdio-server"]
	require.NotNil(t, server)
	var found bool
	for _, c := range server.Status.Conditions {
		if c.Type == ConditionTypeNotSupportedInCluster {
			require.Equal(t, metav1.ConditionTrue, c.Status)
			require.Equal(t, reasonStdioInClusterMode, c.Reason)
			found = true
		}
	}
	require.True(t, found, "expected NotSupportedInCluster condition to be set")
}

func TestApplierFor_FilesystemMode_ReturnsYAMLApplier(t *testing.T) {
	t.Parallel()

	stub := stubApplier{}
	r := NewMCPServerReconcilerFilesystem(
		NewMockOrchestratorAPI(),
		NewMockMCPServerManager(),
		NewMockServiceRegistry(),
		stub,
		stub,
	)

	got, err := r.applierFor(t.Context(), "any", "default")
	require.NoError(t, err)
	_, ok := got.(stubApplier)
	require.True(t, ok, "filesystem mode must return the wired Applier instance")
}

func TestApplierFor_ClusterMode_ReturnsK8sApplier(t *testing.T) {
	t.Parallel()

	mgr := NewMockMCPServerManager()
	registry := NewMockServiceRegistry()
	updater := NewMockStatusUpdater()
	seedMCPServer(updater, "demo", "default", "uid-demo")

	r := newClusterReconciler(t, mgr, registry, updater)

	got, err := r.applierFor(t.Context(), "demo", "default")
	require.NoError(t, err)
	_, ok := got.(*k8sapply.Applier)
	require.True(t, ok, "cluster mode must construct a *k8s.Applier")
}

func TestReconcileDelete_DeleterError_PropagatesAndRequeues(t *testing.T) {
	t.Parallel()

	mgr := NewMockMCPServerManager()
	registry := NewMockServiceRegistry()
	registry.AddService("demo", &MockServiceInfo{
		Name:        "demo",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
	})

	failingDeleter := failingDeleter{err: errors.New("disk error")}
	r := NewMCPServerReconcilerFilesystem(
		NewMockOrchestratorAPI(),
		mgr,
		registry,
		stubApplier{},
		failingDeleter,
	)

	res := r.reconcileDelete(t.Context(), ReconcileRequest{Name: "demo", Namespace: "default"})

	require.Error(t, res.Error)
	require.True(t, res.Requeue, "Deleter error must trigger a requeue")
	require.Contains(t, res.Error.Error(), "disk error")

	// Service registry entry must still be present — Deleter ran before
	// StopService so the resource remains finalized.
	_, exists := registry.Get("demo")
	require.True(t, exists)
}

// =============================================================================
// failingDeleter — Deleter that always returns the configured error
// =============================================================================

type failingDeleter struct {
	err error
}

func (d failingDeleter) Delete(context.Context, string) error { return d.err }
