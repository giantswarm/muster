package aggregator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
)

// How a discovered description lives with the operator's pins, the
// definitions and the servers that come and go.

// countingDefinitions makes a's definitions the given ones and counts how
// often every MCPServer is looked at (describedServers).
func countingDefinitions(a *AggregatorServer, defined map[string]*ServerInfo) *int {
	calls := 0
	a.definedServers = func() map[string]*ServerInfo {
		calls++
		out := make(map[string]*ServerInfo, len(defined))
		for name, info := range defined {
			copied := &ServerInfo{Name: info.Name, URL: info.URL, AuthConfig: info.AuthConfig, CreatedAt: info.CreatedAt}
			a.rememberedDefinition(copied)
			out[name] = copied
		}
		return out
	}
	return &calls
}

func secretPinnedAuth(issuer string) *api.MCPServerAuth {
	auth := pinnedAuth(issuer, issuer+"/authorize", issuer+"/token")
	auth.AuthorizationServer.ClientCredentialsSecretRef = &api.ClientCredentialsSecretRef{Name: "operator-client"}
	return auth
}

func registerSecrets(t *testing.T) *mockSecretCredentialsHandler {
	t.Helper()
	secrets := &mockSecretCredentialsHandler{credentials: &api.ClientCredentials{ClientID: "operator-client", ClientSecret: "s3cr3t"}}
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(nil) })
	return secrets
}

// TestReleaseOfADiscoveredServerKeepsTheOperatorsPin: a discovered server
// that only went along with an operator's pin goes without re-reading the
// operator's client Secret, so an unreadable Secret no longer unpins the
// operator's working pin.
func TestReleaseOfADiscoveredServerKeepsTheOperatorsPin(t *testing.T) {
	ctx := context.Background()
	handler := newLookupPins(t)
	secrets := registerSecrets(t)
	a := bareAggregator()
	operator := registerDiscovered(t, a.registry, "operator", "", secretPinnedAuth(discoveredIssuer))
	require.NoError(t, a.pinAuthorizationServer(ctx, operator))
	require.Equal(t, "operator-client", handler.pins[discoveredIssuer].ClientID)
	connector := registerDiscovered(t, a.registry, "connector", discoveredIssuer, discoveredSubjectAuth())

	secrets.err = errors.New("secrets \"operator-client\" is forbidden")
	reads := secrets.loadCalls
	previous := serverDescription(connector)
	require.NoError(t, a.registry.Deregister("connector"))
	a.releaseAuthorizationServerDescription(ctx, "connector", previous)

	assert.Equal(t, "operator-client", handler.pins[discoveredIssuer].ClientID, "the operator's pin stands")
	assert.Empty(t, handler.unpinned)
	assert.Equal(t, reads, secrets.loadCalls, "the Secret is not read again")
}

// TestOperatorPinThatCannotBeAppliedWithdrawsTheDiscoveredPin: while an
// operator's description of an issuer cannot be pinned, a discovered pin
// does not stay standing under it.
func TestOperatorPinThatCannotBeAppliedWithdrawsTheDiscoveredPin(t *testing.T) {
	ctx := context.Background()
	handler := newLookupPins(t)
	secrets := registerSecrets(t)
	secrets.err = errors.New("secrets \"operator-client\" is forbidden")
	a := bareAggregator()
	connector := registerDiscovered(t, a.registry, "connector", discoveredIssuer, discoveredSubjectAuth())
	require.NoError(t, a.pinDiscoveredIssuer(ctx, connector))
	require.True(t, handler.pins[discoveredIssuer].SubjectScoped)

	operator := registerDiscovered(t, a.registry, "operator", "", secretPinnedAuth(discoveredIssuer))
	require.Error(t, a.pinAuthorizationServer(ctx, operator))

	_, still := handler.pins[discoveredIssuer]
	assert.False(t, still, "nothing stands under the operator's issuer until its pin can be applied")
	require.NoError(t, a.pinDiscoveredIssuer(ctx, connector))
	_, still = handler.pins[discoveredIssuer]
	assert.False(t, still, "the discovered server does not take the key back")
}

// TestDeletedDefinitionReleasesItsDiscoveredPin: a definition without a
// registry entry that held the discovered pin releases it when it is deleted.
func TestDeletedDefinitionReleasesItsDiscoveredPin(t *testing.T) {
	ctx := context.Background()
	handler := newLookupPins(t)
	a := newTestAggregatorWithPool(t)
	const url = "https://connector.example.com/mcp"
	a.discovered.remember("connector", url, &AuthInfo{Issuer: discoveredIssuer})
	defined := map[string]*ServerInfo{"connector": {Name: "connector", URL: url, AuthConfig: discoveredSubjectAuth()}}
	countingDefinitions(a, defined)
	require.NoError(t, a.pinDiscoveredIssuer(ctx, a.describedServers()["connector"]))
	require.True(t, handler.pins[discoveredIssuer].SubjectScoped)

	// The definition is deleted while it is still listed.
	_ = a.DeregisterServer("connector")

	_, still := handler.pins[discoveredIssuer]
	assert.False(t, still, "the deleted definition's issuer is released")
	assert.Nil(t, a.discovered.remembered("connector", url))
}

// TestGoneDiscoveredServerPinsNothing: a snapshot of a server that is neither
// registered nor defined any more -- a lookup or a listing that straddled its
// deletion -- does not pin its discovered issuer.
func TestGoneDiscoveredServerPinsNothing(t *testing.T) {
	handler := newLookupPins(t)
	a := bareAggregator()
	snapshot := registerDiscovered(t, a.registry, "connector", discoveredIssuer, discoveredSubjectAuth())
	require.NoError(t, a.registry.Deregister("connector"))

	require.NoError(t, a.pinDiscoveredIssuer(context.Background(), snapshot))
	assert.Empty(t, handler.pins)
}

// TestDiscoveredIssuerIsRecordedWithTheTenantOwner: the issuer key a tenant
// server's lookup learns goes into the server's owner record, so deleting a
// sibling does not sweep the person's grant this server's sessions use.
func TestDiscoveredIssuerIsRecordedWithTheTenantOwner(t *testing.T) {
	newLookupPins(t)
	backend, _ := newPRMBackend(t, discoveredIssuer)
	owners := oauthstore.NewInMemoryTenantOwnerStore()
	a := bareAggregator()
	a.tenantOwners = owners
	ctx := context.Background()
	require.NoError(t, owners.AddOwner(ctx, "org-a", "uid-b", oauthstore.TenantOwner{Server: "org-a/connector-b"}))
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "org-a/connector-b", Namespace: "org-a", UID: "uid-b"},
		URL:                backend.URL + "/mcp",
		AuthInfo:           &AuthInfo{},
		AuthConfig:         discoveredSubjectAuth(),
	}))

	a.lookupDiscoveredIssuer("org-a/connector-b", false)
	a.wg.Wait()

	recorded, err := owners.Owners(ctx, "org-a")
	require.NoError(t, err)
	assert.Contains(t, recorded["uid-b"].IssuerKeys, api.IssuerKey("org-a", discoveredIssuer))
}

// TestReconcileDiscoveredIssuer: the definitions' tick makes a replica that
// never learned a registered server's issuer look it up, and pins a known
// issuer that is not pinned in this process.
func TestReconcileDiscoveredIssuer(t *testing.T) {
	ctx := context.Background()
	handler := newLookupPins(t)
	backend, requests := newPRMBackend(t, discoveredIssuer)
	a := bareAggregator()
	registerBare(t, a, "connector", backend.URL+"/mcp", discoveredSubjectAuth())
	definition := &ServerInfo{Name: "connector", URL: backend.URL + "/mcp", AuthConfig: discoveredSubjectAuth()}

	a.reconcileDiscoveredIssuer(ctx, definition)
	a.wg.Wait()
	require.Equal(t, int32(1), requests.Load(), "the unknown issuer is looked up")
	require.True(t, handler.pins[discoveredIssuer].SubjectScoped)

	delete(handler.pins, discoveredIssuer)
	a.reconcileDiscoveredIssuer(ctx, definition)
	a.wg.Wait()
	assert.True(t, handler.pins[discoveredIssuer].SubjectScoped, "a known issuer is pinned again")
	assert.Equal(t, int32(1), requests.Load(), "without another lookup")
}

// TestDiscoveredDescriptionUnderAnotherPinLooksAtNoServer: under an
// operator's pin, and for a re-registration that describes the same, the
// hot paths answer without looking at every MCPServer.
func TestDiscoveredDescriptionUnderAnotherPinLooksAtNoServer(t *testing.T) {
	ctx := context.Background()
	newLookupPins(t)
	a := bareAggregator()
	calls := countingDefinitions(a, nil)
	operator := registerDiscovered(t, a.registry, "operator", "", pinnedAuth(discoveredIssuer, discoveredIssuer+"/authorize", discoveredIssuer+"/token"))
	require.NoError(t, a.pinAuthorizationServer(ctx, operator))
	connector := registerDiscovered(t, a.registry, "connector", discoveredIssuer, discoveredSubjectAuth())

	*calls = 0
	require.NoError(t, a.pinDiscoveredIssuer(ctx, connector))
	require.NoError(t, a.pinAuthorizationServer(ctx, connector))
	require.NoError(t, a.AuthorizationServerConflict("connector"))
	a.releaseAuthorizationServerDescription(ctx, "connector", serverDescription(connector))
	assert.Zero(t, *calls)
}

// TestLookupNotifiesLiveSessions: once the background lookup learns and pins
// a subject-scoped issuer, live sessions are told their lists changed, so a
// client that listed while the issuer was unknown lists again.
func TestLookupNotifiesLiveSessions(t *testing.T) {
	newDiscoveredGrantHandler(t)
	backend, _ := newPRMBackend(t, discoveredIssuer)
	a := bareAggregator()
	a.mcpServer = mcpserver.NewMCPServer("test", "1.0.0", mcpserver.WithToolCapabilities(true))
	a.subjectSessions = newSubjectSessionTracker()
	session := &downstreamSession{id: "alice-mcp-1", ch: make(chan mcp.JSONRPCNotification, 8)}
	require.NoError(t, a.mcpServer.RegisterSession(context.Background(), session))
	a.subjectSessions.Track("alice", session.id)
	registerBare(t, a, "connector", backend.URL+"/mcp", discoveredSubjectAuth())

	a.lookupDiscoveredIssuer("connector", false)
	a.wg.Wait()

	select {
	case notification := <-session.ch:
		assert.Equal(t, "notifications/tools/list_changed", notification.Method)
	case <-time.After(2 * time.Second):
		t.Fatal("no list_changed reached the live session")
	}
}

// forgettingGrantHandler records the grants it was asked to forget.
type forgettingGrantHandler struct {
	*discoveredGrantHandler
	forgotten []string
}

func (h *forgettingGrantHandler) ForgetSubjectGrant(userID, issuer string) {
	h.forgotten = append(h.forgotten, userID+"|"+issuer)
}

// TestHandleAuthLogout_DiscoveredServer: signing out of a server that
// discovers its subject-scoped issuer fails, revoking nothing, while muster
// cannot tell the issuer, and drops the person's grant also while an
// operator's session-scoped pin of the issuer stands.
func TestHandleAuthLogout_DiscoveredServer(t *testing.T) {
	t.Run("the issuer cannot be resolved", func(t *testing.T) {
		newDiscoveredGrantHandler(t)
		backend, _ := newPRMBackend(t, "")
		a := newTestAggregatorWithPool(t)
		registerBare(t, a, "connector", backend.URL+"/mcp", discoveredSubjectAuth())

		result, err := NewAuthToolProvider(a).handleAuthLogout(testSessionCtx(), map[string]any{"server": "connector"})
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0], "nothing was revoked")
	})

	t.Run("under an operator's session-scoped pin", func(t *testing.T) {
		handler := &forgettingGrantHandler{discoveredGrantHandler: &discoveredGrantHandler{subjectGrantMockHandler: newSubjectGrantMockHandler(), pins: map[string]api.IssuerPin{}}}
		api.RegisterOAuthHandler(handler)
		t.Cleanup(func() { api.RegisterOAuthHandler(nil) })
		a := newTestAggregatorWithPool(t)
		operator := registerDiscovered(t, a.registry, "operator", "", pinnedAuth(discoveredIssuer, discoveredIssuer+"/authorize", discoveredIssuer+"/token"))
		require.NoError(t, a.pinAuthorizationServer(context.Background(), operator))
		registerDiscoveredAt(t, a, "connector", "https://connector.example.com/mcp", discoveredIssuer, discoveredSubjectAuth())
		handler.grantFor("test-user", discoveredIssuer, subjectGrantToken, "https://connector.example.com/mcp")

		result, err := NewAuthToolProvider(a).handleAuthLogout(testSessionCtx(), map[string]any{"server": "connector"})
		require.NoError(t, err)
		require.False(t, result.IsError, "%v", result.Content)
		assert.Equal(t, []string{"test-user|" + discoveredIssuer}, handler.forgotten, "the grant the person filed through the connector goes")
	})
}

// TestResolveServerAuthInfo_IssuerLessBlockDiscovers: a block without an
// issuer pins none, so a login discovers the server's issuer from its
// metadata instead of failing the override.
func TestResolveServerAuthInfo_IssuerLessBlockDiscovers(t *testing.T) {
	newLookupPins(t)
	backend, _ := newPRMBackend(t, discoveredIssuer)
	a := bareAggregator()
	auth := discoveredSubjectAuth()
	auth.AuthorizationServer = &api.MCPServerAuthAuthorizationServer{Scopes: "read"}
	registerBare(t, a, "connector", backend.URL+"/mcp", auth)
	info, _ := a.registry.GetServerInfo("connector")

	authInfo, err := a.resolveServerAuthInfo(context.Background(), info)
	require.NoError(t, err)
	assert.Equal(t, discoveredIssuer, authInfo.Issuer)
}

// TestEffectiveSubjectScoped_WithoutAnIssuer: a server that says subject but
// neither pins nor discovers an issuer reports session.
func TestEffectiveSubjectScoped_WithoutAnIssuer(t *testing.T) {
	info := &ServerInfo{Name: "x", AuthConfig: &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, AuthorizationServer: &api.MCPServerAuthAuthorizationServer{GrantScope: api.GrantScopeSubject}}}
	require.True(t, info.AuthConfig.SubjectScoped())
	assert.False(t, effectiveSubjectScoped(info))
}
