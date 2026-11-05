package aggregator

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	configPkg "github.com/giantswarm/muster/v5/internal/config"
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
	ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{Name: "fixture-muster-client-abcde12345"},
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
		{name: "the reference policy not in force", state: api.StateFailed, definition: fixtureAuth, wantKept: true,
			err: fmt.Errorf("x: %w", api.ErrSecretReferencePolicyInactive)},
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

// A refused server already removed is not removed again on every failed
// retry of its backoff; a pin it still owns goes.
func TestSignInRefused_NotRemovedAgain(t *testing.T) {
	handler := &pinCaptureHandler{mockOAuthHandler: newMockOAuthHandler(true), pins: map[string]api.IssuerPin{}}
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })
	server := &AggregatorServer{registry: NewServerRegistry("x"), definedServers: func() map[string]*ServerInfo {
		return map[string]*ServerInfo{"fixture": {Name: "fixture", AuthConfig: fixtureAuth}}
	}}
	am := &AggregatorManager{
		aggregatorServer: server,
		serviceRegistry: &serviceRegistryOf{stubServiceRegistry: newStubServiceRegistry(),
			service: &refusedService{name: "fixture", state: api.StateFailed, err: fmt.Errorf("x: %w", api.ErrSecretNotOwned)}},
	}

	require.Error(t, am.deregisterOnStateChange("fixture"), "nothing to remove: the event handler emits no event")
	assert.Empty(t, handler.unpinned)

	handler.pins["https://as.example.com"] = api.IssuerPin{DescriptionKey: "repinned"}
	require.Error(t, am.deregisterOnStateChange("fixture"))
	assert.Equal(t, []string{"https://as.example.com"}, handler.unpinned, "a pin it still owns goes")
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

	err := a.DescriptionConflict("hubspot-2", nil, auth("https://as.example.com", ""), true)
	require.ErrorIs(t, err, api.ErrAuthorizationServerConflict)
	assert.Contains(t, err.Error(), "MCP server 'hubspot' signs in to https://as.example.com with another client or endpoints")

	assert.NoError(t, a.DescriptionConflict("hubspot-2", nil, auth("https://as.example.com", "fixture-muster-client-abcde12345"), false), "the same client")
	assert.NoError(t, a.DescriptionConflict("hubspot-2", nil, auth("https://other.example.com", ""), true), "another issuer")
	assert.NoError(t, a.DescriptionConflict("hubspot", nil, auth("https://as.example.com", ""), true), "itself")
	assert.NoError(t, a.DescriptionConflict("plain", nil, auth("https://as.example.com", ""), false), "a plain issuer override pins nothing")

	// Unchanged auth on the owner is not blocked by a newer server that
	// conflicts with it; changing its client is.
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "hubspot-2"},
		URL:                "https://hubspot-2.example.com/mcp",
		AuthConfig:         auth("https://as.example.com", "hubspot-2-muster-client-xyzab12345"),
	}))
	owner := auth("https://as.example.com", "fixture-muster-client-abcde12345")
	assert.NoError(t, a.DescriptionConflict("hubspot", owner, owner, false), "unchanged auth on the owner is not blocked")
	rescoped := auth("https://as.example.com", "fixture-muster-client-abcde12345")
	rescoped.AuthorizationServer.Scopes = "crm"
	assert.NoError(t, a.DescriptionConflict("hubspot", owner, rescoped, false), "scopes are not part of the pinned description")
	require.ErrorIs(t, a.DescriptionConflict("hubspot", owner, auth("https://as.example.com", ""), true),
		api.ErrAuthorizationServerConflict, "a new client is checked")
	require.ErrorIs(t, a.DescriptionConflict("hubspot", auth("https://old.example.com", "fixture-muster-client-abcde12345"), owner, false),
		api.ErrAuthorizationServerConflict, "moving to the issuer is checked")

	// A new client on an issuer only this server pins is no conflict.
	assert.NoError(t, a.DescriptionConflict("fresh", nil, auth("https://fresh.example.com", ""), true))

	// The comparison is the pin's: the older server's description is
	// refused for the newer one there too.
	newer := &ServerInfo{Name: "hubspot-2", AuthConfig: auth("https://as.example.com", "hubspot-2-muster-client-new")}
	_, pinErr := a.issuerPinOwner(newer)
	require.ErrorIs(t, pinErr, api.ErrAuthorizationServerConflict)
}

// An issuer a provider App owns signs in with the App's client: a new client
// there would be stored and never used.
func TestDescriptionConflict_ProviderApp(t *testing.T) {
	setAppOwnedIssuers(&providerAppSet{
		apps:  map[string]configPkg.ProviderAppConfig{"github": {Name: "github", Issuer: "https://github.com"}},
		order: []string{"github"},
	})
	t.Cleanup(func() { setAppOwnedIssuers(nil) })
	a := &AggregatorServer{registry: NewServerRegistry("x")}
	auth := &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: "https://github.com"}}

	err := a.DescriptionConflict("github-connector", nil, auth, true)
	require.ErrorIs(t, err, api.ErrAuthorizationServerConflict)
	assert.Contains(t, err.Error(), "provider App 'github' signs in to https://github.com with its own client")

	assert.NoError(t, a.DescriptionConflict("github-connector", nil, auth, false), "without a client of its own the App's pin stands, as before")
}
