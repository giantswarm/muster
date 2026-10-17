package aggregator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// stubFailedService is an MCPServer service whose backend does not answer:
// Failed and unhealthy, with no client, its definition intact.
type stubFailedService struct {
	name      string
	namespace string
	auth      *api.MCPServerAuth
}

func (s *stubFailedService) GetName() string             { return s.name }
func (s *stubFailedService) GetType() api.ServiceType    { return api.TypeMCPServer }
func (s *stubFailedService) GetState() api.ServiceState  { return api.StateFailed }
func (s *stubFailedService) GetHealth() api.HealthStatus { return api.HealthUnhealthy }
func (s *stubFailedService) GetLastError() error         { return errors.New("connection refused") }
func (s *stubFailedService) GetServiceData() map[string]interface{} {
	return map[string]interface{}{
		"url":         "http://workspace-manager.agent-platform:8080/mcp",
		"auth":        s.auth,
		"namespace":   s.namespace,
		"clientReady": false,
	}
}

// TestPinDefinedAuthorizationServers is the regression test for
// giantswarm/muster#1553: a subject-scoped issuer was pinned only once its
// MCPServer answered (connected or 401), so after a muster roll with the
// issuer's backend down FetchSecret answered "the person holds no grant"
// although the grant was intact. The pin follows the definition instead.
func TestPinDefinedAuthorizationServers(t *testing.T) {
	ctx := context.Background()
	const issuer = "https://github.com/login/oauth"
	secretRef := &api.ClientCredentialsSecretRef{Name: "workspaces-github-app"}
	issuerAuth := func() *api.MCPServerAuth {
		return githubStyleServer(secretRef, api.GrantScopeSubject).AuthConfig
	}

	setup := func(t *testing.T, secrets *mockSecretCredentialsHandler, services ...api.ServiceInfo) (*AggregatorManager, *pinCaptureHandler, *stubTypedServiceRegistry) {
		t.Helper()
		handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
		api.RegisterOAuthHandler(handler)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })
		api.RegisterSecretCredentialsHandler(secrets)
		t.Cleanup(func() { api.RegisterSecretCredentialsHandler(nil) })

		typed := &stubTypedServiceRegistry{services: services}
		am := &AggregatorManager{
			aggregatorServer: &AggregatorServer{registry: NewServerRegistry("x")},
			serviceRegistry:  typed,
			definitionPins:   map[string]*api.MCPServerAuthAuthorizationServer{},
		}
		am.aggregatorServer.definedServers = am.definedServers
		return am, handler, typed
	}
	appCredentials := func() *mockSecretCredentialsHandler {
		return &mockSecretCredentialsHandler{credentials: &api.ClientCredentials{ClientID: "Iv23liApp", ClientSecret: "shh"}}
	}

	t.Run("a Failed issuer MCPServer is pinned from its definition", func(t *testing.T) {
		svc := &stubFailedService{name: "workspace-manager", namespace: "agent-platform", auth: issuerAuth()}
		am, handler, _ := setup(t, appCredentials(), svc)

		am.pinDefinedAuthorizationServers(ctx)

		pin, ok := handler.pins[issuer]
		require.True(t, ok, "the issuer is pinned although its backend never answered")
		assert.True(t, pin.SubjectScoped, "the person's grant is released for a subject-scoped issuer")
		assert.Equal(t, "Iv23liApp", pin.ClientID)
		_, registered := am.aggregatorServer.registry.GetServerInfo("workspace-manager")
		assert.False(t, registered, "pinning registers nothing")
	})

	t.Run("a backend that stops answering keeps the pin", func(t *testing.T) {
		svc := &stubFailedService{name: "workspace-manager", namespace: "agent-platform", auth: issuerAuth()}
		am, handler, _ := setup(t, appCredentials(), svc)
		require.NoError(t, am.aggregatorServer.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "workspace-manager", Namespace: "agent-platform"},
			URL:                "http://workspace-manager.agent-platform:8080/mcp",
			AuthInfo:           &AuthInfo{},
			AuthConfig:         issuerAuth(),
		}))
		am.pinDefinedAuthorizationServers(ctx)

		err := am.aggregatorServer.DeregisterServer("workspace-manager")
		require.NoError(t, err)

		_, still := handler.pins[issuer]
		assert.True(t, still, "the definition still describes the issuer")
	})

	t.Run("a deleted definition releases its pin", func(t *testing.T) {
		svc := &stubFailedService{name: "workspace-manager", namespace: "agent-platform", auth: issuerAuth()}
		am, handler, services := setup(t, appCredentials(), svc)
		am.pinDefinedAuthorizationServers(ctx)
		require.Contains(t, handler.pins, issuer)

		services.services = nil
		am.pinDefinedAuthorizationServers(ctx)

		assert.NotContains(t, handler.pins, issuer, "no MCPServer describes the issuer any more")
		assert.Empty(t, am.definitionPins)
	})

	t.Run("a pin the Secret defers is retried", func(t *testing.T) {
		svc := &stubFailedService{name: "workspace-manager", namespace: "agent-platform", auth: issuerAuth()}
		secrets := &mockSecretCredentialsHandler{err: errors.New("secret not found")}
		am, handler, _ := setup(t, secrets, svc)

		am.pinDefinedAuthorizationServers(ctx)
		assert.Empty(t, handler.pins)

		secrets.err, secrets.credentials = nil, &api.ClientCredentials{ClientID: "Iv23liApp", ClientSecret: "shh"}
		am.pinDefinedAuthorizationServers(ctx)
		assert.Contains(t, handler.pins, issuer)

		am.pinDefinedAuthorizationServers(ctx)
		assert.Equal(t, 2, secrets.loadCalls, "an unchanged definition is pinned once")
	})
}
