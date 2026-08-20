package filesystem

import (
	"context"
	"os"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

func newTestServer(name string) *musterv1alpha1.MCPServer {
	return &musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       musterv1alpha1.MCPServerSpec{Type: "streamable-http", URL: "http://localhost:1/mcp"},
	}
}

// TestUpdateMCPServerStatus_DoesNotResurrectDeleted pins the fix for the
// delete-recreate CI flake: a status sync that read the definition before a
// concurrent delete must NOT write the file back into existence. A
// resurrected definition reconciles as "existing and up to date", so the
// service teardown never happens.
func TestUpdateMCPServerStatus_DoesNotResurrectDeleted(t *testing.T) {
	ctx := context.Background()
	c := New(t.TempDir())

	server := newTestServer("doomed")
	if err := c.CreateMCPServer(ctx, server); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Status syncer reads the definition (pre-delete snapshot)...
	stale, err := c.GetMCPServer(ctx, "doomed", "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stale.Status.State = musterv1alpha1.MCPServerStateConnected

	// ...the definition is deleted concurrently...
	if err := c.DeleteMCPServer(ctx, "doomed", "default"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// ...and the syncer's status write must fail NotFound, not resurrect.
	if err := c.UpdateMCPServerStatus(ctx, stale); !errors.IsNotFound(err) {
		t.Fatalf("expected NotFound from status update after delete, got %v", err)
	}
	if _, err := os.Stat(mcpServerMeta.filePath(c.basePath, "doomed")); !os.IsNotExist(err) {
		t.Fatal("status update resurrected the deleted definition file")
	}
}

// TestUpdateMCPServerStatus_PreservesConcurrentSpec verifies a status write
// applies only status onto the current on-disk spec instead of clobbering it
// with the (stale) spec the status writer read earlier.
func TestUpdateMCPServerStatus_PreservesConcurrentSpec(t *testing.T) {
	ctx := context.Background()
	c := New(t.TempDir())

	if err := c.CreateMCPServer(ctx, newTestServer("shared")); err != nil {
		t.Fatalf("create: %v", err)
	}

	stale, err := c.GetMCPServer(ctx, "shared", "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stale.Status.State = musterv1alpha1.MCPServerStateConnected

	// Concurrent spec update lands after the status writer's read.
	updated, err := c.GetMCPServer(ctx, "shared", "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	updated.Spec.URL = "http://localhost:2/mcp"
	if err := c.UpdateMCPServer(ctx, updated); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := c.UpdateMCPServerStatus(ctx, stale); err != nil {
		t.Fatalf("status update: %v", err)
	}

	final, err := c.GetMCPServer(ctx, "shared", "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if final.Spec.URL != "http://localhost:2/mcp" {
		t.Errorf("status write clobbered concurrent spec update: url=%s", final.Spec.URL)
	}
	if final.Status.State != musterv1alpha1.MCPServerStateConnected {
		t.Errorf("status not applied: state=%s", final.Status.State)
	}
}
