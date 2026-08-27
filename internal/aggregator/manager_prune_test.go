package aggregator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

// stubServiceRegistry implements api.ServiceRegistryHandler with a fixed set of
// service names. Only Get is exercised by pruneOrphanedRegistrations.
type stubServiceRegistry struct {
	names map[string]struct{}
}

func newStubServiceRegistry(names ...string) *stubServiceRegistry {
	s := &stubServiceRegistry{names: make(map[string]struct{}, len(names))}
	for _, name := range names {
		s.names[name] = struct{}{}
	}
	return s
}

func (s *stubServiceRegistry) Get(name string) (api.ServiceInfo, bool) {
	_, exists := s.names[name]
	return nil, exists
}

func (s *stubServiceRegistry) GetAll() []api.ServiceInfo { return nil }

func (s *stubServiceRegistry) GetByType(api.ServiceType) []api.ServiceInfo { return nil }

func registerPendingAuthServer(t *testing.T, reg *ServerRegistry, name string) {
	t.Helper()
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: name, ToolPrefix: name},
		URL:                "https://" + name + ".example.com",
		AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
	}))
}

func TestPruneOrphanedRegistrations(t *testing.T) {
	// The regression: an OAuth-protected server is registered pending auth, its
	// definition is deleted (so the service disappears), and the event handler
	// deliberately skips deregistering auth_required servers. Nothing else ever
	// removes the entry, so it keeps showing up in servers_requiring_auth.
	t.Run("drops a pending-auth registration whose service is gone", func(t *testing.T) {
		reg := NewServerRegistry("x")
		registerPendingAuthServer(t, reg, "deleted-oauth")
		registerPendingAuthServer(t, reg, "live-oauth")

		am := &AggregatorManager{
			aggregatorServer: &AggregatorServer{registry: reg},
			serviceRegistry:  newStubServiceRegistry("live-oauth"),
		}

		am.pruneOrphanedRegistrations()

		_, exists := reg.GetServerInfo("deleted-oauth")
		assert.False(t, exists, "registration without a service should be pruned")
		_, exists = reg.GetServerInfo("live-oauth")
		assert.True(t, exists, "registration backed by a live service must survive")
	})

	t.Run("keeps every registration while all services exist", func(t *testing.T) {
		reg := NewServerRegistry("x")
		registerPendingAuthServer(t, reg, "one")
		registerPendingAuthServer(t, reg, "two")

		am := &AggregatorManager{
			aggregatorServer: &AggregatorServer{registry: reg},
			serviceRegistry:  newStubServiceRegistry("one", "two"),
		}

		am.pruneOrphanedRegistrations()

		assert.Len(t, reg.GetAllServers(), 2)
	})

	// Without a service registry the manager cannot tell an orphan from a
	// service it simply cannot see, so it must not guess and drop everything.
	t.Run("no-ops without a service registry", func(t *testing.T) {
		reg := NewServerRegistry("x")
		registerPendingAuthServer(t, reg, "one")

		am := &AggregatorManager{aggregatorServer: &AggregatorServer{registry: reg}}

		am.pruneOrphanedRegistrations()

		assert.Len(t, reg.GetAllServers(), 1)
	})
}
