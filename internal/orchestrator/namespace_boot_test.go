package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
)

// tenantServerInfo is a server of the namespace org-a, keyed as the MCPServer
// manager keys it.
func tenantServerInfo(auth *api.MCPServerAuth) api.MCPServerInfo {
	return api.MCPServerInfo{
		Name:      "github",
		Namespace: "org-a",
		Key:       "org-a/github",
		Type:      "streamable-http",
		URL:       "https://mcp.example.invalid/mcp",
		Auth:      auth,
		AutoStart: true,
	}
}

// TestBootRefusesWhatTheReconcilerRefuses: the boot pass and a lazy
// registration run before the reconciler's first pass, so a tenant server
// that names a Secret of muster's namespace, or asks for an identity, is not
// registered there either: it would connect once before the reconciler
// refuses it.
func TestBootRefusesWhatTheReconcilerRefuses(t *testing.T) {
	pin := &api.MCPServerAuth{
		Type: api.MCPServerAuthTypeOAuth,
		AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
			Issuer:                     "https://github.com/login/oauth",
			ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{Name: "github-app", Namespace: "agent-platform"},
		},
	}
	for _, tt := range []struct {
		name string
		info api.MCPServerInfo
		want error
	}{
		{name: "Secret of muster's namespace", info: tenantServerInfo(pin), want: api.ErrSecretOutsideNamespace},
		{name: "forwardToken", info: tenantServerInfo(&api.MCPServerAuth{ForwardToken: true}), want: api.ErrIdentityOutsideNamespace},
		{name: "org- name in muster's namespace", info: api.MCPServerInfo{Name: "org-a", Namespace: "agent-platform", Type: "streamable-http", URL: "https://mcp.example.invalid/mcp", AutoStart: true}, want: api.ErrReservedServerName},
	} {
		t.Run(tt.name, func(t *testing.T) {
			key := tt.info.ServerKey()
			manager := &mockMCPServerManager{servers: map[string]api.MCPServerInfo{key: tt.info}}
			api.RegisterMCPServerManager(manager)
			t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

			o := New(Config{Aggregator: config.AggregatorConfig{}, KubernetesMode: true, Namespace: "agent-platform"})
			require.NoError(t, o.Start(context.Background()))
			t.Cleanup(func() { _ = o.Stop() })

			_, exists := o.registry.Get(key)
			assert.False(t, exists, "a refused definition must not be registered at boot")

			err := o.StartService(key)
			require.ErrorIs(t, err, tt.want)
			_, exists = o.registry.Get(key)
			assert.False(t, exists, "a refused definition must not be registered lazily")
		})
	}
}

// TestBootRegistersATenantServerUnderItsKey: a tenant server without an
// identity setting is registered under "<namespace>/<name>", beside a server
// of the same name in muster's namespace.
func TestBootRegistersATenantServerUnderItsKey(t *testing.T) {
	own := tenantServerInfo(nil)
	own.Namespace, own.Key = "agent-platform", ""
	own.AutoStart = false
	tenant := tenantServerInfo(nil)
	tenant.AutoStart = false
	manager := &mockMCPServerManager{servers: map[string]api.MCPServerInfo{"github": own, "org-a/github": tenant}}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	o := New(Config{Aggregator: config.AggregatorConfig{}, KubernetesMode: true, Namespace: "agent-platform"})
	require.NoError(t, o.Start(context.Background()))
	t.Cleanup(func() { _ = o.Stop() })

	for _, key := range []string{"github", "org-a/github"} {
		svc, err := o.registerMCPServerFromDefinition(key)
		require.NoError(t, err)
		assert.Equal(t, key, svc.GetName())
	}
}

// TestFilesystemModeBootHasNoTenants: in filesystem mode every definition
// carries the namespace "default"; with muster's namespace set to another
// value, an SSO server is muster's own and is registered.
func TestFilesystemModeBootHasNoTenants(t *testing.T) {
	info := api.MCPServerInfo{Name: "kubernetes", Namespace: "default", Type: "streamable-http", URL: "https://mcp.example.invalid/mcp", Auth: &api.MCPServerAuth{ForwardToken: true}}
	manager := &mockMCPServerManager{servers: map[string]api.MCPServerInfo{"kubernetes": info}}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	o := New(Config{Aggregator: config.AggregatorConfig{}, Namespace: "team-alpha"})
	require.NoError(t, o.Start(context.Background()))
	t.Cleanup(func() { _ = o.Stop() })

	svc, err := o.registerMCPServerFromDefinition("kubernetes")
	require.NoError(t, err)
	assert.Equal(t, "kubernetes", svc.GetName())
}
