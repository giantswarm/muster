package reconciler

import (
	"context"
	"errors"
	"strings"
	"testing"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
)

// credentialReconcilerFixture wires a Kubernetes-mode reconciler, muster in
// namespace "muster", around one remote definition in namespace.
func credentialReconcilerFixture(t *testing.T, namespace string, info *api.MCPServerInfo) (*MCPServerReconciler, *MockOrchestratorAPI, *MockServiceRegistry, *MockStatusUpdater) {
	t.Helper()

	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()
	statusUpdater := NewMockStatusUpdater()
	statusUpdater.KubernetesMode = true

	crd := &musterv1alpha1.MCPServer{}
	crd.Name = info.Name
	crd.Namespace = namespace
	crd.Spec.Type = info.Type
	crd.Spec.URL = info.URL
	statusUpdater.AddMCPServer(crd)

	info.Namespace = namespace
	info.Key = api.MCPServerKey(namespace, info.Name, "muster")
	info.AutoStart = true
	mgr.AddMCPServer(info)

	r := NewMCPServerReconciler(orchAPI, mgr, registry).WithStatusUpdater(statusUpdater, "muster")
	return r, orchAPI, registry, statusUpdater
}

// TestReconcileRefusesACredentialInTheDefinition: a server written before the
// CRD's CEL rule shipped, with a credential in its definition, is never
// started, is torn down when it runs, and reads Failed with the reason.
func TestReconcileRefusesACredentialInTheDefinition(t *testing.T) {
	remote := func() *api.MCPServerInfo {
		return &api.MCPServerInfo{Name: "github", Type: "streamable-http", URL: "https://api.githubcopilot.com/mcp/"}
	}
	tests := []struct {
		name   string
		mutate func(*api.MCPServerInfo)
		reason string
	}{
		{name: "Authorization header", mutate: func(i *api.MCPServerInfo) { i.Headers = map[string]string{"Authorization": "Bearer t"} }, reason: "header Authorization holds a credential"},
		{name: "user info in url", mutate: func(i *api.MCPServerInfo) { i.URL = "https://u:p@api.githubcopilot.com/mcp/" }, reason: "user name or password"},
		{name: "env on a remote server", mutate: func(i *api.MCPServerInfo) { i.Env = map[string]string{"TOKEN": "t"} }, reason: "env is only allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := remote()
			tt.mutate(info)
			r, orchAPI, _, statusUpdater := credentialReconcilerFixture(t, "muster", info)

			result := r.Reconcile(context.Background(), ReconcileRequest{Type: ResourceTypeMCPServer, Name: "github", Namespace: "muster", Attempt: 1})

			if !errors.Is(result.Error, api.ErrCredentialInDefinition) {
				t.Fatalf("unexpected error: %v", result.Error)
			}
			if orchAPI.StartedServices["github"] {
				t.Fatal("a refused definition must not be started")
			}
			synced := statusUpdater.LastUpdatedMCPServer
			if synced == nil || synced.Status.State != musterv1alpha1.MCPServerStateFailed {
				t.Fatalf("expected status Failed, got %+v", synced)
			}
			if !strings.Contains(synced.Status.LastError, tt.reason) {
				t.Errorf("status.lastError %q does not mention %q", synced.Status.LastError, tt.reason)
			}
		})
	}
}

// TestReconcileTearsDownARunningServerWithACredentialInItsDefinition covers
// the upgrade: a server that ran before this check does not keep running.
func TestReconcileTearsDownARunningServerWithACredentialInItsDefinition(t *testing.T) {
	info := &api.MCPServerInfo{Name: "github", Type: "streamable-http", URL: "https://api.githubcopilot.com/mcp/", Headers: map[string]string{"X-API-Key": "k"}}
	r, orchAPI, registry, _ := credentialReconcilerFixture(t, "muster", info)
	registry.AddService("github", &MockServiceInfo{Name: "github", ServiceType: api.TypeMCPServer, State: api.StateRunning, Health: api.HealthHealthy})

	result := r.Reconcile(context.Background(), ReconcileRequest{Type: ResourceTypeMCPServer, Name: "github", Namespace: "muster", Attempt: 1})

	if !errors.Is(result.Error, api.ErrCredentialInDefinition) {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if !orchAPI.RemovedServices["github"] {
		t.Fatal("expected the running service to be removed")
	}
	if orchAPI.RestartedServices["github"] {
		t.Fatal("a refused definition must not be restarted")
	}
}

// TestReconcileRefusesATenantSecretOutsideItsNamespace: a server outside
// muster's namespace whose authorization-server pin names a Secret in
// muster's namespace is refused before muster reads that Secret; the same
// reference from a server in muster's namespace is started.
func TestReconcileRefusesATenantSecretOutsideItsNamespace(t *testing.T) {
	pinned := func() *api.MCPServerInfo {
		return &api.MCPServerInfo{
			Name: "github",
			Type: "streamable-http",
			URL:  "https://api.githubcopilot.com/mcp/",
			Auth: &api.MCPServerAuth{
				Type: api.MCPServerAuthTypeOAuth,
				AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
					Issuer:                     "https://github.com/login/oauth",
					ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{Name: "github-app", Namespace: "muster"},
				},
			},
		}
	}

	r, orchAPI, _, statusUpdater := credentialReconcilerFixture(t, "org-acme", pinned())
	result := r.Reconcile(context.Background(), ReconcileRequest{Type: ResourceTypeMCPServer, Name: "github", Namespace: "org-acme", Attempt: 1})
	if !errors.Is(result.Error, api.ErrSecretOutsideNamespace) {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if orchAPI.StartedServices["github"] {
		t.Fatal("a refused definition must not be started")
	}
	if synced := statusUpdater.LastUpdatedMCPServer; synced == nil || synced.Status.State != musterv1alpha1.MCPServerStateFailed {
		t.Fatalf("expected status Failed, got %+v", synced)
	}

	r, orchAPI, _, _ = credentialReconcilerFixture(t, "muster", pinned())
	result = r.Reconcile(context.Background(), ReconcileRequest{Type: ResourceTypeMCPServer, Name: "github", Namespace: "muster", Attempt: 1})
	if result.Error != nil {
		t.Fatalf("a server in muster's namespace keeps its reference: %v", result.Error)
	}
	if !orchAPI.StartedServices["github"] {
		t.Fatal("expected the server in muster's namespace to be started")
	}
}
