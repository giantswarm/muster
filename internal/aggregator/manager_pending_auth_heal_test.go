package aggregator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/internal/api"
)

// stubAuthRequiredService is an MCPServer service sitting in auth-required
// state, exposing the definition fields the pending-auth heal reads.
type stubAuthRequiredService struct {
	name string
	auth *api.MCPServerAuth
}

func (s *stubAuthRequiredService) GetName() string            { return s.name }
func (s *stubAuthRequiredService) GetType() api.ServiceType   { return api.TypeMCPServer }
func (s *stubAuthRequiredService) GetState() api.ServiceState { return api.StateAuthRequired }
func (s *stubAuthRequiredService) GetHealth() api.HealthStatus {
	return api.HealthUnknown
}
func (s *stubAuthRequiredService) GetLastError() error { return nil }
func (s *stubAuthRequiredService) GetServiceData() map[string]interface{} {
	return map[string]interface{}{
		"url":        "http://localhost:9/mcp",
		"toolPrefix": s.name,
		"family":     (*api.MCPServerFamily)(nil),
		"meta":       map[string]string{"team": "test"},
		"auth":       s.auth,
	}
}

// stubTypedServiceRegistry serves a fixed service list from GetByType.
type stubTypedServiceRegistry struct {
	stubServiceRegistry
	services []api.ServiceInfo
}

func (s *stubTypedServiceRegistry) GetByType(api.ServiceType) []api.ServiceInfo {
	return s.services
}

// TestAttemptPendingRegistrations_HealsAuthRequired is the regression test for
// issue #1110: an MCPServer hit its 401 before the aggregator manager existed,
// so the orchestrator's auth-required hook could not register it. The service
// reported Auth Required (instance readiness passed) while the aggregator
// registry had no entry, and core_auth_login answered "Server not found" for
// the life of the process. The periodic reconciliation must rebuild the
// pending-auth entry from the service's own definition.
func TestAttemptPendingRegistrations_HealsAuthRequired(t *testing.T) {
	t.Run("registers a missing pending-auth entry for an auth-required service", func(t *testing.T) {
		reg := NewServerRegistry("x")
		svc := &stubAuthRequiredService{name: "stranded-oauth"}

		am := &AggregatorManager{
			aggregatorServer: &AggregatorServer{registry: reg},
			serviceRegistry:  &stubTypedServiceRegistry{services: []api.ServiceInfo{svc}},
		}

		am.attemptPendingRegistrations(context.Background())

		info, exists := reg.GetServerInfo("stranded-oauth")
		assert.True(t, exists, "auth-required service without a registry entry must be healed")
		if assert.NotNil(t, info) {
			assert.True(t, info.RequiresSessionAuth(), "healed entry must require session auth so core_auth_login accepts it")
			assert.Equal(t, "http://localhost:9/mcp", info.URL)
		}
	})

	t.Run("leaves an existing pending-auth entry untouched", func(t *testing.T) {
		reg := NewServerRegistry("x")
		registerPendingAuthServer(t, reg, "registered-oauth")
		before, _ := reg.GetServerInfo("registered-oauth")

		svc := &stubAuthRequiredService{name: "registered-oauth"}
		am := &AggregatorManager{
			aggregatorServer: &AggregatorServer{registry: reg},
			serviceRegistry:  &stubTypedServiceRegistry{services: []api.ServiceInfo{svc}},
		}

		am.attemptPendingRegistrations(context.Background())

		after, exists := reg.GetServerInfo("registered-oauth")
		assert.True(t, exists)
		assert.Same(t, before, after, "an existing entry must not be replaced (it carries discovered AuthInfo)")
	})

	t.Run("refuses a non-interactive auth type", func(t *testing.T) {
		reg := NewServerRegistry("x")
		svc := &stubAuthRequiredService{
			name: "sigv4-server",
			auth: &api.MCPServerAuth{Type: api.MCPServerAuthTypeSigV4},
		}

		am := &AggregatorManager{
			aggregatorServer: &AggregatorServer{registry: reg},
			serviceRegistry:  &stubTypedServiceRegistry{services: []api.ServiceInfo{svc}},
		}

		am.attemptPendingRegistrations(context.Background())

		_, exists := reg.GetServerInfo("sigv4-server")
		assert.False(t, exists, "an auth type with no login flow must not be registered for interactive auth")
	})
}
