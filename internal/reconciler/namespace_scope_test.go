package reconciler

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

const testMusterNamespace = "agent-platform"

// TestDetectorScopesTheWatchPerResourceType: MCPServer objects are watched in
// all namespaces only when the detector is told so; Workflow objects, like
// every other type, stay in muster's namespace.
func TestDetectorScopesTheWatchPerResourceType(t *testing.T) {
	d, err := NewKubernetesDetector(nil, testMusterNamespace)
	require.NoError(t, err)

	opts := d.cacheOptions()
	assert.Equal(t, []string{testMusterNamespace}, namespaceKeys(opts.DefaultNamespaces))
	assert.Nil(t, opts.ByObject, "off by default: MCPServer objects stay in muster's namespace")

	d.WatchMCPServersInAllNamespaces()
	opts = d.cacheOptions()
	assert.Equal(t, []string{testMusterNamespace}, namespaceKeys(opts.DefaultNamespaces), "Workflow objects stay in muster's namespace")
	require.Len(t, opts.ByObject, 1)
	for obj, byObject := range opts.ByObject {
		assert.IsType(t, &musterv1alpha1.MCPServer{}, obj)
		assert.NotNil(t, byObject.Namespaces, "a nil map would fall back to muster's namespace")
		assert.Empty(t, byObject.Namespaces, "an empty map caches all namespaces")
	}
	assert.Contains(t, d.namespaceDisplay(), "MCPServer objects in all namespaces")
}

func namespaceKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// namespacedFixture is a reconciler for muster's namespace with one server
// definition per (namespace, info), keyed as the MCPServer manager keys them.
func namespacedFixture(t *testing.T, servers map[string]*api.MCPServerInfo) (*MCPServerReconciler, *MockOrchestratorAPI, *MockStatusUpdater) {
	t.Helper()
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	statusUpdater := NewMockStatusUpdater()
	statusUpdater.KubernetesMode = true
	for namespace, info := range servers {
		info.Namespace = namespace
		info.Key = api.MCPServerKey(namespace, info.Name, testMusterNamespace)
		info.AutoStart = true
		mgr.AddMCPServer(info)
		crd := &musterv1alpha1.MCPServer{}
		crd.Name = info.Name
		crd.Namespace = namespace
		crd.Spec.Type = info.Type
		statusUpdater.AddMCPServer(crd)
	}
	r := NewMCPServerReconciler(orchAPI, mgr, NewMockServiceRegistry()).WithStatusUpdater(statusUpdater, testMusterNamespace)
	return r, orchAPI, statusUpdater
}

// TestReconcileTwoServersWithOneName: a server called github in muster's
// namespace and one in a tenant's namespace are two services: the first under
// its bare name, as today, the second under "<namespace>/<name>". Each status
// is written on its own object.
func TestReconcileTwoServersWithOneName(t *testing.T) {
	remote := func() *api.MCPServerInfo {
		return &api.MCPServerInfo{Name: "github", Type: "streamable-http", URL: "https://mcp.example.com/mcp"}
	}
	r, orchAPI, statusUpdater := namespacedFixture(t, map[string]*api.MCPServerInfo{
		testMusterNamespace: remote(),
		"org-a":             remote(),
	})

	for _, namespace := range []string{testMusterNamespace, "org-a"} {
		result := r.Reconcile(context.Background(), ReconcileRequest{Type: ResourceTypeMCPServer, Name: "github", Namespace: namespace, Attempt: 1})
		require.NoError(t, result.Error)
		synced := statusUpdater.GetLastUpdatedMCPServer()
		require.NotNil(t, synced)
		assert.Equal(t, namespace, synced.Namespace, "the status is written on the server's own object")
		assert.Equal(t, "github", synced.Name)
	}

	assert.True(t, orchAPI.StartedServices["github"], "the server of muster's namespace keeps its name")
	assert.True(t, orchAPI.StartedServices["org-a/github"], "the tenant's server carries its namespace")
	assert.Len(t, orchAPI.StartedServices, 2)
}

// TestReconcileRefusesAnIdentityOutsideMustersNamespace: a tenant server that
// asks for an SSO or SigV4 identity is never started and reads Failed with the
// reason; the same definition in muster's namespace starts.
func TestReconcileRefusesAnIdentityOutsideMustersNamespace(t *testing.T) {
	for _, tt := range []struct {
		name   string
		auth   *api.MCPServerAuth
		family *api.MCPServerFamily
	}{
		{name: "forwardToken", auth: &api.MCPServerAuth{ForwardToken: true}},
		{name: "requiredAudiences", auth: &api.MCPServerAuth{ForwardToken: true, RequiredAudiences: []string{"dex-k8s-authenticator"}}},
		{name: "forwardIdentity", auth: &api.MCPServerAuth{ForwardIdentity: true}},
		{name: "tokenExchange", auth: &api.MCPServerAuth{TokenExchange: &api.TokenExchangeConfig{Enabled: true}}},
		{name: "sigv4", auth: &api.MCPServerAuth{Type: api.MCPServerAuthTypeSigV4}},
		{name: "family", family: &api.MCPServerFamily{Name: "kubernetes", InstanceArg: "cluster"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := func() *api.MCPServerInfo {
				return &api.MCPServerInfo{Name: "kubernetes", Type: "streamable-http", URL: "https://mcp.example.com/mcp", Auth: tt.auth, Family: tt.family}
			}

			r, orchAPI, statusUpdater := namespacedFixture(t, map[string]*api.MCPServerInfo{"org-a": server()})
			result := r.Reconcile(context.Background(), ReconcileRequest{Type: ResourceTypeMCPServer, Name: "kubernetes", Namespace: "org-a", Attempt: 1})
			require.ErrorIs(t, result.Error, api.ErrIdentityOutsideNamespace)
			assert.Empty(t, orchAPI.StartCalls, "a refused definition is never started")
			synced := statusUpdater.GetLastUpdatedMCPServer()
			require.NotNil(t, synced)
			assert.Equal(t, musterv1alpha1.MCPServerStateFailed, synced.Status.State)
			assert.Contains(t, synced.Status.LastError, "not allowed in namespace")

			r, orchAPI, _ = namespacedFixture(t, map[string]*api.MCPServerInfo{testMusterNamespace: server()})
			result = r.Reconcile(context.Background(), ReconcileRequest{Type: ResourceTypeMCPServer, Name: "kubernetes", Namespace: testMusterNamespace, Attempt: 1})
			require.NoError(t, result.Error)
			assert.True(t, orchAPI.StartedServices["kubernetes"])
		})
	}
}

// TestReconcileRefusesATenantPrefixInMustersNamespace: a server for everyone
// may not take the prefix space of an organization namespace.
func TestReconcileRefusesATenantPrefixInMustersNamespace(t *testing.T) {
	r, orchAPI, statusUpdater := namespacedFixture(t, map[string]*api.MCPServerInfo{
		testMusterNamespace: {Name: "github", ToolPrefix: "org-a", Type: "streamable-http", URL: "https://mcp.example.com/mcp"},
	})
	result := r.Reconcile(context.Background(), ReconcileRequest{Type: ResourceTypeMCPServer, Name: "github", Namespace: testMusterNamespace, Attempt: 1})
	require.ErrorIs(t, result.Error, api.ErrReservedServerName)
	assert.Empty(t, orchAPI.StartCalls)
	assert.Equal(t, musterv1alpha1.MCPServerStateFailed, statusUpdater.GetLastUpdatedMCPServer().Status.State)
	assert.True(t, isRefusal(result.Error))
	assert.True(t, isRefusal(api.ErrIdentityOutsideNamespace))
	assert.False(t, isRefusal(errors.New("connection refused")))
}

// TestStateChangeOfATenantServiceReconcilesItsObject: a state change of the
// service "org-a/github" reconciles the object github in org-a, and a change
// of "github" the object in muster's namespace.
func TestStateChangeOfATenantServiceReconcilesItsObject(t *testing.T) {
	r, _, _ := namespacedFixture(t, nil)
	t.Cleanup(func() { _ = r.Close() })
	m := NewManager(ManagerConfig{Namespace: testMusterNamespace})
	require.NoError(t, m.RegisterReconciler(r))
	b := NewStateChangeBridge(NewMockOrchestratorAPI(), m, testMusterNamespace)

	b.triggerReconcile(ResourceTypeMCPServer, "org-a/github")
	b.triggerReconcile(ResourceTypeMCPServer, "github")

	assert.ElementsMatch(t, []ReconcileRequest{
		{Type: ResourceTypeMCPServer, Name: "github", Namespace: "org-a", Attempt: 1},
		{Type: ResourceTypeMCPServer, Name: "github", Namespace: testMusterNamespace, Attempt: 1},
	}, drain(t, m, 2))
}

// TestResyncReconcilesEveryServerInItsNamespace: the periodic resync names a
// tenant server by its key and reconciles the object in its namespace.
func TestResyncReconcilesEveryServerInItsNamespace(t *testing.T) {
	r, _, _ := namespacedFixture(t, map[string]*api.MCPServerInfo{
		testMusterNamespace: {Name: "github", Type: "streamable-http"},
		"org-a":             {Name: "github", Type: "streamable-http"},
	})
	t.Cleanup(func() { _ = r.Close() })
	m := NewManager(ManagerConfig{Namespace: testMusterNamespace})
	m.ctx = context.Background()
	require.NoError(t, m.RegisterReconciler(r))

	m.resyncAll()

	assert.ElementsMatch(t, []ReconcileRequest{
		{Type: ResourceTypeMCPServer, Name: "github", Namespace: "org-a", Attempt: 1},
		{Type: ResourceTypeMCPServer, Name: "github", Namespace: testMusterNamespace, Attempt: 1},
	}, drain(t, m, 2))
}

func drain(t *testing.T, m *Manager, n int) []ReconcileRequest {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := make([]ReconcileRequest, 0, n)
	for range n {
		req, ok := m.queue.Get(ctx)
		require.True(t, ok, "expected %d requests, got %v", n, got)
		got = append(got, req)
		m.queue.Done(req)
	}
	return got
}

// TestFilesystemModeHasNoTenants: the filesystem store gives every
// definition the namespace "default"; with muster's namespace set to another
// value, an SSO server is still muster's own and starts under its name.
func TestFilesystemModeHasNoTenants(t *testing.T) {
	mgr := NewMockMCPServerManager()
	mgr.AddMCPServer(&api.MCPServerInfo{Name: "kubernetes", Namespace: "default", Type: "streamable-http", URL: "https://mcp.example.com/mcp", AutoStart: true, Auth: &api.MCPServerAuth{ForwardToken: true}})
	orchAPI := NewMockOrchestratorAPI()
	statusUpdater := NewMockStatusUpdater()
	r := NewMCPServerReconciler(orchAPI, mgr, NewMockServiceRegistry()).WithStatusUpdater(statusUpdater, "team-alpha")
	t.Cleanup(func() { _ = r.Close() })

	result := r.Reconcile(context.Background(), ReconcileRequest{Type: ResourceTypeMCPServer, Name: "kubernetes", Namespace: "team-alpha", Attempt: 1})
	require.NoError(t, result.Error)
	assert.True(t, orchAPI.StartedServices["kubernetes"])
}
