package reconciler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestManager_OneObjectIsNeverReconciledTwiceAtOnce covers the request
// sources that name an object differently: the filesystem detector and the
// periodic resync leave the namespace empty, the service state-change bridge
// sends the configured one. The queue serializes requests per key, so the two
// spellings must resolve to the same key; otherwise two workers reconcile one
// MCPServer at once and both act on the same spec.restartRequestedAt before
// either wrote status.lastRestartedAt -- the duplicate restart of
// giantswarm/muster#1153.
func TestManager_OneObjectIsNeverReconciledTwiceAtOnce(t *testing.T) {
	manager := NewManager(ManagerConfig{
		Mode:           WatchModeFilesystem,
		FilesystemPath: t.TempDir(),
		Namespace:      "default",
		WorkerCount:    2,
	})

	var inFlight, maxInFlight atomic.Int32
	started := make(chan ReconcileRequest, 4)
	release := make(chan struct{})
	releaseAll := sync.OnceFunc(func() { close(release) })
	reconciler := &mockReconciler{
		resourceType: ResourceTypeMCPServer,
		reconcileFunc: func(ctx context.Context, req ReconcileRequest) ReconcileResult {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				m := maxInFlight.Load()
				if n <= m || maxInFlight.CompareAndSwap(m, n) {
					break
				}
			}
			started <- req
			<-release
			return ReconcileResult{}
		},
	}
	if err := manager.RegisterReconciler(reconciler); err != nil {
		t.Fatalf("failed to register reconciler: %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("failed to start manager: %v", err)
	}
	defer func() { _ = manager.Stop() }()
	defer releaseAll() // runs first: a failed assertion must not leave a worker blocked in Stop

	// The filesystem detector's request, then the bridge's while the first
	// is still being reconciled.
	manager.TriggerReconcile(ResourceTypeMCPServer, "live-server", "")
	first := waitStarted(t, started)
	manager.TriggerReconcile(ResourceTypeMCPServer, "live-server", "default")

	select {
	case req := <-started:
		t.Fatalf("a second reconcile of live-server (namespace %q) started while the first (namespace %q) was in flight",
			req.Namespace, first.Namespace)
	case <-time.After(300 * time.Millisecond):
	}

	releaseAll()
	second := waitStarted(t, started)
	if got := maxInFlight.Load(); got != 1 {
		t.Errorf("live-server was reconciled %d times at once, want 1", got)
	}
	for _, req := range []ReconcileRequest{first, second} {
		if req.Namespace != "default" {
			t.Errorf("request namespace = %q, want the manager's %q", req.Namespace, "default")
		}
	}
}

func waitStarted(t *testing.T, started <-chan ReconcileRequest) ReconcileRequest {
	t.Helper()
	select {
	case req := <-started:
		return req
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for a reconcile to start")
		return ReconcileRequest{}
	}
}
