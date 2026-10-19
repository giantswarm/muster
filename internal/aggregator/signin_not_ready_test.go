package aggregator

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/oauth"
)

// While muster cannot apply a tenant server's pin (its client Secret is not
// readable to muster), core_auth_login says the server's sign-in is not
// ready and why -- as the listings show the server -- instead of "not
// found" (giantswarm/agentlab#907). A server the caller may not see, or
// one that does not exist, is still "not found"; a server of muster's
// namespace keeps its answer.
func TestHandleAuthLogin_SignInNotReady(t *testing.T) {
	const notFound = "Server '%[1]s' not found. Use list_tools to see available servers."
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "fixture-client", nil)
	pinned := &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
		Issuer:                     "https://idp.example.com",
		ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{Name: "fixture-client"},
	}}
	tests := []struct {
		name       string
		server     string
		registered bool
		defined    bool
		sees       []string
		want       string // %[1]s is the server
	}{
		{name: "tenant, listed, not registered", server: "org-b/fixture", defined: true, sees: []string{"org-b"},
			want: "Cannot sign in to '%[1]s': the server's sign-in is not ready: load client credentials for server %[1]s: muster may not read the Secret org-b/fixture-client of the pin"},
		{name: "tenant, registered", server: "org-b/fixture", registered: true, defined: true, sees: []string{"org-b"},
			want: "Cannot sign in to '%[1]s': the server's sign-in is not ready: load client credentials for server %[1]s: muster may not read the Secret org-b/fixture-client of the pin"},
		{name: "tenant the caller may not see", server: "org-b/fixture", defined: true, sees: []string{"org-a"}, want: notFound},
		{name: "tenant that does not exist", server: "org-b/fixture", sees: []string{"org-b"}, want: notFound},
		{name: "muster's namespace, registered", server: "fixture", registered: true, defined: true,
			want: "Cannot authenticate to '%[1]s': load client credentials for server %[1]s: secrets \"fixture-client\" is forbidden"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newSuspensionTestServer(t)
			namespace, name := api.SplitMCPServerKey(tc.server)
			definitions := &stubMCPServerManager{servers: map[string]api.MCPServerInfo{}}
			if tc.defined {
				definitions.servers[tc.server] = api.MCPServerInfo{Name: name, Namespace: namespace, Key: tc.server, Auth: pinned, State: "auth_required"}
			}
			api.RegisterMCPServerManager(definitions)
			api.RegisterSecretCredentialsHandler(&mockSecretCredentialsHandler{err: forbidden})
			manager := oauth.NewManager(config.OAuthMCPClientConfig{Enabled: true, PublicURL: "https://muster.example.com", ClientID: "client-id", CallbackPath: "/oauth/proxy/callback"})
			t.Cleanup(manager.Stop)
			api.RegisterOAuthHandler(oauth.NewAdapter(manager))
			t.Cleanup(func() {
				api.RegisterMCPServerManager(nil)
				api.RegisterSecretCredentialsHandler(nil)
				api.RegisterOAuthHandler(nil)
			})
			if tc.sees != nil {
				useVisibility(t, api.ServersOfNamespacesVisible("muster", tc.sees))
			}
			if tc.registered {
				require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
					ServerRegistration: ServerRegistration{Name: tc.server, Namespace: namespace},
					URL:                "http://127.0.0.1:1/mcp",
					AuthConfig:         pinned,
				}))
			}

			result, err := NewAuthToolProvider(a).handleAuthLogin(testSessionCtx(), map[string]any{"server": tc.server})
			require.NoError(t, err)
			require.True(t, result.IsError)
			require.Len(t, result.Content, 1)
			want := fmt.Sprintf(tc.want, tc.server)
			if tc.want == notFound {
				assert.Equal(t, want, result.Content[0], "word for word as for a server that does not exist")
				return
			}
			assert.Contains(t, result.Content[0], want)
		})
	}
}
