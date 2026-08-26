package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/giantswarm/muster/internal/callerwrite"
	kubernetesclient "github.com/giantswarm/muster/internal/client/kubernetes"
	"github.com/giantswarm/muster/internal/server"
)

// TestWritesAsCallerEnvtest runs the Workflow writes-as-caller path against a
// real kube-apiserver with RBAC: an allowed user (bound to a workflow-editor
// style Role) creates, updates, and deletes through the adapter; a denied
// user gets the mapped permission error and no write occurs; muster's own
// SA-equivalent client still reconciles status on a CR it did not create.
//
// Requires envtest binaries: run via `make test-envtest`, which resolves
// KUBEBUILDER_ASSETS with setup-envtest. Skipped otherwise.
func TestWritesAsCallerEnvtest(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run via `make test-envtest`")
	}

	// Static token authentication: bearer → user. The tokens are JWT-shaped
	// and carry the kubernetes audience so the adapter's audience precheck —
	// the same code the dex path runs — passes and the apiserver decides.
	allowedToken := testJWT(t, callerwrite.DefaultKubernetesAudience)
	deniedToken := testJWT(t, []string{callerwrite.DefaultKubernetesAudience, "muster"})
	tokenFile := filepath.Join(t.TempDir(), "tokens.csv")
	tokens := fmt.Sprintf("%s,allowed-user,1000,\n%s,denied-user,1001,\n", allowedToken, deniedToken)
	if err := os.WriteFile(tokenFile, []byte(tokens), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "helm", "muster", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	// envtest's apiserver already runs --authorization-mode=RBAC by default.
	testEnv.ControlPlane.GetAPIServer().Configure().Set("token-auth-file", tokenFile)

	adminCfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})

	// The admin config plays muster's ServiceAccount: it backs the shared
	// MusterClient (reads, status reconciliation) exactly like production.
	saClient, err := kubernetesclient.New(adminCfg)
	if err != nil {
		t.Fatalf("create kubernetes muster client: %v", err)
	}
	t.Cleanup(func() { _ = saClient.Close() })

	ctx := context.Background()

	// workflow-editor equivalent, bound to allowed-user only (the chart ships
	// the default binding; the Role shape is the same).
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "workflow-editor", Namespace: "default"},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{"muster.giantswarm.io"},
			Resources: []string{"workflows"},
			Verbs:     []string{"create", "update", "patch", "delete"},
		}},
	}
	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "workflow-editor", Namespace: "default"},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: "allowed-user"}},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "workflow-editor"},
	}
	if err := saClient.Create(ctx, role); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := saClient.Create(ctx, binding); err != nil {
		t.Fatalf("create rolebinding: %v", err)
	}

	adapter := NewAdapterWithClient(saClient, "default", nil, nil, "")
	t.Cleanup(adapter.stopGC)
	adapter.EnableWritesAsCaller(callerwrite.NewKubernetesClientFactory(adminCfg), "")

	allowedCtx := server.ContextWithIDToken(ctx, allowedToken)
	deniedCtx := server.ContextWithIDToken(ctx, deniedToken)

	// createArgs builds a minimal valid definition whose step tool identifies
	// the writing revision, so update effects are observable in the CR.
	createArgs := func(name, stepTool string) map[string]interface{} {
		return map[string]interface{}{
			"name": name,
			"steps": []interface{}{
				map[string]interface{}{"id": "s1", "tool": stepTool},
			},
		}
	}

	t.Run("allowed user create-update-delete", func(t *testing.T) {
		result, err := adapter.ExecuteTool(allowedCtx, "workflow_create", createArgs("allowed-workflow", "core_service_list"))
		if err != nil || result.IsError {
			t.Fatalf("create: err=%v result=%s", err, resultText(t, result))
		}
		created, err := saClient.GetWorkflow(ctx, "allowed-workflow", "default")
		if err != nil {
			t.Fatalf("created CR not found: %v", err)
		}
		if len(created.Spec.Steps) != 1 || created.Spec.Steps[0].Tool != "core_service_list" {
			t.Fatalf("unexpected spec: %+v", created.Spec)
		}

		result, err = adapter.ExecuteTool(allowedCtx, "workflow_update", createArgs("allowed-workflow", "core_workflow_list"))
		if err != nil || result.IsError {
			t.Fatalf("update: err=%v result=%s", err, resultText(t, result))
		}
		updated, err := saClient.GetWorkflow(ctx, "allowed-workflow", "default")
		if err != nil {
			t.Fatalf("get after update: %v", err)
		}
		if len(updated.Spec.Steps) != 1 || updated.Spec.Steps[0].Tool != "core_workflow_list" {
			t.Fatalf("update did not land: %+v", updated.Spec)
		}

		// Muster's SA still reconciles status on the user-created CR: the
		// controllers keep their identity, only spec mutations switched.
		updated.Status.Valid = true
		updated.Status.StepCount = len(updated.Spec.Steps)
		if err := saClient.UpdateWorkflowStatus(ctx, updated); err != nil {
			t.Fatalf("SA status update on caller-created CR: %v", err)
		}

		result, err = adapter.ExecuteTool(allowedCtx, "workflow_delete",
			map[string]interface{}{"name": "allowed-workflow"})
		if err != nil || result.IsError {
			t.Fatalf("delete: err=%v result=%s", err, resultText(t, result))
		}
		if _, err := saClient.GetWorkflow(ctx, "allowed-workflow", "default"); !apierrors.IsNotFound(err) {
			t.Fatalf("CR still present after delete: %v", err)
		}
	})

	t.Run("denied user create", func(t *testing.T) {
		result, err := adapter.ExecuteTool(deniedCtx, "workflow_create", createArgs("denied-workflow", "core_service_list"))
		if err != nil {
			t.Fatalf("ExecuteTool: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected permission error")
		}
		text := resultText(t, result)
		for _, want := range []string{"Permission denied", "create", "workflows.muster.giantswarm.io", `"default"`, "workflow-editor"} {
			if !strings.Contains(text, want) {
				t.Errorf("error %q does not mention %q", text, want)
			}
		}
		if _, err := saClient.GetWorkflow(ctx, "denied-workflow", "default"); !apierrors.IsNotFound(err) {
			t.Fatalf("denied create still wrote the CR: %v", err)
		}
	})

	t.Run("denied user update and delete", func(t *testing.T) {
		// A CR the denied user did not create and may not touch.
		result, err := adapter.ExecuteTool(allowedCtx, "workflow_create", createArgs("victim-workflow", "core_service_list"))
		if err != nil || result.IsError {
			t.Fatalf("setup create: err=%v result=%s", err, resultText(t, result))
		}

		result, err = adapter.ExecuteTool(deniedCtx, "workflow_update", createArgs("victim-workflow", "x_evil_tool"))
		if err != nil {
			t.Fatalf("ExecuteTool: %v", err)
		}
		if !result.IsError || !strings.Contains(resultText(t, result), "Permission denied") {
			t.Fatalf("expected permission error on update, got: %s", resultText(t, result))
		}
		after, err := saClient.GetWorkflow(ctx, "victim-workflow", "default")
		if err != nil {
			t.Fatalf("get victim: %v", err)
		}
		if len(after.Spec.Steps) != 1 || after.Spec.Steps[0].Tool != "core_service_list" {
			t.Fatalf("denied update still changed the spec: %+v", after.Spec)
		}

		result, err = adapter.ExecuteTool(deniedCtx, "workflow_delete",
			map[string]interface{}{"name": "victim-workflow"})
		if err != nil {
			t.Fatalf("ExecuteTool: %v", err)
		}
		if !result.IsError || !strings.Contains(resultText(t, result), "Permission denied") {
			t.Fatalf("expected permission error on delete, got: %s", resultText(t, result))
		}
		if _, err := saClient.GetWorkflow(ctx, "victim-workflow", "default"); err != nil {
			t.Fatalf("denied delete still removed the CR: %v", err)
		}
	})
}
