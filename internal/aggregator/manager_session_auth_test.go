package aggregator

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

// stubConnectedService is an MCPServer service that reports Connected and
// Healthy — the state a service reaches when its connection probe was
// answered by a backend that accepts anonymous requests — and exposes the
// definition the aggregator reads, including a probe client.
type stubConnectedService struct {
	name      string
	namespace string
	auth      *api.MCPServerAuth
	client    MCPClient
}

func (s *stubConnectedService) GetName() string             { return s.name }
func (s *stubConnectedService) GetType() api.ServiceType    { return api.TypeMCPServer }
func (s *stubConnectedService) GetState() api.ServiceState  { return api.StateConnected }
func (s *stubConnectedService) GetHealth() api.HealthStatus { return api.HealthHealthy }
func (s *stubConnectedService) GetLastError() error         { return nil }
func (s *stubConnectedService) GetServiceData() map[string]interface{} {
	data := map[string]interface{}{
		"url":        "http://localhost:9/mcp",
		"toolPrefix": s.name,
		"family":     (*api.MCPServerFamily)(nil),
		"meta":       map[string]string(nil),
	}
	if s.namespace != "" {
		data["namespace"] = s.namespace
	}
	if s.auth != nil {
		data["auth"] = s.auth
	}
	if s.client != nil {
		data["client"] = s.client
		data["clientReady"] = true
	}
	return data
}

// stubNamedServiceRegistry serves the given services by name and by type.
type stubNamedServiceRegistry struct {
	services map[string]api.ServiceInfo
}

func newStubNamedServiceRegistry(services ...api.ServiceInfo) *stubNamedServiceRegistry {
	s := &stubNamedServiceRegistry{services: make(map[string]api.ServiceInfo, len(services))}
	for _, svc := range services {
		s.services[svc.GetName()] = svc
	}
	return s
}

func (s *stubNamedServiceRegistry) Get(name string) (api.ServiceInfo, bool) {
	svc, exists := s.services[name]
	return svc, exists
}

func (s *stubNamedServiceRegistry) GetAll() []api.ServiceInfo { return s.GetByType(api.TypeMCPServer) }

func (s *stubNamedServiceRegistry) GetByType(api.ServiceType) []api.ServiceInfo {
	out := make([]api.ServiceInfo, 0, len(s.services))
	for _, svc := range s.services {
		out = append(out, svc)
	}
	return out
}

// probeClient is the token-less client a service keeps after an anonymous
// probe; the tests assert it is never consulted for tools.
type probeClient struct {
	mockMCPClient
	listToolsCalls int
}

func (c *probeClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	c.listToolsCalls++
	return c.mockMCPClient.ListTools(ctx)
}

func (c *probeClient) listToolsCalled() bool { return c.listToolsCalls > 0 }

func forwardTokenAuth() *api.MCPServerAuth {
	return &api.MCPServerAuth{Type: "oauth", ForwardToken: true, RequiredAudiences: []string{"kubernetes"}}
}

// TestIsServerSSOBased_ReadsServerConfiguration is the regression test for
// issue #1135. On a configuration change the reconciler deregisters the server
// before restarting it, so when the restarted service reports Connected there
// is no registry entry to consult. The decision must come from the service's
// own definition, or a server that just gained auth.forwardToken is registered
// globally with a token-less client.
func TestIsServerSSOBased_ReadsServerConfiguration(t *testing.T) {
	t.Run("forwardToken service without a registry entry is session-based", func(t *testing.T) {
		am := &AggregatorManager{
			aggregatorServer: &AggregatorServer{registry: NewServerRegistry("x")},
			serviceRegistry:  newStubNamedServiceRegistry(&stubConnectedService{name: "model-manager", auth: forwardTokenAuth()}),
		}
		assert.True(t, am.isServerSSOBased("model-manager"))
	})

	t.Run("tokenExchange service without a registry entry is session-based", func(t *testing.T) {
		auth := &api.MCPServerAuth{Type: "oauth", TokenExchange: &api.TokenExchangeConfig{Enabled: true}}
		am := &AggregatorManager{
			aggregatorServer: &AggregatorServer{registry: NewServerRegistry("x")},
			serviceRegistry:  newStubNamedServiceRegistry(&stubConnectedService{name: "remote-mc", auth: auth}),
		}
		assert.True(t, am.isServerSSOBased("remote-mc"))
	})

	t.Run("service without session auth is not, even when the registry says pending auth", func(t *testing.T) {
		// A plain OAuth server (core_auth_login flow) keeps its pending-auth
		// entry, but it is not SSO: the event handler must still register it
		// globally once a user's login connects it.
		reg := NewServerRegistry("x")
		registerPendingAuthServer(t, reg, "plain-oauth")
		am := &AggregatorManager{
			aggregatorServer: &AggregatorServer{registry: reg},
			serviceRegistry:  newStubNamedServiceRegistry(&stubConnectedService{name: "plain-oauth", auth: &api.MCPServerAuth{Type: "oauth"}}),
		}
		assert.False(t, am.isServerSSOBased("plain-oauth"))
	})

	t.Run("service with no auth block is not session-based", func(t *testing.T) {
		am := &AggregatorManager{
			aggregatorServer: &AggregatorServer{registry: NewServerRegistry("x")},
			serviceRegistry:  newStubNamedServiceRegistry(&stubConnectedService{name: "anonymous"}),
		}
		assert.False(t, am.isServerSSOBased("anonymous"))
	})

	t.Run("falls back to the registry entry for a server without a service", func(t *testing.T) {
		reg := NewServerRegistry("x")
		require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "direct"},
			URL:                "https://direct.example.com",
			AuthConfig:         forwardTokenAuth(),
		}))
		am := &AggregatorManager{
			aggregatorServer: &AggregatorServer{registry: reg},
			serviceRegistry:  newStubNamedServiceRegistry(),
		}
		assert.True(t, am.isServerSSOBased("direct"))
		assert.False(t, am.isServerSSOBased("unknown"))
	})
}

// TestRegisterSingleServer_RefusesSessionAuthServer pins the backstop: even a
// Connected, Healthy service that still holds a probe client must not be
// registered globally when its configuration says session-level auth.
func TestRegisterSingleServer_RefusesSessionAuthServer(t *testing.T) {
	reg := NewServerRegistry("x")
	client := &probeClient{}
	am := &AggregatorManager{
		aggregatorServer: &AggregatorServer{registry: reg},
		serviceRegistry:  newStubNamedServiceRegistry(&stubConnectedService{name: "model-manager", auth: forwardTokenAuth(), client: client}),
	}

	err := am.registerSingleServer(context.Background(), "model-manager")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session-level auth")

	_, exists := reg.GetServerInfo("model-manager")
	assert.False(t, exists, "no registry entry may be created from the token-less client")
	assert.False(t, client.listToolsCalled(), "the token-less client must not be used")
}

// TestRegisterSingleServer_CarriesNamespace: the global registration path
// takes the MCPServer's namespace from the service definition, so the entry's
// Secret references and events resolve against the resource's own namespace.
func TestRegisterSingleServer_CarriesNamespace(t *testing.T) {
	reg := NewServerRegistry("x")
	am := &AggregatorManager{
		aggregatorServer: &AggregatorServer{registry: reg},
		serviceRegistry: newStubNamedServiceRegistry(&stubConnectedService{
			name: "kubernetes", namespace: "agent-platform", client: &probeClient{},
		}),
	}

	require.NoError(t, am.registerSingleServer(context.Background(), "kubernetes"))

	info, exists := reg.GetServerInfo("kubernetes")
	require.True(t, exists)
	assert.Equal(t, "agent-platform", info.GetNamespace())
}

// TestAttemptPendingRegistrations_SessionAuthServerGetsPendingAuthEntry covers
// the periodic reconciliation: a session-auth service that reports Connected
// (its probe reached an anonymous backend) and has no registry entry gets a
// pending-auth entry — the only kind it may ever have — and never a global one.
func TestAttemptPendingRegistrations_SessionAuthServerGetsPendingAuthEntry(t *testing.T) {
	reg := NewServerRegistry("x")
	client := &probeClient{}
	svc := &stubConnectedService{name: "model-manager", namespace: "agent-platform", auth: forwardTokenAuth(), client: client}
	am := &AggregatorManager{
		aggregatorServer: &AggregatorServer{registry: reg},
		serviceRegistry:  newStubNamedServiceRegistry(svc),
	}

	am.attemptPendingRegistrations(context.Background())

	info, exists := reg.GetServerInfo("model-manager")
	require.True(t, exists, "the session-auth server must be registered for per-session connections")
	assert.True(t, info.RequiresSessionAuth())
	assert.Equal(t, "agent-platform", info.GetNamespace(), "the pending-auth entry carries the MCPServer's namespace")
	assert.True(t, ShouldUseTokenForwarding(info), "the entry must carry the forwardToken config so sessions forward the caller's token")
	assert.Nil(t, info.Client, "no shared client may be attached")
	assert.False(t, client.listToolsCalled(), "the token-less probe client must not be used")

	// A second pass leaves the entry alone.
	am.attemptPendingRegistrations(context.Background())
	again, _ := reg.GetServerInfo("model-manager")
	assert.Same(t, info, again)
}

// TestRegisterHealthyMCPServers_SkipsSessionAuthServers covers the initial
// sync at aggregator start.
func TestRegisterHealthyMCPServers_SkipsSessionAuthServers(t *testing.T) {
	reg := NewServerRegistry("x")
	client := &probeClient{}
	am := &AggregatorManager{
		aggregatorServer: &AggregatorServer{registry: reg},
		serviceRegistry:  newStubNamedServiceRegistry(&stubConnectedService{name: "model-manager", auth: forwardTokenAuth(), client: client}),
	}

	require.NoError(t, am.registerHealthyMCPServers(context.Background()))

	_, exists := reg.GetServerInfo("model-manager")
	assert.False(t, exists, "initial sync must not register a session-auth server globally")
	assert.False(t, client.listToolsCalled())
}
