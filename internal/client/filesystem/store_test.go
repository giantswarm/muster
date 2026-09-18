package filesystem

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
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
	// Nor may the deleted definition get a status file of its own.
	statusPath, err := mcpServerMeta.statusPath(c.basePath, "doomed")
	if err != nil {
		t.Fatalf("statusPath: %v", err)
	}
	if _, err := os.Stat(statusPath); !os.IsNotExist(err) {
		t.Fatal("status update recorded a status for the deleted definition")
	}
}

// TestUpdateMCPServerStatus_LeavesTheDefinitionFileAlone pins the fix for
// #1288: a status sync must not rewrite the definition file at all. It used to
// read the file, apply the status and rename its copy over the original -- and
// a spec written by another process between that read and that rename (the
// operator's editor, a GitOps sync, the test harness switching an MCPServer to
// a pinned authorization server) was overwritten with the stale spec. The
// reconciler then saw the old definition and the change never happened.
//
// The status write here is the whole window: before it, another process
// writes the definition file directly; after it, the file must hold exactly
// those bytes, and a read must show the new spec with the recorded status.
func TestUpdateMCPServerStatus_LeavesTheDefinitionFileAlone(t *testing.T) {
	ctx := context.Background()
	c := New(t.TempDir())

	if err := c.CreateMCPServer(ctx, newTestServer("edited")); err != nil {
		t.Fatalf("create: %v", err)
	}
	// The status syncer's snapshot, taken before the edit.
	snapshot, err := c.GetMCPServer(ctx, "edited", "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	snapshot.Status.State = musterv1alpha1.MCPServerStateConnected

	// Another process edits the definition file directly.
	definitionPath, err := mcpServerMeta.filePath(c.basePath, "edited")
	if err != nil {
		t.Fatalf("filePath: %v", err)
	}
	edited := []byte("apiVersion: muster.giantswarm.io/v1alpha1\nkind: MCPServer\nmetadata:\n  name: edited\nspec:\n  type: streamable-http\n  url: http://localhost:2/mcp\n  auth:\n    type: oauth\n    authorizationServer:\n      issuer: https://issuer.example\n")
	if err := os.WriteFile(definitionPath, edited, 0o600); err != nil {
		t.Fatalf("external write: %v", err)
	}

	if err := c.UpdateMCPServerStatus(ctx, snapshot); err != nil {
		t.Fatalf("status update: %v", err)
	}

	onDisk, err := os.ReadFile(definitionPath)
	if err != nil {
		t.Fatalf("read definition: %v", err)
	}
	if !bytes.Equal(onDisk, edited) {
		t.Errorf("status update rewrote the definition file:\n%s", onDisk)
	}
	final, err := c.GetMCPServer(ctx, "edited", "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if final.Spec.URL != "http://localhost:2/mcp" || final.Spec.Auth == nil || final.Spec.Auth.AuthorizationServer == nil {
		t.Errorf("the edit did not survive the status update: %+v", final.Spec)
	}
	if final.Status.State != musterv1alpha1.MCPServerStateConnected {
		t.Errorf("status not recorded: state=%s", final.Status.State)
	}
}

// TestDefinitionFile_NeverCarriesStatus: create and update write the
// definition alone, whatever status the caller's object holds; the status is
// read from the status file; a status block found in the definition file (a
// file written by muster before status had its own directory) is not read.
func TestDefinitionFile_NeverCarriesStatus(t *testing.T) {
	ctx := context.Background()
	c := New(t.TempDir())

	server := newTestServer("plain")
	server.Status.State = musterv1alpha1.MCPServerStateConnected
	if err := c.CreateMCPServer(ctx, server); err != nil {
		t.Fatalf("create: %v", err)
	}
	definitionPath, err := mcpServerMeta.filePath(c.basePath, "plain")
	if err != nil {
		t.Fatalf("filePath: %v", err)
	}
	assertNoStatusIn := func(step string) {
		t.Helper()
		data, err := os.ReadFile(definitionPath)
		if err != nil {
			t.Fatalf("%s: read definition: %v", step, err)
		}
		if strings.Contains(string(data), "status:") {
			t.Errorf("%s wrote a status block into the definition file:\n%s", step, data)
		}
	}
	assertNoStatusIn("create")

	got, err := c.GetMCPServer(ctx, "plain", "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.State != "" {
		t.Errorf("create recorded a status: state=%s", got.Status.State)
	}

	got.Spec.URL = "http://localhost:3/mcp"
	got.Status.State = musterv1alpha1.MCPServerStateConnected
	if err := c.UpdateMCPServer(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	assertNoStatusIn("update")

	if err := c.UpdateMCPServerStatus(ctx, got); err != nil {
		t.Fatalf("status update: %v", err)
	}
	assertNoStatusIn("status update")
	got, err = c.GetMCPServer(ctx, "plain", "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.State != musterv1alpha1.MCPServerStateConnected || got.Spec.URL != "http://localhost:3/mcp" {
		t.Errorf("read does not combine the definition with its recorded status: %+v %+v", got.Spec, got.Status)
	}

	// A status block in the definition file itself is ignored.
	legacy := []byte("apiVersion: muster.giantswarm.io/v1alpha1\nkind: MCPServer\nmetadata:\n  name: legacy\nspec:\n  type: streamable-http\n  url: http://localhost:4/mcp\nstatus:\n  state: Connected\n  lastError: stale\n")
	legacyPath, err := mcpServerMeta.filePath(c.basePath, "legacy")
	if err != nil {
		t.Fatalf("filePath: %v", err)
	}
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	fromLegacy, err := c.GetMCPServer(ctx, "legacy", "default")
	if err != nil {
		t.Fatalf("get legacy: %v", err)
	}
	if fromLegacy.Status.State != "" || fromLegacy.Status.LastError != "" {
		t.Errorf("status embedded in the definition file was read: %+v", fromLegacy.Status)
	}
	listed, err := c.ListMCPServers(ctx, "default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, item := range listed {
		switch item.Name {
		case "plain":
			if item.Status.State != musterv1alpha1.MCPServerStateConnected {
				t.Errorf("list does not carry the recorded status: %+v", item.Status)
			}
		case "legacy":
			if item.Status.State != "" {
				t.Errorf("list read the status embedded in the legacy file: %+v", item.Status)
			}
		}
	}
}

// TestDeleteMCPServer_RemovesTheStatusFile: a deleted definition takes its
// recorded status with it.
func TestDeleteMCPServer_RemovesTheStatusFile(t *testing.T) {
	ctx := context.Background()
	c := New(t.TempDir())

	server := newTestServer("gone")
	if err := c.CreateMCPServer(ctx, server); err != nil {
		t.Fatalf("create: %v", err)
	}
	server.Status.State = musterv1alpha1.MCPServerStateConnected
	if err := c.UpdateMCPServerStatus(ctx, server); err != nil {
		t.Fatalf("status update: %v", err)
	}
	statusPath, err := mcpServerMeta.statusPath(c.basePath, "gone")
	if err != nil {
		t.Fatalf("statusPath: %v", err)
	}
	if _, err := os.Stat(statusPath); err != nil {
		t.Fatalf("status file not written: %v", err)
	}
	if err := c.DeleteMCPServer(ctx, "gone", "default"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(statusPath); !os.IsNotExist(err) {
		t.Fatal("delete left the status file behind")
	}
}

// TestUpdateWorkflowStatus_RoundTrip covers the second kind the store holds.
func TestUpdateWorkflowStatus_RoundTrip(t *testing.T) {
	ctx := context.Background()
	c := New(t.TempDir())

	w := &musterv1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "default"},
		Spec:       musterv1alpha1.WorkflowSpec{Steps: []musterv1alpha1.WorkflowStep{{ID: "one", Tool: "core_service_list"}}},
	}
	if err := c.CreateWorkflow(ctx, w); err != nil {
		t.Fatalf("create: %v", err)
	}
	w.Status.Conditions = []metav1.Condition{{Type: "Available", Status: metav1.ConditionTrue, Reason: "Loaded", LastTransitionTime: metav1.Now()}}
	if err := c.UpdateWorkflowStatus(ctx, w); err != nil {
		t.Fatalf("status update: %v", err)
	}
	got, err := c.GetWorkflow(ctx, "wf", "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Status.Conditions) != 1 || got.Status.Conditions[0].Type != "Available" {
		t.Errorf("workflow status not recorded: %+v", got.Status)
	}
	definitionPath, err := workflowMeta.filePath(c.basePath, "wf")
	if err != nil {
		t.Fatalf("filePath: %v", err)
	}
	data, err := os.ReadFile(definitionPath)
	if err != nil {
		t.Fatalf("read definition: %v", err)
	}
	if strings.Contains(string(data), "status:") {
		t.Errorf("workflow status written into the definition file:\n%s", data)
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
