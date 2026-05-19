package reconciler

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mark3labs/mcp-go/mcp"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
)

// stubApplier records every Config it was asked to apply and optionally
// returns a sentinel error from the next Apply call.
type stubApplier struct {
	mu        sync.Mutex
	applied   []agentgateway.Config
	deleted   []string
	applyErr  error
	deleteErr error
}

func (s *stubApplier) Apply(_ context.Context, config agentgateway.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = append(s.applied, config)
	return s.applyErr
}

func (s *stubApplier) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, name)
	return s.deleteErr
}

func (s *stubApplier) appliedNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.applied))
	for i, c := range s.applied {
		out[i] = c.Name
	}
	return out
}

// stubAggregator implements api.AggregatorHandler so RegisterUpstream calls
// from the reconciler hit a recorder instead of nil. Only the upstream
// methods carry assertions; the rest return zero values.
type stubAggregator struct {
	mu           sync.Mutex
	registered   []string
	deregistered []string
	state        map[string]api.UpstreamServerState
	registerErr  error
}

func newStubAggregator() *stubAggregator {
	return &stubAggregator{state: make(map[string]api.UpstreamServerState)}
}

func (s *stubAggregator) RegisterUpstream(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.registerErr != nil {
		return s.registerErr
	}
	s.registered = append(s.registered, name)
	if _, preseeded := s.state[name]; !preseeded {
		s.state[name] = api.UpstreamServerConnected
	}
	return nil
}

func (s *stubAggregator) DeregisterUpstream(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deregistered = append(s.deregistered, name)
	delete(s.state, name)
	return nil
}

func (s *stubAggregator) UpstreamServerState(name string) api.UpstreamServerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state[name]
}

func (s *stubAggregator) GetServiceData() map[string]interface{} { return nil }
func (s *stubAggregator) GetEndpoint() string                    { return "" }
func (s *stubAggregator) GetPort() int                           { return 0 }
func (s *stubAggregator) CallTool(_ context.Context, _ string, _ map[string]interface{}) (*api.CallToolResult, error) {
	return nil, nil
}
func (s *stubAggregator) CallToolInternal(_ context.Context, _ string, _ map[string]interface{}) (*mcp.CallToolResult, error) {
	return nil, nil
}
func (s *stubAggregator) IsToolAvailable(_ string) bool { return false }
func (s *stubAggregator) GetAvailableTools() []string   { return nil }
func (s *stubAggregator) UpdateCapabilities()           {}
func (s *stubAggregator) RegisterServerPendingAuth(_, _, _ string, _ *api.AuthInfo) error {
	return nil
}
func (s *stubAggregator) RegisterServerPendingAuthWithConfig(_, _, _ string, _ *api.AuthInfo, _ *api.MCPServerAuth) error {
	return nil
}
func (s *stubAggregator) MarkUserStopped(_ string) {}
func (s *stubAggregator) MarkUserStarted(_ string) {}

// withAggregator swaps the API service-locator aggregator handler for the
// duration of one test. Use t.Cleanup so parallel tests don't leak state.
func withAggregator(t *testing.T, agg api.AggregatorHandler) {
	t.Helper()
	prev := api.GetAggregator()
	api.RegisterAggregator(agg)
	t.Cleanup(func() { api.RegisterAggregator(prev) })
}

const (
	testServerGitHub   = "github"
	testServerAlpha    = "alpha"
	testServerBeta     = "beta"
	testTypeStreamable = "streamable-http"
	testGithubURL      = "https://github.example.com/mcp"
)

func newReconcilerForTest(applier *stubApplier, mgr MCPServerManager, upd StatusUpdater) *MCPServerReconciler {
	r := NewMCPServerReconciler(mgr, func(_ context.Context, _, _ string) agentgateway.Applier { return applier })
	r.WithStatusUpdater(upd, "default")
	return r
}

func TestReconcileRegistersUpstreamOnAutoStart(t *testing.T) {
	applier := &stubApplier{}
	mgr := NewMockMCPServerManager()
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      testServerGitHub,
		Type:      testTypeStreamable,
		URL:       testGithubURL,
		AutoStart: true,
	})

	updater := NewMockStatusUpdater()
	agg := newStubAggregator()
	withAggregator(t, agg)

	r := newReconcilerForTest(applier, mgr, updater)

	result := r.Reconcile(t.Context(), ReconcileRequest{Name: testServerGitHub, Namespace: DefaultNamespace})

	require.NoError(t, result.Error)
	require.Equal(t, []string{testServerGitHub}, applier.appliedNames())
	require.Equal(t, []string{testServerGitHub}, agg.registered)
	require.Empty(t, agg.deregistered)
	require.Equal(t, musterv1alpha1.MCPServerStateConnected, updater.GetLastUpdatedMCPServer().Status.State)
}

func TestReconcileSkipsRegisterWhenAutoStartFalse(t *testing.T) {
	applier := &stubApplier{}
	mgr := NewMockMCPServerManager()
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      testServerGitHub,
		Type:      testTypeStreamable,
		URL:       testGithubURL,
		AutoStart: false,
	})

	updater := NewMockStatusUpdater()
	agg := newStubAggregator()
	withAggregator(t, agg)

	r := newReconcilerForTest(applier, mgr, updater)

	result := r.Reconcile(t.Context(), ReconcileRequest{Name: testServerGitHub, Namespace: DefaultNamespace})

	require.NoError(t, result.Error)
	require.Equal(t, []string{testServerGitHub}, applier.appliedNames(), "applyConfig still runs so agentgateway sees the route")
	require.Empty(t, agg.registered, "RegisterUpstream skipped when AutoStart=false")
}

func TestReconcileMarksNotSupportedInClusterOnStdioSentinel(t *testing.T) {
	applier := &stubApplier{applyErr: agentgateway.ErrUnsupportedTransport}
	mgr := NewMockMCPServerManager()
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "kubernetes",
		Type:      "stdio",
		Command:   "/usr/local/bin/mcp-kubernetes",
		AutoStart: true,
	})

	updater := NewMockStatusUpdater()
	agg := newStubAggregator()
	withAggregator(t, agg)

	r := newReconcilerForTest(applier, mgr, updater)

	result := r.Reconcile(t.Context(), ReconcileRequest{Name: "kubernetes", Namespace: "default"})

	require.NoError(t, result.Error)
	require.NotZero(t, result.RequeueAfter)
	require.Empty(t, agg.registered, "RegisterUpstream must be skipped when applyConfig short-circuits")

	server := updater.GetLastUpdatedMCPServer()
	require.NotNil(t, server)
	var found bool
	for _, c := range server.Status.Conditions {
		if c.Type == ConditionTypeNotSupportedInCluster {
			found = true
			break
		}
	}
	require.True(t, found, "NotSupportedInCluster condition should be set")
}

func TestReconcileDeleteDeregistersUpstreamAndDeletesApplier(t *testing.T) {
	applier := &stubApplier{}
	mgr := NewMockMCPServerManager()
	// No MCPServer registered → reconciler treats it as a delete.

	updater := NewMockStatusUpdater()
	agg := newStubAggregator()
	withAggregator(t, agg)

	r := newReconcilerForTest(applier, mgr, updater)

	result := r.Reconcile(t.Context(), ReconcileRequest{Name: testServerGitHub, Namespace: DefaultNamespace})

	require.NoError(t, result.Error)
	require.Equal(t, []string{testServerGitHub}, agg.deregistered)
	require.Equal(t, []string{testServerGitHub}, applier.deleted)
}

func TestReconcileRegisterFailureRequeues(t *testing.T) {
	applier := &stubApplier{}
	mgr := NewMockMCPServerManager()
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      testServerGitHub,
		Type:      testTypeStreamable,
		URL:       testGithubURL,
		AutoStart: true,
	})

	updater := NewMockStatusUpdater()
	agg := newStubAggregator()
	agg.registerErr = errors.New("upstream init failed")
	withAggregator(t, agg)

	r := newReconcilerForTest(applier, mgr, updater)

	result := r.Reconcile(t.Context(), ReconcileRequest{Name: testServerGitHub, Namespace: DefaultNamespace})

	require.Error(t, result.Error)
	require.True(t, result.Requeue)
	require.Equal(t, musterv1alpha1.MCPServerStateDisconnected, updater.GetLastUpdatedMCPServer().Status.State)
}

func TestReconcileClosureInvokedPerRequestWithCallArgs(t *testing.T) {
	mgr := NewMockMCPServerManager()
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      testServerAlpha,
		Type:      testTypeStreamable,
		URL:       "https://alpha.example/mcp",
		AutoStart: true,
	})
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      testServerBeta,
		Type:      testTypeStreamable,
		URL:       "https://beta.example/mcp",
		AutoStart: true,
	})

	withAggregator(t, newStubAggregator())

	type call struct{ name, namespace string }
	var (
		mu       sync.Mutex
		calls    []call
		appliers []*stubApplier
	)
	applierFn := func(_ context.Context, name, namespace string) agentgateway.Applier {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call{name: name, namespace: namespace})
		a := &stubApplier{}
		appliers = append(appliers, a)
		return a
	}

	r := NewMCPServerReconciler(mgr, applierFn).WithStatusUpdater(NewMockStatusUpdater(), "default")

	require.NoError(t, r.Reconcile(t.Context(), ReconcileRequest{Name: testServerAlpha, Namespace: DefaultNamespace}).Error)
	require.NoError(t, r.Reconcile(t.Context(), ReconcileRequest{Name: testServerBeta, Namespace: DefaultNamespace}).Error)

	require.Equal(t, []call{
		{name: testServerAlpha, namespace: DefaultNamespace},
		{name: testServerBeta, namespace: DefaultNamespace},
	}, calls)
	require.Len(t, appliers, 2)
	require.NotSame(t, appliers[0], appliers[1], "closure should return a distinct Applier per request")
	require.Equal(t, []string{testServerAlpha}, appliers[0].appliedNames())
	require.Equal(t, []string{testServerBeta}, appliers[1].appliedNames())
}

func TestReconcileMapsAuthRequiredFromAggregatorState(t *testing.T) {
	applier := &stubApplier{}
	mgr := NewMockMCPServerManager()
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      testServerGitHub,
		Type:      testTypeStreamable,
		URL:       testGithubURL,
		AutoStart: true,
	})

	updater := NewMockStatusUpdater()
	agg := newStubAggregator()
	withAggregator(t, agg)
	// Pretend the aggregator's RegisterUpstream rolled the upstream into
	// pending-auth state (the real implementation does this when the
	// upstream returns 401 on Initialize).
	agg.state[testServerGitHub] = api.UpstreamServerAuthRequired

	r := newReconcilerForTest(applier, mgr, updater)

	result := r.Reconcile(t.Context(), ReconcileRequest{Name: testServerGitHub, Namespace: DefaultNamespace})
	require.NoError(t, result.Error)
	require.Equal(t, musterv1alpha1.MCPServerStateAuthRequired, updater.GetLastUpdatedMCPServer().Status.State)
}
