package aggregator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// registerAppPinnedServer registers an auth-required MCPServer pinned to the
// shared subject-scoped issuer of these tests, as two managers pinned to one
// GitHub App are.
func registerAppPinnedServer(t *testing.T, a *AggregatorServer, name, url string) {
	t.Helper()
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: name, ToolPrefix: name},
		URL:                url,
		AuthInfo:           &AuthInfo{Issuer: subjectGrantIssuerURL, Scope: "repo"},
		AuthConfig: &api.MCPServerAuth{
			Type: "oauth",
			AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
				Issuer:     subjectGrantIssuerURL,
				GrantScope: api.GrantScopeSubject,
			},
		},
	}))
}

func sessionServerStatuses(response pkgoauth.AuthStatusResponse) map[string]pkgoauth.SessionServerStatus {
	statuses := make(map[string]pkgoauth.SessionServerStatus, len(response.Servers))
	for _, server := range response.Servers {
		statuses[server.Name] = server.Status
	}
	return statuses
}

// The status reports a server connected as soon as the person holds a usable
// grant for its issuer, whichever server's sign-in created it: a session that
// signed in to one of two servers sharing an App issuer is not asked to sign
// in to the other.
func TestSessionAuthStatus_ReportsAHeldGrantOfASharedIssuerConnected(t *testing.T) {
	backend, _ := newGrantBackend(t, subjectGrantToken)
	a := newTestAggregatorWithPool(t)
	registerAppPinnedServer(t, a, "workspace-manager", backend.URL)
	registerAppPinnedServer(t, a, "toolchain-manager", backend.URL)

	handler := newSubjectGrantMockHandler()
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	ctx := sessionContext("sess-portal", "alice")
	require.Equal(t, map[string]pkgoauth.SessionServerStatus{
		"workspace-manager": pkgoauth.SessionServerStatusAuthRequired,
		"toolchain-manager": pkgoauth.SessionServerStatusAuthRequired,
	}, sessionServerStatuses(a.sessionAuthStatus(ctx)))

	// The workspace-manager sign-in files the person's grant under the App's
	// issuer and marks only its own server.
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	require.NoError(t, a.authStore.MarkAuthenticated(ctx, "sess-portal", "workspace-manager"))

	require.Equal(t, map[string]pkgoauth.SessionServerStatus{
		"workspace-manager": pkgoauth.SessionServerStatusConnected,
		"toolchain-manager": pkgoauth.SessionServerStatusConnected,
	}, sessionServerStatuses(a.sessionAuthStatus(ctx)))
}

// A grant the backend refuses is no usable grant: the server stays
// auth_required.
func TestSessionAuthStatus_RefusedGrantStaysAuthRequired(t *testing.T) {
	backend, _ := newGrantBackend(t, "ghu_someone_else")
	a := newTestAggregatorWithPool(t)
	registerAppPinnedServer(t, a, "toolchain-manager", backend.URL)

	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	statuses := sessionServerStatuses(a.sessionAuthStatus(sessionContext("sess-portal", "alice")))
	require.Equal(t, pkgoauth.SessionServerStatusAuthRequired, statuses["toolchain-manager"])
}
