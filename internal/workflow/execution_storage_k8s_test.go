package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/giantswarm/muster/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// These tests define the contract for the Kubernetes-backed ExecutionStorage
// (Phase 2 of #930). They are written test-first: the production
// newK8sExecutionStorage / k8sExecutionStorage / WorkflowExecution CRD do not
// exist yet, so this file is expected to fail to compile until the storage
// backend and CRD type land.

// k8sTestScheme registers the muster CRD types (including WorkflowExecution)
// alongside the core types so the controller-runtime fake client can serve them.
func k8sTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(musterv1alpha1.AddToScheme(scheme))
	return scheme
}

// newTestK8sStorage builds a k8sExecutionStorage backed by an in-memory fake
// client, with the clock pinned to now for deterministic retention tests.
func newTestK8sStorage(t *testing.T, now time.Time, seed ...client.Object) *k8sExecutionStorage {
	t.Helper()
	fakeClient := fake.NewClientBuilder().
		WithScheme(k8sTestScheme(t)).
		WithObjects(seed...).
		Build()
	s := newK8sExecutionStorage(fakeClient, "default")
	s.now = func() time.Time { return now }
	return s
}

func sampleExecution(id, workflow string, status api.WorkflowExecutionStatus, startedAt time.Time) *api.WorkflowExecution {
	return &api.WorkflowExecution{
		ExecutionID:  id,
		WorkflowName: workflow,
		Status:       status,
		StartedAt:    startedAt,
		Input:        map[string]interface{}{"k": "v"},
		Steps: []api.WorkflowExecutionStep{
			{StepID: "step-1", Tool: "x_echo_echo", Status: api.WorkflowExecutionCompleted},
		},
	}
}

func TestK8sExecutionStorageStoreAndGet(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	s := newTestK8sStorage(t, now)
	ctx := context.Background()

	exec := sampleExecution("exec-1", "alpha", api.WorkflowExecutionCompleted, now)
	require.NoError(t, s.Store(ctx, exec))

	got, err := s.Get(ctx, "exec-1")
	require.NoError(t, err)
	require.Equal(t, "exec-1", got.ExecutionID)
	require.Equal(t, "alpha", got.WorkflowName)
	require.Equal(t, api.WorkflowExecutionCompleted, got.Status)
	require.Len(t, got.Steps, 1)
	require.Equal(t, "step-1", got.Steps[0].StepID)
}

func TestK8sExecutionStorageStoreIsUpsert(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	s := newTestK8sStorage(t, now)
	ctx := context.Background()

	// Initial in-progress record, then a final completed record under the same
	// execution ID — TrackExecution stores twice, so Store must upsert.
	require.NoError(t, s.Store(ctx, sampleExecution("exec-1", "alpha", api.WorkflowExecutionInProgress, now)))
	require.NoError(t, s.Store(ctx, sampleExecution("exec-1", "alpha", api.WorkflowExecutionCompleted, now)))

	got, err := s.Get(ctx, "exec-1")
	require.NoError(t, err)
	require.Equal(t, api.WorkflowExecutionCompleted, got.Status)
}

func TestK8sExecutionStorageGetNotFound(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	s := newTestK8sStorage(t, now)

	_, err := s.Get(context.Background(), "missing")
	require.Error(t, err)
}

func TestK8sExecutionStorageDelete(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	s := newTestK8sStorage(t, now)
	ctx := context.Background()

	require.NoError(t, s.Store(ctx, sampleExecution("exec-1", "alpha", api.WorkflowExecutionCompleted, now)))
	require.NoError(t, s.Delete(ctx, "exec-1"))

	_, err := s.Get(ctx, "exec-1")
	require.Error(t, err)
}

func TestK8sExecutionStorageListFilters(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	s := newTestK8sStorage(t, now)
	ctx := context.Background()

	// Two alpha runs (newest first by StartedAt) and one beta run.
	require.NoError(t, s.Store(ctx, sampleExecution("alpha-old", "alpha", api.WorkflowExecutionFailed, now.Add(-2*time.Hour))))
	require.NoError(t, s.Store(ctx, sampleExecution("alpha-new", "alpha", api.WorkflowExecutionCompleted, now.Add(-1*time.Hour))))
	require.NoError(t, s.Store(ctx, sampleExecution("beta-1", "beta", api.WorkflowExecutionCompleted, now)))

	// Filter by workflow name.
	resp, err := s.List(ctx, &api.ListWorkflowExecutionsRequest{WorkflowName: "alpha"})
	require.NoError(t, err)
	require.Equal(t, 2, resp.Total)
	require.Len(t, resp.Executions, 2)
	// Sorted by StartedAt descending (most recent first).
	require.Equal(t, "alpha-new", resp.Executions[0].ExecutionID)
	require.Equal(t, "alpha-old", resp.Executions[1].ExecutionID)

	// Filter by status.
	resp, err = s.List(ctx, &api.ListWorkflowExecutionsRequest{Status: api.WorkflowExecutionCompleted})
	require.NoError(t, err)
	require.Equal(t, 2, resp.Total)
	for _, e := range resp.Executions {
		require.Equal(t, api.WorkflowExecutionCompleted, e.Status)
	}

	// Combined filter.
	resp, err = s.List(ctx, &api.ListWorkflowExecutionsRequest{WorkflowName: "alpha", Status: api.WorkflowExecutionFailed})
	require.NoError(t, err)
	require.Equal(t, 1, resp.Total)
	require.Equal(t, "alpha-old", resp.Executions[0].ExecutionID)
}

func TestK8sExecutionStorageListPagination(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	s := newTestK8sStorage(t, now)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		id := time.Duration(i)
		require.NoError(t, s.Store(ctx, sampleExecution(
			"exec-"+string(rune('a'+i)), "alpha", api.WorkflowExecutionCompleted, now.Add(-id*time.Minute))))
	}

	resp, err := s.List(ctx, &api.ListWorkflowExecutionsRequest{WorkflowName: "alpha", Limit: 2, Offset: 0})
	require.NoError(t, err)
	require.Equal(t, 5, resp.Total)
	require.Len(t, resp.Executions, 2)
	require.True(t, resp.HasMore)

	resp, err = s.List(ctx, &api.ListWorkflowExecutionsRequest{WorkflowName: "alpha", Limit: 2, Offset: 4})
	require.NoError(t, err)
	require.Len(t, resp.Executions, 1)
	require.False(t, resp.HasMore)
}

func TestK8sExecutionStoragePruneByAge(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	s := newTestK8sStorage(t, now)
	ctx := context.Background()

	require.NoError(t, s.Store(ctx, sampleExecution("fresh", "alpha", api.WorkflowExecutionCompleted, now.Add(-1*time.Hour))))
	require.NoError(t, s.Store(ctx, sampleExecution("stale", "alpha", api.WorkflowExecutionCompleted, now.Add(-48*time.Hour))))

	deleted, err := s.Prune(ctx, RetentionPolicy{MaxAge: 24 * time.Hour})
	require.NoError(t, err)
	require.Equal(t, 1, deleted)

	_, err = s.Get(ctx, "stale")
	require.Error(t, err)
	_, err = s.Get(ctx, "fresh")
	require.NoError(t, err)
}

func TestK8sExecutionStoragePruneByMaxCount(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	s := newTestK8sStorage(t, now)
	ctx := context.Background()

	// Five recent records; keep only the newest two.
	for i := 0; i < 5; i++ {
		require.NoError(t, s.Store(ctx, sampleExecution(
			"exec-"+string(rune('a'+i)), "alpha", api.WorkflowExecutionCompleted, now.Add(-time.Duration(i)*time.Minute))))
	}

	deleted, err := s.Prune(ctx, RetentionPolicy{MaxCount: 2})
	require.NoError(t, err)
	require.Equal(t, 3, deleted)

	resp, err := s.List(ctx, &api.ListWorkflowExecutionsRequest{Limit: 50})
	require.NoError(t, err)
	require.Equal(t, 2, resp.Total)
	// The two newest survive.
	require.Equal(t, "exec-a", resp.Executions[0].ExecutionID)
	require.Equal(t, "exec-b", resp.Executions[1].ExecutionID)
}

// Compile-time assertion that the Kubernetes backend satisfies the storage
// interface (which Prune is added to in Phase 3).
var _ ExecutionStorage = (*k8sExecutionStorage)(nil)
