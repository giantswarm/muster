package mcpserver

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

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/callerwrite"
	kubernetesclient "github.com/giantswarm/muster/internal/client/kubernetes"
	"github.com/giantswarm/muster/internal/server"
)

// TestWritesAsCallerEnvtest runs the writes-as-caller path against a real
// kube-apiserver with RBAC: an allowed user (bound to an mcpserver-editor
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

	// mcpserver-editor equivalent, bound to allowed-user only (issue #1058
	// ships the chart's default binding; the Role shape is the same).
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "mcpserver-editor", Namespace: "default"},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{"muster.giantswarm.io"},
			Resources: []string{"mcpservers"},
			Verbs:     []string{"create", "update", "patch", "delete"},
		}},
	}
	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "mcpserver-editor", Namespace: "default"},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: "allowed-user"}},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "mcpserver-editor"},
	}
	if err := saClient.Create(ctx, role); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := saClient.Create(ctx, binding); err != nil {
		t.Fatalf("create rolebinding: %v", err)
	}

	adapter := NewAdapterWithClient(saClient, "default")
	adapter.EnableWritesAsCaller(callerwrite.NewKubernetesClientFactory(adminCfg), "")

	allowedCtx := server.ContextWithIDToken(ctx, allowedToken)
	deniedCtx := server.ContextWithIDToken(ctx, deniedToken)

	// Remote type: Kubernetes mode refuses stdio outright (issue #1067), so the
	// fixture has to be a definition the aggregator would actually accept.
	createArgs := func(name string) map[string]interface{} {
		return map[string]interface{}{"name": name, "type": "streamable-http", "url": "http://example.com/mcp"}
	}

	t.Run("allowed user create-update-delete", func(t *testing.T) {
		result, err := adapter.ExecuteTool(allowedCtx, "mcpserver_create", createArgs("allowed-server"))
		if err != nil || result.IsError {
			t.Fatalf("create: err=%v result=%s", err, resultText(t, result))
		}
		created, err := saClient.GetMCPServer(ctx, "allowed-server", "default")
		if err != nil {
			t.Fatalf("created CR not found: %v", err)
		}
		if created.Spec.URL != "http://example.com/mcp" {
			t.Fatalf("unexpected spec: %+v", created.Spec)
		}

		result, err = adapter.ExecuteTool(allowedCtx, "mcpserver_update",
			map[string]interface{}{"name": "allowed-server", "url": "http://example.com/mcp2"})
		if err != nil || result.IsError {
			t.Fatalf("update: err=%v result=%s", err, resultText(t, result))
		}
		updated, err := saClient.GetMCPServer(ctx, "allowed-server", "default")
		if err != nil {
			t.Fatalf("get after update: %v", err)
		}
		if updated.Spec.URL != "http://example.com/mcp2" {
			t.Fatalf("update did not land: %+v", updated.Spec)
		}

		// Muster's SA still reconciles status on the user-created CR: the
		// controllers keep their identity, only spec mutations switched.
		updated.Status.State = musterv1alpha1.MCPServerStateRunning
		if err := saClient.UpdateMCPServerStatus(ctx, updated); err != nil {
			t.Fatalf("SA status update on caller-created CR: %v", err)
		}

		result, err = adapter.ExecuteTool(allowedCtx, "mcpserver_delete",
			map[string]interface{}{"name": "allowed-server"})
		if err != nil || result.IsError {
			t.Fatalf("delete: err=%v result=%s", err, resultText(t, result))
		}
		if _, err := saClient.GetMCPServer(ctx, "allowed-server", "default"); !apierrors.IsNotFound(err) {
			t.Fatalf("CR still present after delete: %v", err)
		}
	})

	t.Run("stdio refused before any write", func(t *testing.T) {
		stdioArgs := map[string]interface{}{"name": "stdio-server", "type": "stdio", "command": "echo"}

		// validate reports the rejection, so a caller can check before writing.
		result, err := adapter.ExecuteTool(allowedCtx, "mcpserver_validate", stdioArgs)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if !result.IsError || !strings.Contains(resultText(t, result), "not supported in Kubernetes mode") {
			t.Fatalf("validate accepted stdio in Kubernetes mode: %s", resultText(t, result))
		}

		// create fails the tool call for a user who *is* allowed to write
		// mcpservers, and leaves no CR behind.
		result, err = adapter.ExecuteTool(allowedCtx, "mcpserver_create", stdioArgs)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		text := resultText(t, result)
		if !result.IsError {
			t.Fatalf("create accepted stdio in Kubernetes mode: %s", text)
		}
		for _, want := range []string{"not supported in Kubernetes mode", "streamable-http", "sse"} {
			if !strings.Contains(text, want) {
				t.Errorf("rejection %q does not mention %q", text, want)
			}
		}
		if _, err := saClient.GetMCPServer(ctx, "stdio-server", "default"); !apierrors.IsNotFound(err) {
			t.Fatalf("refused stdio create still wrote the CR: %v", err)
		}

		// An update that switches an accepted server over to stdio is refused
		// the same way — the gate is on the resulting spec, not on the request.
		if result, err := adapter.ExecuteTool(allowedCtx, "mcpserver_create", createArgs("switch-server")); err != nil || result.IsError {
			t.Fatalf("setup create: err=%v result=%s", err, resultText(t, result))
		}
		t.Cleanup(func() {
			_, _ = adapter.ExecuteTool(allowedCtx, "mcpserver_delete", map[string]interface{}{"name": "switch-server"})
		})
		result, err = adapter.ExecuteTool(allowedCtx, "mcpserver_update",
			map[string]interface{}{"name": "switch-server", "type": "stdio", "command": "echo"})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if !result.IsError || !strings.Contains(resultText(t, result), "not supported in Kubernetes mode") {
			t.Fatalf("update switched a server to stdio in Kubernetes mode: %s", resultText(t, result))
		}
		switched, err := saClient.GetMCPServer(ctx, "switch-server", "default")
		if err != nil {
			t.Fatalf("get after refused update: %v", err)
		}
		if switched.Spec.Type != "streamable-http" {
			t.Fatalf("refused update still changed the spec: %+v", switched.Spec)
		}
	})

	t.Run("denied user create", func(t *testing.T) {
		result, err := adapter.ExecuteTool(deniedCtx, "mcpserver_create", createArgs("denied-server"))
		if err != nil {
			t.Fatalf("ExecuteTool: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected permission error")
		}
		text := resultText(t, result)
		for _, want := range []string{"Permission denied", "create", "mcpservers.muster.giantswarm.io", `"default"`} {
			if !strings.Contains(text, want) {
				t.Errorf("error %q does not mention %q", text, want)
			}
		}
		if _, err := saClient.GetMCPServer(ctx, "denied-server", "default"); !apierrors.IsNotFound(err) {
			t.Fatalf("denied create still wrote the CR: %v", err)
		}
	})

	t.Run("lifecycle allowed user suspend-resume-restart", func(t *testing.T) {
		result, err := adapter.ExecuteTool(allowedCtx, "mcpserver_create", createArgs("lifecycle-server"))
		if err != nil || result.IsError {
			t.Fatalf("setup create: err=%v result=%s", err, resultText(t, result))
		}

		// Stop → spec.suspended=true, written as the caller.
		result, handled, err := adapter.StopMCPServerAsCaller(allowedCtx, "lifecycle-server")
		if err != nil || !handled || result.IsError {
			t.Fatalf("stop: handled=%v err=%v result=%s", handled, err, resultText(t, result))
		}
		after, err := saClient.GetMCPServer(ctx, "lifecycle-server", "default")
		if err != nil {
			t.Fatalf("get after stop: %v", err)
		}
		if !after.Spec.Suspended {
			t.Fatal("stop did not set spec.suspended")
		}

		// Start on the suspended server → resume: spec.suspended back to false.
		// With no service registered (down), the start also writes
		// spec.restartRequestedAt so the resume survives a lost in-memory
		// suspension marker.
		result, handled, err = adapter.StartMCPServerAsCaller(allowedCtx, "lifecycle-server")
		if err != nil || !handled || result.IsError {
			t.Fatalf("start: handled=%v err=%v result=%s", handled, err, resultText(t, result))
		}
		after, err = saClient.GetMCPServer(ctx, "lifecycle-server", "default")
		if err != nil {
			t.Fatalf("get after start: %v", err)
		}
		if after.Spec.Suspended {
			t.Fatal("start did not clear spec.suspended")
		}
		if after.Spec.RestartRequestedAt == nil {
			t.Fatal("start on a down server must also write spec.restartRequestedAt")
		}

		// Restart → spec.restartRequestedAt, written as the caller.
		result, handled, err = adapter.RestartMCPServerAsCaller(allowedCtx, "lifecycle-server")
		if err != nil || !handled || result.IsError {
			t.Fatalf("restart: handled=%v err=%v result=%s", handled, err, resultText(t, result))
		}
		after, err = saClient.GetMCPServer(ctx, "lifecycle-server", "default")
		if err != nil {
			t.Fatalf("get after restart: %v", err)
		}
		if after.Spec.RestartRequestedAt == nil {
			t.Fatal("restart did not write spec.restartRequestedAt")
		}
	})

	t.Run("lifecycle denied user suspend-resume-restart", func(t *testing.T) {
		lifecycle := func(action string) (*api.CallToolResult, bool, error) {
			switch action {
			case "start":
				return adapter.StartMCPServerAsCaller(deniedCtx, "lifecycle-server")
			case "stop":
				return adapter.StopMCPServerAsCaller(deniedCtx, "lifecycle-server")
			default:
				return adapter.RestartMCPServerAsCaller(deniedCtx, "lifecycle-server")
			}
		}
		before, err := saClient.GetMCPServer(ctx, "lifecycle-server", "default")
		if err != nil {
			t.Fatalf("get before: %v", err)
		}
		for _, action := range []string{"stop", "restart", "start"} {
			result, handled, err := lifecycle(action)
			if err != nil {
				t.Fatalf("%s: %v", action, err)
			}
			if !handled || !result.IsError || !strings.Contains(resultText(t, result), "Permission denied") {
				t.Fatalf("%s: expected permission error, got handled=%v: %s", action, handled, resultText(t, result))
			}
		}
		after, err := saClient.GetMCPServer(ctx, "lifecycle-server", "default")
		if err != nil {
			t.Fatalf("get after: %v", err)
		}
		if after.Spec.Suspended != before.Spec.Suspended ||
			!after.Spec.RestartRequestedAt.Equal(before.Spec.RestartRequestedAt) ||
			after.ResourceVersion != before.ResourceVersion {
			t.Fatal("denied lifecycle action still changed the CR")
		}
	})

	t.Run("denied user update and delete", func(t *testing.T) {
		// A CR the denied user did not create and may not touch.
		result, err := adapter.ExecuteTool(allowedCtx, "mcpserver_create", createArgs("victim-server"))
		if err != nil || result.IsError {
			t.Fatalf("setup create: err=%v result=%s", err, resultText(t, result))
		}

		result, err = adapter.ExecuteTool(deniedCtx, "mcpserver_update",
			map[string]interface{}{"name": "victim-server", "url": "http://evil.example.com/mcp"})
		if err != nil {
			t.Fatalf("ExecuteTool: %v", err)
		}
		if !result.IsError || !strings.Contains(resultText(t, result), "Permission denied") {
			t.Fatalf("expected permission error on update, got: %s", resultText(t, result))
		}
		after, err := saClient.GetMCPServer(ctx, "victim-server", "default")
		if err != nil {
			t.Fatalf("get victim: %v", err)
		}
		if after.Spec.URL != "http://example.com/mcp" {
			t.Fatalf("denied update still changed the spec: %+v", after.Spec)
		}

		result, err = adapter.ExecuteTool(deniedCtx, "mcpserver_delete",
			map[string]interface{}{"name": "victim-server"})
		if err != nil {
			t.Fatalf("ExecuteTool: %v", err)
		}
		if !result.IsError || !strings.Contains(resultText(t, result), "Permission denied") {
			t.Fatalf("expected permission error on delete, got: %s", resultText(t, result))
		}
		if _, err := saClient.GetMCPServer(ctx, "victim-server", "default"); err != nil {
			t.Fatalf("denied delete still removed the CR: %v", err)
		}
	})
}
