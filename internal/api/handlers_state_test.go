package api

import (
	"reflect"
	"testing"
)

// updatableMockService is a registry entry that accepts external state
// updates, the way an MCPServer service does for the aggregator's
// session-level notifications.
type updatableMockService struct {
	mockServiceInfo
	updates []ServiceState
}

func (m *updatableMockService) UpdateState(state ServiceState, health HealthStatus, err error) {
	m.updates = append(m.updates, state)
	m.state = state
	m.health = health
	m.lastErr = err
}

func registerUpdatableService(t *testing.T, state ServiceState) *updatableMockService {
	t.Helper()
	svc := &updatableMockService{mockServiceInfo: mockServiceInfo{
		name:    "svc",
		svcType: TypeMCPServer,
		state:   state,
		health:  HealthUnknown,
	}}
	registry := newMockServiceRegistryHandler()
	registry.addService(svc)
	RegisterServiceRegistry(registry)
	t.Cleanup(func() { RegisterServiceRegistry(nil) })
	return svc
}

// TestUpdateMCPServerState_HandsTheStateToTheService pins what the handler
// does and does not do: it passes the state to the service, whether or not it
// differs from the current one, and nothing else. A change is the service's to
// publish (BaseService.UpdateState fires its callback only when the state
// differs) and the reconciler's StateChangeBridge to pick up; an unchanged
// state -- a session connecting to a server that is already Connected -- is
// not an event. Until issue #1290 the handler queued a reconcile after every
// call, one pass and one status write per server per session start.
func TestUpdateMCPServerState_HandsTheStateToTheService(t *testing.T) {
	for _, tc := range []struct {
		name    string
		current ServiceState
	}{
		{name: "unchanged state", current: StateConnected},
		{name: "changed state", current: StateAuthRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := registerUpdatableService(t, tc.current)

			if err := UpdateMCPServerState("svc", StateConnected, HealthHealthy, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(svc.updates) != 1 || svc.updates[0] != StateConnected {
				t.Errorf("expected exactly one UpdateState(Connected) on the service, got %v", svc.updates)
			}
			if svc.GetState() != StateConnected || svc.GetHealth() != HealthHealthy {
				t.Errorf("service should read Connected/healthy, got %s/%s", svc.GetState(), svc.GetHealth())
			}
		})
	}
}

// TestReconcileManagerHandler_HasNoManualTrigger pins that the API offers no
// way to queue a reconcile by hand. Reconciles come from a definition change,
// a service state change, or the reconciler's own resync; a caller who wants
// the CRD status to follow a state changes the state (issue #1290).
func TestReconcileManagerHandler_HasNoManualTrigger(t *testing.T) {
	handler := reflect.TypeOf((*ReconcileManagerHandler)(nil)).Elem()
	if _, found := handler.MethodByName("TriggerReconcile"); found {
		t.Fatal("ReconcileManagerHandler must not expose TriggerReconcile")
	}
}
