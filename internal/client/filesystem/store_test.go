package filesystem

import (
	"context"
	"os"
	"path/filepath"
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
	doomedPath, err := mcpServerMeta.filePath(c.basePath, "doomed")
	if err != nil {
		t.Fatalf("filePath: %v", err)
	}
	if _, err := os.Stat(doomedPath); !os.IsNotExist(err) {
		t.Fatal("status update resurrected the deleted definition file")
	}
}

// TestCreateMCPServer_RejectsPathTraversal pins the fix for the filesystem
// path-traversal hole: a caller-controlled name that escapes the config dir
// must be rejected before any file is written.
//
// The traversal targets point at a sibling t.TempDir() rather than /tmp or
// /etc, so a regression stays inside the directories this test owns, and each
// case asserts os.Stat on the exact path the name would resolve to — scanning
// the base dir cannot see a file that landed outside it.
func TestCreateMCPServer_RejectsPathTraversal(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	outside := t.TempDir() // sibling of base, under the same parent
	c := New(base)

	names := []string{
		// base/mcpservers/../../<sibling>/escaped.yaml resolves into outside.
		"../../" + filepath.Base(outside) + "/escaped",
		// More ".." segments than there are directories: filepath.Join clamps
		// at the root, then the absolute sibling path lands in outside.
		"../../../../../../../.." + outside + "/deep-escape",
		// Absolute name: joined relative to the store dir today, but a
		// regression that trusts it writes straight into outside.
		filepath.Join(outside, "absolute-evil"),
		"foo/bar",
		"..",
		".",
		// Not traversal, but the same choke point: a newline forges a record in
		// the line-oriented legacy events.log. Deliberately carries no
		// separator, so it can only be rejected by the control-character rule.
		"x\nforged",
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			if err := c.CreateMCPServer(ctx, newTestServer(name)); err == nil {
				t.Fatalf("expected create to reject name %q, got nil error", name)
			}
			// The path filePath would have produced for this name — inside the
			// store dir for the harmless cases, outside it for the escapes.
			target := filepath.Join(base, "mcpservers", name+".yaml")
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("name %q wrote a file at %s (stat error: %v)", name, target, err)
			}
		})
	}

	// Belt and braces: the sibling directory must still be empty.
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatalf("read sibling dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("traversal escaped into sibling dir: %v", entries)
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
