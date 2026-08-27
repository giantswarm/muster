package reconciler

import (
	"context"
	"errors"
	"strings"
	"testing"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
)

// stdioReconcilerFixture wires a reconciler around a single stdio definition,
// with the status updater reporting the given mode.
func stdioReconcilerFixture(t *testing.T, kubernetesMode bool) (*MCPServerReconciler, *MockOrchestratorAPI, *MockServiceRegistry, *MockStatusUpdater) {
	t.Helper()

	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()
	statusUpdater := NewMockStatusUpdater()
	statusUpdater.KubernetesMode = kubernetesMode

	crd := &musterv1alpha1.MCPServer{}
	crd.Name = "stdio-server"
	crd.Namespace = "default"
	crd.Spec.Type = "stdio"
	crd.Spec.Command = "echo"
	statusUpdater.AddMCPServer(crd)

	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "stdio-server",
		Type:      "stdio",
		Command:   "echo",
		AutoStart: true,
	})

	r := NewMCPServerReconciler(orchAPI, mgr, registry).WithStatusUpdater(statusUpdater, "default")
	return r, orchAPI, registry, statusUpdater
}

func stdioReconcileRequest() ReconcileRequest {
	return ReconcileRequest{
		Type:      ResourceTypeMCPServer,
		Name:      "stdio-server",
		Namespace: "default",
		Attempt:   1,
	}
}

// TestReconcileStdio_KubernetesModeFailsInsteadOfSpawning is the pre-existing-CR
// half of issue #1067: a stdio CR written straight through the apiserver never
// reaches StartService, and its status says why.
func TestReconcileStdio_KubernetesModeFailsInsteadOfSpawning(t *testing.T) {
	r, orchAPI, _, statusUpdater := stdioReconcilerFixture(t, true)

	result := r.Reconcile(context.Background(), stdioReconcileRequest())

	if result.Error == nil {
		t.Fatal("expected the reconcile to fail for a stdio definition in Kubernetes mode")
	}
	if !errors.Is(result.Error, api.ErrStdioNotAllowedInKubernetesMode) {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if orchAPI.StartedServices["stdio-server"] {
		t.Fatal("a refused stdio definition must not be started")
	}

	synced := statusUpdater.LastUpdatedMCPServer
	if synced == nil {
		t.Fatal("expected the refusal to be synced to the CRD status")
	}
	if synced.Status.State != musterv1alpha1.MCPServerStateFailed {
		t.Errorf("expected state Failed, got %q", synced.Status.State)
	}
	for _, want := range []string{"not supported in Kubernetes mode", "streamable-http"} {
		if !strings.Contains(synced.Status.LastError, want) {
			t.Errorf("status.lastError %q does not mention %q", synced.Status.LastError, want)
		}
	}
}

// TestReconcileStdio_KubernetesModeTearsDownAnExistingService covers the
// upgrade case: a subprocess started by an older muster does not survive the
// reconcile that now refuses its definition.
func TestReconcileStdio_KubernetesModeTearsDownAnExistingService(t *testing.T) {
	r, orchAPI, registry, _ := stdioReconcilerFixture(t, true)
	registry.AddService("stdio-server", &MockServiceInfo{
		Name:        "stdio-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
		Health:      api.HealthHealthy,
	})

	result := r.Reconcile(context.Background(), stdioReconcileRequest())

	if !errors.Is(result.Error, api.ErrStdioNotAllowedInKubernetesMode) {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if !orchAPI.RemovedServices["stdio-server"] {
		t.Fatal("expected the already-running stdio service to be removed")
	}
	if orchAPI.RestartedServices["stdio-server"] {
		t.Fatal("a refused stdio definition must not be restarted")
	}
}

// TestReconcileStdio_FilesystemModeStillStarts pins that the local CLI keeps
// working: same definition, same reconciler, filesystem mode.
func TestReconcileStdio_FilesystemModeStillStarts(t *testing.T) {
	r, orchAPI, _, statusUpdater := stdioReconcilerFixture(t, false)

	result := r.Reconcile(context.Background(), stdioReconcileRequest())

	if result.Error != nil {
		t.Fatalf("unexpected error in filesystem mode: %v", result.Error)
	}
	if !orchAPI.StartedServices["stdio-server"] {
		t.Fatal("expected the stdio service to be started in filesystem mode")
	}
	if synced := statusUpdater.LastUpdatedMCPServer; synced != nil && synced.Status.LastError != "" {
		t.Errorf("unexpected status.lastError: %q", synced.Status.LastError)
	}
}

// TestReconcileStdio_TransientErrorsKeepTheirState guards the status mapping:
// only a refusal reads as Failed, an ordinary start failure stays retryable.
func TestReconcileStdio_TransientErrorsKeepTheirState(t *testing.T) {
	r, _, _, _ := stdioReconcilerFixture(t, false)
	server := &musterv1alpha1.MCPServer{}
	server.Spec.Type = "stdio"

	r.applyStatusFromService(server, "stdio-server", errors.New("failed to start service: connection refused"), nil)

	if server.Status.State != musterv1alpha1.MCPServerStateStopped {
		t.Errorf("expected a transient error to stay Stopped, got %q", server.Status.State)
	}
	if server.Status.LastError == "" {
		t.Error("expected the transient error to be reported in status.lastError")
	}
}
