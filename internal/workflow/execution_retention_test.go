package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

// These tests define the retention contract for the filesystem ExecutionStorage
// backend (Phase 3 of #930): an injectable clock drives age-based pruning so the
// retention GC is deterministic and needs no time.Sleep. They are written
// test-first and expect Prune / the now clock field to be added to
// ExecutionStorageImpl and the ExecutionStorage interface.

// newTestFsStorage builds a filesystem-backed storage rooted at a temp dir with
// the clock pinned to now for deterministic retention tests.
func newTestFsStorage(t *testing.T, now time.Time) *ExecutionStorageImpl {
	t.Helper()
	storage, ok := NewExecutionStorage(t.TempDir()).(*ExecutionStorageImpl)
	require.True(t, ok, "NewExecutionStorage must return *ExecutionStorageImpl")
	storage.now = func() time.Time { return now }
	return storage
}

func TestFsExecutionStoragePruneByAge(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	s := newTestFsStorage(t, now)
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

func TestFsExecutionStoragePruneByMaxCount(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	s := newTestFsStorage(t, now)
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		require.NoError(t, s.Store(ctx, sampleExecution(
			"exec-"+string(rune('a'+i)), "alpha", api.WorkflowExecutionCompleted, now.Add(-time.Duration(i)*time.Minute))))
	}

	deleted, err := s.Prune(ctx, RetentionPolicy{MaxCount: 2})
	require.NoError(t, err)
	require.Equal(t, 2, deleted)

	resp, err := s.List(ctx, &api.ListWorkflowExecutionsRequest{Limit: 50})
	require.NoError(t, err)
	require.Equal(t, 2, resp.Total)
}

func TestFsExecutionStoragePruneNoPolicyKeepsAll(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	s := newTestFsStorage(t, now)
	ctx := context.Background()

	require.NoError(t, s.Store(ctx, sampleExecution("a", "alpha", api.WorkflowExecutionCompleted, now.Add(-1000*time.Hour))))

	// An empty policy (no age, no count cap) must be a no-op rather than
	// deleting everything.
	deleted, err := s.Prune(ctx, RetentionPolicy{})
	require.NoError(t, err)
	require.Equal(t, 0, deleted)

	_, err = s.Get(ctx, "a")
	require.NoError(t, err)
}
