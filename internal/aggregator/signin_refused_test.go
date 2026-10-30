package aggregator

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// refusedService is an MCPServer service in a state with its last error.
type refusedService struct {
	name  string
	state api.ServiceState
	err   error
}

func (s *refusedService) GetName() string                        { return s.name }
func (s *refusedService) GetType() api.ServiceType               { return api.TypeMCPServer }
func (s *refusedService) GetState() api.ServiceState             { return s.state }
func (s *refusedService) GetHealth() api.HealthStatus            { return api.HealthUnhealthy }
func (s *refusedService) GetLastError() error                    { return s.err }
func (s *refusedService) GetServiceData() map[string]interface{} { return nil }

// serviceRegistryOf answers Get with one service.
type serviceRegistryOf struct {
	*stubServiceRegistry
	service *refusedService
}

func (r *serviceRegistryOf) Get(name string) (api.ServiceInfo, bool) {
	if name != r.service.name {
		return nil, false
	}
	return r.service, true
}

// fixtureAuth pins https://as.example.com with a client Secret.
var fixtureAuth = &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
	Issuer:                     "https://as.example.com",
	ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{Name: "fixture-oauth-client-abcde"},
}}

// A server whose sign-in muster refuses loses its pending-auth entry and the
// pin it was registered with, so core_auth_login answers with the refusal
// instead of signing in with the old pin. Any other failure keeps the entry.
func TestSignInRefused_DropsRegistrationAndPin(t *testing.T) {
	refused := fmt.Errorf("load client credentials: %w", api.ErrSecretOtherAuthorizationServer)
	tests := []struct {
		name       string
		state      api.ServiceState
		err        error
		definition *api.MCPServerAuth // the definition's auth now; nil: none
		wantKept   bool
	}{
		{name: "refused, the spec moved to another issuer", state: api.StateFailed, err: refused,
			definition: &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
				Issuer: "https://evil.example.com", ClientCredentialsSecretRef: fixtureAuth.AuthorizationServer.ClientCredentialsSecretRef,
			}}},
		{name: "refused, same description", state: api.StateFailed, err: refused, definition: fixtureAuth},
		{name: "refused, unreachable", state: api.StateUnreachable, err: fmt.Errorf("x: %w", api.ErrSecretNotOwned), definition: fixtureAuth},
		{name: "another failure", state: api.StateFailed, err: errors.New("connection refused"), definition: fixtureAuth, wantKept: true},
		{name: "refused error, but auth required again", state: api.StateAuthRequired, err: refused, definition: fixtureAuth, wantKept: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{
				"https://as.example.com": {DescriptionKey: "old"},
			}}
			api.RegisterOAuthHandler(handler)
			t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

			reg := NewServerRegistry("x")
			require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
				ServerRegistration: ServerRegistration{Name: "fixture"},
				URL:                "https://fixture.example.com/mcp",
				AuthConfig:         fixtureAuth,
			}))
			server := &AggregatorServer{registry: reg, definedServers: func() map[string]*ServerInfo {
				return map[string]*ServerInfo{"fixture": {Name: "fixture", AuthConfig: tt.definition}}
			}}
			am := &AggregatorManager{
				aggregatorServer: server,
				serviceRegistry: &serviceRegistryOf{stubServiceRegistry: newStubServiceRegistry(),
					service: &refusedService{name: "fixture", state: tt.state, err: tt.err}},
			}

			assert.Equal(t, tt.wantKept, am.isServerAuthRequired("fixture"), "the event handler skips only a kept entry")
			require.NoError(t, am.deregisterOnStateChange("fixture"))
			_, exists := reg.GetServerInfo("fixture")
			assert.Equal(t, tt.wantKept, exists)
			_, pinned := handler.pins["https://as.example.com"]
			assert.Equal(t, tt.wantKept, pinned, "the pin of a refused client goes")
		})
	}
}

func TestDescriptionConflict(t *testing.T) {
	reg := NewServerRegistry("x")
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "hubspot"},
		URL:                "https://hubspot.example.com/mcp",
		AuthConfig:         fixtureAuth,
	}))
	a := &AggregatorServer{registry: reg}
	auth := func(issuer, ref string) *api.MCPServerAuth {
		as := &api.MCPServerAuthAuthorizationServer{Issuer: issuer}
		if ref != "" {
			as.ClientCredentialsSecretRef = &api.ClientCredentialsSecretRef{Name: ref}
		}
		return &api.MCPServerAuth{Type: "oauth", AuthorizationServer: as}
	}

	err := a.DescriptionConflict("hubspot-2", auth("https://as.example.com", "hubspot-2-oauth-client-new"))
	require.ErrorIs(t, err, api.ErrAuthorizationServerConflict)
	assert.Contains(t, err.Error(), "MCP server 'hubspot' signs in to https://as.example.com with another client or endpoints")

	assert.NoError(t, a.DescriptionConflict("hubspot-2", auth("https://as.example.com", "fixture-oauth-client-abcde")), "the same client")
	assert.NoError(t, a.DescriptionConflict("hubspot-2", auth("https://other.example.com", "hubspot-2-oauth-client-new")), "another issuer")
	assert.NoError(t, a.DescriptionConflict("hubspot", auth("https://as.example.com", "hubspot-oauth-client-new")), "itself")
	assert.NoError(t, a.DescriptionConflict("plain", auth("https://as.example.com", "")), "a plain issuer override pins nothing")

	// The comparison is the pin's: the older server's description is
	// refused for the newer one there too.
	newer := &ServerInfo{Name: "hubspot-2", AuthConfig: auth("https://as.example.com", "hubspot-2-oauth-client-new")}
	_, pinErr := a.issuerPinOwner(newer)
	require.ErrorIs(t, pinErr, api.ErrAuthorizationServerConflict)
}
