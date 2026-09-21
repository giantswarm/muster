package aggregator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/events"
)

// updatableStubService is a registry entry that accepts the aggregator's
// state notifications, the way an MCPServer service does.
type updatableStubService struct {
	stubServiceInfo
	updates []api.ServiceState
}

func (s *updatableStubService) UpdateState(state api.ServiceState, _ api.HealthStatus, _ error) {
	s.updates = append(s.updates, state)
	s.state = state
}

type updatableStubRegistry struct {
	services map[string]*updatableStubService
}

func (r *updatableStubRegistry) GetAll() []api.ServiceInfo                   { return nil }
func (r *updatableStubRegistry) GetByType(api.ServiceType) []api.ServiceInfo { return nil }
func (r *updatableStubRegistry) Get(name string) (api.ServiceInfo, bool) {
	svc, ok := r.services[name]
	return svc, ok
}

// reasonRecordingEventManager keeps the reason of every event, per server.
type reasonRecordingEventManager struct {
	reasons map[string][]string
}

func (m *reasonRecordingEventManager) CreateEventWithData(_ context.Context, ref api.ObjectReference, reason string, _ api.EventData) error {
	m.reasons[ref.Name] = append(m.reasons[ref.Name], reason)
	return nil
}
func (m *reasonRecordingEventManager) DefaultNamespace() string { return "agent-platform" }
func (m *reasonRecordingEventManager) QueryEvents(context.Context, api.EventQueryOptions) (*api.EventQueryResult, error) {
	return &api.EventQueryResult{}, nil
}
func (m *reasonRecordingEventManager) WatchEvents(context.Context, api.EventQueryOptions) (<-chan api.EventResult, error) {
	ch := make(chan api.EventResult)
	close(ch)
	return ch, nil
}
func (m *reasonRecordingEventManager) IsKubernetesMode() bool { return false }

// TestLastSessionLossSyncsTheStateTheServerWaitsIn: when the last session's
// connection to a server is lost, the server returns to the state it waits
// in -- Awaiting Session for a server served with the caller's own identity
// (forwardToken, tokenExchange), Auth Required for one a person signs in to
// through muster -- and the event names it the same way.
func TestLastSessionLossSyncsTheStateTheServerWaitsIn(t *testing.T) {
	services := &updatableStubRegistry{services: map[string]*updatableStubService{}}
	for _, name := range []string{"forwarded", "exchanged", "signed-in"} {
		services.services[name] = &updatableStubService{stubServiceInfo: stubServiceInfo{name: name, state: api.StateConnected}}
	}
	api.RegisterServiceRegistry(services)
	t.Cleanup(func() { api.RegisterServiceRegistry(nil) })

	eventsSeen := &reasonRecordingEventManager{reasons: map[string][]string{}}
	api.RegisterEventManager(eventsSeen)
	t.Cleanup(func() { api.RegisterEventManager(nil) })

	registry := NewServerRegistry("x")
	for name, auth := range map[string]*api.MCPServerAuth{
		"forwarded": {Type: "oauth", ForwardToken: true},
		"exchanged": {Type: "oauth", TokenExchange: &api.TokenExchangeConfig{Enabled: true, DexTokenEndpoint: "https://dex.remote.example.test/token", ConnectorID: "remote"}},
		"signed-in": {Type: "oauth"},
	} {
		require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: name, Namespace: "agent-platform"},
			URL:                "https://" + name + ".example.test/mcp",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.test", Scope: "openid"},
			AuthConfig:         auth,
		}))
	}
	a := &AggregatorServer{registry: registry}

	for _, name := range []string{"forwarded", "exchanged", "signed-in"} {
		a.notifyMCPServerAuthRequired(name, "token gone")
	}

	assert.Equal(t, []api.ServiceState{api.StateAwaitingSession}, services.services["forwarded"].updates)
	assert.Equal(t, []api.ServiceState{api.StateAwaitingSession}, services.services["exchanged"].updates)
	assert.Equal(t, []api.ServiceState{api.StateAuthRequired}, services.services["signed-in"].updates)

	assert.Equal(t, []string{string(events.ReasonMCPServerAwaitingSession)}, eventsSeen.reasons["forwarded"])
	assert.Equal(t, []string{string(events.ReasonMCPServerAwaitingSession)}, eventsSeen.reasons["exchanged"])
	assert.Equal(t, []string{string(events.ReasonMCPServerAuthRequired)}, eventsSeen.reasons["signed-in"])
}
