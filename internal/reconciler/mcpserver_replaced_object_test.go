package reconciler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// An MCPServer deleted and created again under the same key, whose delete
// the reconciler never saw on its own (the events merged in the queue, or a
// watch gap), is a new object: its old service is removed -- which forgets
// the old object's sign-ins -- and the new one is started, even with the
// same spec, and before a suspension or a resume touches the old service.
// The same object with the same spec is left alone.
func TestMCPServerReconciler_ANewObjectUnderTheSameKeyReplacesTheService(t *testing.T) {
	for _, tc := range []struct {
		name       string
		runningUID string
		suspended  bool
		replaced   bool
	}{
		{name: "new UID", runningUID: "uid-1", replaced: true},
		{name: "new UID, suspended", runningUID: "uid-1", suspended: true, replaced: true},
		{name: "same UID", runningUID: "uid-2"},
		{name: "no UID (filesystem mode)", runningUID: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewMockMCPServerManager()
			orchAPI := NewMockOrchestratorAPI()
			registry := NewMockServiceRegistry()
			data := map[string]interface{}{}
			if tc.runningUID != "" {
				data[api.ServiceDataUID] = tc.runningUID
			}
			registry.AddService("fixture", &MockServiceInfo{
				Name: "fixture", ServiceType: api.TypeMCPServer, State: api.StateRunning, Health: api.HealthHealthy,
				ServiceData: data,
			})
			uid := "uid-2"
			if tc.runningUID == "" {
				uid = ""
			}
			mgr.AddMCPServer(&api.MCPServerInfo{Name: "fixture", Type: "streamable-http", URL: "http://127.0.0.1:1/mcp", AutoStart: true, UID: uid, Suspended: tc.suspended})

			result := NewMCPServerReconciler(orchAPI, mgr, registry).Reconcile(context.Background(),
				ReconcileRequest{Type: ResourceTypeMCPServer, Name: "fixture", Attempt: 1})
			require.NoError(t, result.Error)

			assert.Equal(t, tc.replaced, orchAPI.RemovedServices["fixture"], "the old object's service is removed")
			assert.Equal(t, tc.replaced && !tc.suspended, orchAPI.StartedServices["fixture"], "the new object's service is started unless suspended")
		})
	}
}
