package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

// Issue #1211: an MCPServer whose spec holds its service down
// (spec.suspended=true) keeps its pending-auth registry entry, so the sign-in
// paths and the status resource used to treat it like any auth-required
// server. These tests pin the three answers the fix gives: core_auth_login
// refuses it, the OAuth callback stores the token without connecting, and
// auth://status reports the infrastructure state instead of the auth mark.

// stubMCPServerManager is the definition source the aggregator reads
// spec.suspended from. lists counts ListMCPServers calls so a test can prove
// auth://status reads the spec only when a server is down.
type stubMCPServerManager struct {
	servers map[string]api.MCPServerInfo
	err     error
	lists   int
}

func (m *stubMCPServerManager) ListMCPServers(context.Context) ([]api.MCPServerInfo, error) {
	m.lists++
	if m.err != nil {
		return nil, m.err
	}
	servers := make([]api.MCPServerInfo, 0, len(m.servers))
	for _, info := range m.servers {
		servers = append(servers, info)
	}
	return servers, nil
}

func (m *stubMCPServerManager) GetMCPServer(_ context.Context, name string) (*api.MCPServerInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	info, ok := m.servers[name]
	if !ok {
		return nil, api.NewMCPServerNotFoundError(name)
	}
	return &info, nil
}

func (m *stubMCPServerManager) GetTools() []api.ToolMetadata { return nil }

func (m *stubMCPServerManager) ExecuteTool(context.Context, string, map[string]interface{}) (*api.CallToolResult, error) {
	return nil, nil
}

// recordingStateService is a service registry entry that records external
// state updates, so a test can assert that a path did not flip the state.
type recordingStateService struct {
	stubServiceInfo
	updates []api.ServiceState
}

func (s *recordingStateService) UpdateState(state api.ServiceState, _ api.HealthStatus, _ error) {
	s.updates = append(s.updates, state)
}

type recordingServiceRegistry struct {
	services map[string]*recordingStateService
}

func (r *recordingServiceRegistry) GetAll() []api.ServiceInfo                   { return nil }
func (r *recordingServiceRegistry) GetByType(api.ServiceType) []api.ServiceInfo { return nil }
func (r *recordingServiceRegistry) Get(name string) (api.ServiceInfo, bool) {
	svc, ok := r.services[name]
	if !ok {
		return nil, false
	}
	return svc, true
}

// challengeCountingOAuthHandler counts CreateAuthChallenge calls: a refused
// login must not have created one.
type challengeCountingOAuthHandler struct {
	issuerMockOAuthHandler
	challenges int
}

func (m *challengeCountingOAuthHandler) CreateAuthChallenge(_ context.Context, _ api.AuthChallengeParams) (*api.AuthChallenge, error) {
	m.challenges++
	return &api.AuthChallenge{AuthURL: "https://muster.example.com/oauth/proxy/start?state=x"}, nil
}

// registerSuspendableServer registers one OAuth server that authenticates per
// session and wires the definition source and the service registry around it.
func registerSuspendableServer(t *testing.T, registry *ServerRegistry, name string, suspended bool, state api.ServiceState) (*stubMCPServerManager, *recordingStateService) {
	t.Helper()

	require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: name, ToolPrefix: name},
		URL:                "http://127.0.0.1:1/mcp",
		AuthInfo:           &AuthInfo{Issuer: "https://idp.example.com", Scope: "mcp:read"},
		AuthConfig:         &api.MCPServerAuth{Type: "oauth"},
	}))

	manager := &stubMCPServerManager{servers: map[string]api.MCPServerInfo{
		name: {Name: name, Type: "streamable-http", URL: "http://127.0.0.1:1/mcp", Suspended: suspended},
	}}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	service := &recordingStateService{stubServiceInfo: stubServiceInfo{name: name, state: state}}
	api.RegisterServiceRegistry(&recordingServiceRegistry{services: map[string]*recordingStateService{name: service}})
	t.Cleanup(func() { api.RegisterServiceRegistry(nil) })

	return manager, service
}

func newSuspensionTestServer(t *testing.T) *AggregatorServer {
	t.Helper()
	authStore := oauthstore.NewInMemorySessionAuthStore(time.Hour)
	t.Cleanup(authStore.Stop)
	pool := NewSessionConnectionPool(time.Hour)
	t.Cleanup(pool.Stop)
	return &AggregatorServer{
		registry:  NewServerRegistry("x"),
		authStore: authStore,
		connPool:  pool,
	}
}

func TestHandleAuthLogin_RefusesSuspendedServer(t *testing.T) {
	a := newSuspensionTestServer(t)
	registerSuspendableServer(t, a.registry, "miro", true, api.StateDisconnected)

	oauthHandler := &challengeCountingOAuthHandler{issuerMockOAuthHandler: issuerMockOAuthHandler{enabled: true}}
	api.RegisterOAuthHandler(oauthHandler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	result, err := NewAuthToolProvider(a).handleAuthLogin(testSessionCtx(), map[string]any{"server": "miro"})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.True(t, result.IsError, "a suspended server is refused, not challenged")
	require.Len(t, result.Content, 1)
	assert.Equal(t, "Server 'miro' is deactivated (spec.suspended=true); activate it with core_service_start before signing in.", result.Content[0])
	assert.Equal(t, 0, oauthHandler.challenges, "no auth challenge is created for a suspended server")
}

func TestHandleAuthLogin_SuspensionWinsOverAuthMark(t *testing.T) {
	// A session that signed in before the deactivation holds an auth mark. The
	// mark says nothing about whether the server can be used, so the answer is
	// the deactivation, not "already authenticated".
	a := newSuspensionTestServer(t)
	registerSuspendableServer(t, a.registry, "miro", true, api.StateDisconnected)
	require.NoError(t, a.authStore.MarkAuthenticated(context.Background(), "test-session", "miro"))

	result, err := NewAuthToolProvider(a).handleAuthLogin(testSessionCtx(), map[string]any{"server": "miro"})
	require.NoError(t, err)

	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0], "is deactivated (spec.suspended=true)")
}

func TestHandleAuthLogin_ActiveServerIsNotRefused(t *testing.T) {
	// The check refuses suspended servers only: an active server with an auth
	// mark is answered as before.
	a := newSuspensionTestServer(t)
	registerSuspendableServer(t, a.registry, "miro", false, api.StateAuthRequired)
	require.NoError(t, a.authStore.MarkAuthenticated(context.Background(), "test-session", "miro"))

	result, err := NewAuthToolProvider(a).handleAuthLogin(testSessionCtx(), map[string]any{"server": "miro"})
	require.NoError(t, err)

	assert.False(t, result.IsError)
	assert.Equal(t, "Server 'miro' is already authenticated.", result.Content[0])
}

func TestHandleAuthLogin_UnknownDefinitionIsNotSuspended(t *testing.T) {
	// A registry entry whose CR is gone (or a server registered directly, as
	// in tests) has no spec to be suspended by; the login proceeds.
	a := newSuspensionTestServer(t)
	manager, _ := registerSuspendableServer(t, a.registry, "miro", true, api.StateAuthRequired)
	delete(manager.servers, "miro")
	require.NoError(t, a.authStore.MarkAuthenticated(context.Background(), "test-session", "miro"))

	result, err := NewAuthToolProvider(a).handleAuthLogin(testSessionCtx(), map[string]any{"server": "miro"})
	require.NoError(t, err)

	assert.False(t, result.IsError)
	assert.Equal(t, "Server 'miro' is already authenticated.", result.Content[0])
}

func TestHandleAuthLogin_SuspensionLookupFailureIsReported(t *testing.T) {
	// When the definition source cannot be read the login does not guess: the
	// person sees the lookup failure instead of a sign-in link that may lead
	// nowhere.
	a := newSuspensionTestServer(t)
	manager, _ := registerSuspendableServer(t, a.registry, "miro", false, api.StateAuthRequired)
	manager.err = errors.New("apiserver unavailable")

	result, err := NewAuthToolProvider(a).handleAuthLogin(testSessionCtx(), map[string]any{"server": "miro"})
	require.NoError(t, err)

	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0], "Cannot authenticate to 'miro'")
	assert.Contains(t, result.Content[0], "apiserver unavailable")
}

func TestHandleAuthCompletion_SuspendedServerStoresNoConnection(t *testing.T) {
	// The server was deactivated between the challenge and the callback. The
	// token is the OAuth handler's business (stored before the callback); the
	// aggregator establishes no connection, writes no mark, and does not flip
	// the service state — the flip is what used to make the event handler
	// fail a global registration and the reconciler stop the service again.
	a := newSuspensionTestServer(t)
	_, service := registerSuspendableServer(t, a.registry, "miro", true, api.StateDisconnected)
	am := &AggregatorManager{aggregatorServer: a}

	err := am.handleAuthCompletion(context.Background(), "test-session", "test-user", "miro", "access-token")
	require.NoError(t, err, "a deactivated server is not a failure of the callback")

	_, pooled := a.connPool.Get("test-session", "miro")
	assert.False(t, pooled, "no session connection is pooled")
	authenticated, _ := a.authStore.IsAuthenticated(context.Background(), "test-session", "miro")
	assert.False(t, authenticated, "no auth mark is written")
	assert.Empty(t, service.updates, "the service state is not flipped")
}

func TestHandleAuthCompletion_ActiveServerConnects(t *testing.T) {
	// The control: for an active server the callback still tries to connect
	// (and fails here, against a closed port), so the suspension check does
	// not swallow the connection step.
	a := newSuspensionTestServer(t)
	registerSuspendableServer(t, a.registry, "miro", false, api.StateAuthRequired)
	am := &AggregatorManager{aggregatorServer: a}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := am.handleAuthCompletion(ctx, "test-session", "test-user", "miro", "access-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to establish connection")
}

func TestDetermineSessionAuthStatus_DownServerIsDisconnected(t *testing.T) {
	// The session's auth mark and cached capabilities survive a stop so that
	// the tools come back when the server does. While it is down, the tool
	// list withholds the server (#1162) and the status must say the same.
	a := newSuspensionTestServer(t)
	_, service := registerSuspendableServer(t, a.registry, "miro", true, api.StateDisconnected)
	require.NoError(t, a.authStore.MarkAuthenticated(context.Background(), "test-session", "miro"))
	info := getServerInfo(t, a.registry, "miro")

	assert.Equal(t, pkgoauth.SessionServerStatusDisconnected,
		a.determineSessionAuthStatus("test-user", "test-session", "miro", info),
		"a down server is disconnected even for a session holding the auth mark")

	service.state = api.StateUnreachable
	assert.Equal(t, pkgoauth.SessionServerStatusUnreachable,
		a.determineSessionAuthStatus("test-user", "test-session", "miro", info),
		"unreachable keeps its own status")

	service.state = api.StateAuthRequired
	assert.Equal(t, pkgoauth.SessionServerStatusConnected,
		a.determineSessionAuthStatus("test-user", "test-session", "miro", info),
		"once the server is back the mark counts again — no new sign-in needed")
}

func TestHandleAuthStatusResource_SuspendedServer(t *testing.T) {
	a := newSuspensionTestServer(t)
	manager, service := registerSuspendableServer(t, a.registry, "miro", true, api.StateDisconnected)
	require.NoError(t, a.authStore.MarkAuthenticated(context.Background(), "test-session", "miro"))

	status := readServerAuthStatus(t, a, "miro")
	assert.Equal(t, pkgoauth.SessionServerStatusDisconnected, status.Status)
	assert.True(t, status.Suspended, "the payload names the reason")
	assert.Empty(t, status.AuthTool, "no sign-in is offered for a deactivated server")
	assert.Equal(t, 1, manager.lists, "the spec is read once per status read while a server is down")

	// Activated again: the flag goes, the mark counts, and the spec is not
	// read at all while every server is up.
	manager.servers["miro"] = api.MCPServerInfo{Name: "miro", Type: "streamable-http"}
	service.state = api.StateAuthRequired
	status = readServerAuthStatus(t, a, "miro")
	assert.Equal(t, pkgoauth.SessionServerStatusConnected, status.Status)
	assert.False(t, status.Suspended)
	assert.Equal(t, 1, manager.lists, "no spec read when no server is down")
}

func TestListServersRequiringAuth_SkipsDownServer(t *testing.T) {
	// list_tools must not send an agent to core_auth_login for a server the
	// tool refuses.
	a := newSuspensionTestServer(t)
	registerSuspendableServer(t, a.registry, "miro", true, api.StateDisconnected)
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "github", ToolPrefix: "github"},
		URL:                "https://github.example.com/mcp",
		AuthInfo:           &AuthInfo{Issuer: "https://idp.example.com"},
		AuthConfig:         &api.MCPServerAuth{Type: "oauth"},
	}))

	servers := a.ListServersRequiringAuth(testSessionCtx())
	require.Len(t, servers, 1)
	assert.Equal(t, "github", servers[0].Name, "the down server is left out, the active one stays")
}

// readServerAuthStatus reads auth://status as the test session and returns the
// named server's entry.
func readServerAuthStatus(t *testing.T, a *AggregatorServer, name string) pkgoauth.ServerAuthStatus {
	t.Helper()
	result, err := a.handleAuthStatusResource(testSessionCtx(), mcp.ReadResourceRequest{})
	require.NoError(t, err)
	text, ok := result[0].(mcp.TextResourceContents)
	require.True(t, ok)
	var response pkgoauth.AuthStatusResponse
	require.NoError(t, json.Unmarshal([]byte(text.Text), &response))
	for _, status := range response.Servers {
		if status.Name == name {
			return status
		}
	}
	t.Fatalf("server %q not in auth://status", name)
	return pkgoauth.ServerAuthStatus{}
}

func TestEventHandler_SkipsRegistrationForPerSessionAuthServers(t *testing.T) {
	// A server that signs its sessions in through core_auth_login has a
	// pending-auth registry entry and a service without a client. The
	// Connected state a sign-in syncs to the service must not make the event
	// handler attempt a global registration: that attempt failed with
	// "no MCP client available (service state inconsistent)" and emitted a
	// ToolsUnavailable event on every sign-in.
	provider := newMockOrchestratorAPI()
	callbacks := newMockCallbacks()
	isAuthRequired := func(serverName string) bool { return serverName == "oauth-server" }

	handler := NewEventHandler(provider, callbacks.register, callbacks.deregister, isAuthRequired, callbacks.isSSOBased)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, handler.Start(ctx))
	defer func() { _ = handler.Stop() }()

	provider.sendEvent(api.ServiceStateChangedEvent{
		Name: "oauth-server", ServiceType: "MCPServer", OldState: "auth_required", NewState: "connected", Health: "healthy",
	})
	provider.sendEvent(api.ServiceStateChangedEvent{
		Name: "regular-server", ServiceType: "MCPServer", OldState: "connecting", NewState: "connected", Health: "healthy",
	})

	// Events are processed in order: the regular server's callback proves the
	// OAuth server's event was processed — and skipped — before it.
	require.NoError(t, callbacks.waitForCallbacks(1, 5*time.Second))
	assert.Equal(t, []string{"regular-server"}, callbacks.getRegisteredServers())
}
