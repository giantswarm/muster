package api

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMCPServerKeyKeepsTodaysNames: a server of muster's namespace, or one
// without a namespace (filesystem mode), keeps its bare name as its key and
// its exposed prefix; a server outside it always carries its namespace.
func TestMCPServerKeyKeepsTodaysNames(t *testing.T) {
	assert.Equal(t, "github", MCPServerKey("agent-platform", "github", "agent-platform"))
	assert.Equal(t, "github", MCPServerKey("", "github", "agent-platform"))
	assert.Equal(t, "org-a/github", MCPServerKey("org-a", "github", "agent-platform"))

	ns, name := SplitMCPServerKey("org-a/github")
	assert.Equal(t, "org-a", ns)
	assert.Equal(t, "github", name)
	ns, name = SplitMCPServerKey("github")
	assert.Empty(t, ns)
	assert.Equal(t, "github", name)

	ns, name = MCPServerObject("github", "agent-platform")
	assert.Equal(t, "agent-platform", ns)
	assert.Equal(t, "github", name)
	ns, name = MCPServerObject("org-a/github", "agent-platform")
	assert.Equal(t, "org-a", ns)
	assert.Equal(t, "github", name)

	assert.False(t, IsTenantMCPServerKey("github"))
	assert.True(t, IsTenantMCPServerKey("org-a/github"))
}

func TestValidateServerNamespace(t *testing.T) {
	const muster = "agent-platform"
	tests := []struct {
		name       string
		namespace  string
		server     string
		toolPrefix string
		family     *MCPServerFamily
		auth       *MCPServerAuth
		refused    error
		mentions   string
	}{
		{name: "server for everyone", namespace: muster, server: "github"},
		{name: "SSO in muster's namespace", namespace: muster, server: "kubernetes", auth: &MCPServerAuth{ForwardToken: true, RequiredAudiences: []string{"dex-k8s-authenticator"}}},
		{name: "tenant server", namespace: "org-a", server: "github", toolPrefix: "gh", auth: &MCPServerAuth{Type: MCPServerAuthTypeOAuth}},
		{name: "tenant server with an org- name", namespace: "org-a", server: "org-a-github"},
		{name: "tenant requiredAudiences", namespace: "org-a", server: "x", auth: &MCPServerAuth{RequiredAudiences: []string{"dex-k8s-authenticator"}}, refused: ErrIdentityOutsideNamespace, mentions: "auth.requiredAudiences"},
		{name: "tenant forwardToken", namespace: "org-a", server: "x", auth: &MCPServerAuth{ForwardToken: true}, refused: ErrIdentityOutsideNamespace, mentions: "auth.forwardToken"},
		{name: "tenant forwardIdentity", namespace: "org-a", server: "x", auth: &MCPServerAuth{ForwardIdentity: true}, refused: ErrIdentityOutsideNamespace, mentions: "auth.forwardIdentity"},
		{name: "tenant tokenExchange", namespace: "org-a", server: "x", auth: &MCPServerAuth{TokenExchange: &TokenExchangeConfig{Enabled: true}}, refused: ErrIdentityOutsideNamespace, mentions: "auth.tokenExchange"},
		{name: "tenant sigv4", namespace: "org-a", server: "x", auth: &MCPServerAuth{Type: MCPServerAuthTypeSigV4}, refused: ErrIdentityOutsideNamespace, mentions: "auth.type sigv4"},
		{name: "tenant family", namespace: "org-a", server: "x", family: &MCPServerFamily{Name: "kubernetes", InstanceArg: "cluster"}, refused: ErrIdentityOutsideNamespace, mentions: "family"},
		{name: "every refused field is named", namespace: "org-a", server: "x", auth: &MCPServerAuth{ForwardToken: true, RequiredAudiences: []string{"a"}}, refused: ErrIdentityOutsideNamespace, mentions: "auth.requiredAudiences, auth.forwardToken are not allowed in namespace \"org-a\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateServerNamespace(tt.namespace, muster, tt.family, tt.auth)
			if tt.refused == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.refused)
			require.ErrorContains(t, err, tt.mentions)
		})
	}
}

// TestCollectRequiredAudiencesSkipsTenantServers: a server outside muster's
// namespace adds no audience to any login, even while its refused definition
// is still listed.
func TestCollectRequiredAudiencesSkipsTenantServers(t *testing.T) {
	sso := &MCPServerAuth{ForwardToken: true, RequiredAudiences: []string{"dex-k8s-authenticator"}}
	tenant := &MCPServerAuth{ForwardToken: true, RequiredAudiences: []string{"tenant-audience"}}
	RegisterMCPServerManager(&audienceManager{servers: []MCPServerInfo{
		{Name: "kubernetes", Namespace: "agent-platform", Key: "kubernetes", Auth: sso},
		{Name: "kubernetes", Namespace: "org-a", Key: "org-a/kubernetes", Auth: tenant},
	}})
	t.Cleanup(func() { RegisterMCPServerManager(nil) })

	audiences, err := CollectRequiredAudiences(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"dex-k8s-authenticator"}, audiences)
}

// TestCollectRequiredAudiencesAddsTheLoginAudienceOfVisibility: when muster
// lists servers as the caller, every login asks for the API server's
// audience, so the session's ID token is accepted there (D9).
func TestCollectRequiredAudiencesAddsTheLoginAudienceOfVisibility(t *testing.T) {
	RegisterMCPServerManager(&audienceManager{})
	t.Cleanup(func() { RegisterMCPServerManager(nil) })
	RegisterMCPServerVisibility(loginAudience("dex-k8s-authenticator"))
	t.Cleanup(func() { RegisterMCPServerVisibility(nil) })

	audiences, err := CollectRequiredAudiences(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"dex-k8s-authenticator"}, audiences)
}

type loginAudience string

func (a loginAudience) CallerVisibility(context.Context) ServerVisibility { return AllServersVisible() }
func (a loginAudience) LoginAudience() string                             { return string(a) }

type audienceManager struct {
	MCPServerManagerHandler
	servers []MCPServerInfo
}

func (m *audienceManager) ListMCPServers(context.Context) ([]MCPServerInfo, error) {
	return m.servers, nil
}

func TestShadowingServer(t *testing.T) {
	const muster = "agent-platform"
	server := func(name, toolPrefix, family string) MCPServerInfo {
		info := MCPServerInfo{Name: name, Namespace: muster, ToolPrefix: toolPrefix}
		if family != "" {
			info.Family = &MCPServerFamily{Name: family, InstanceArg: "cluster"}
		}
		return info
	}
	tests := []struct {
		name      string
		namespace string
		servers   []MCPServerInfo
		want      string
	}{
		{name: "a name equal to the namespace", namespace: "team-a", servers: []MCPServerInfo{server("team-a", "", "")}, want: "team-a"},
		{name: "a toolPrefix equal to the namespace", namespace: "team-a", servers: []MCPServerInfo{server("github", "team-a", "")}, want: "github"},
		{name: "a toolPrefix under the namespace", namespace: "team-a", servers: []MCPServerInfo{server("github", "team-a_github", "")}, want: "github"},
		{name: "a family name under the namespace", namespace: "team-a", servers: []MCPServerInfo{server("kubernetes", "", "team-a_k8s")}, want: "kubernetes"},
		{name: "a name the namespace only starts with", namespace: "team-a", servers: []MCPServerInfo{server("team", "", "")}},
		{name: "a name that only starts with the namespace", namespace: "team-a", servers: []MCPServerInfo{server("team-ab", "", "")}},
		{name: "a toolPrefix set beside a shadowing name", namespace: "team-a", servers: []MCPServerInfo{server("team-a", "gh", "")}},
		{name: "a server of another namespace", namespace: "team-a", servers: []MCPServerInfo{{Name: "team-a", Namespace: "team-b"}}},
		{name: "a server without a namespace (filesystem mode)", namespace: "team-a", servers: []MCPServerInfo{{Name: "team-a"}}, want: "team-a"},
		{name: "the first by name of two", namespace: "team-a", servers: []MCPServerInfo{server("zeta", "team-a", ""), server("alpha", "team-a_x", "")}, want: "alpha"},
		{name: "muster's namespace is never shadowed", namespace: muster, servers: []MCPServerInfo{server(muster, "", "")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ShadowingServer(tt.namespace, muster, tt.servers)
			assert.Equal(t, tt.want != "", ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateNotShadowed(t *testing.T) {
	const muster = "agent-platform"
	list := func(servers ...MCPServerInfo) func(context.Context) ([]MCPServerInfo, error) {
		return func(context.Context) ([]MCPServerInfo, error) { return servers, nil }
	}
	err := ValidateNotShadowed(t.Context(), "team-a", muster, list(MCPServerInfo{Name: "team-a", Namespace: muster}))
	require.ErrorIs(t, err, ErrShadowedServer)
	require.ErrorContains(t, err, `the server "team-a" of muster's namespace "agent-platform" exposes names under "team-a_"`)

	require.NoError(t, ValidateNotShadowed(t.Context(), "team-a", muster, list(MCPServerInfo{Name: "github", Namespace: muster})))
	require.NoError(t, ValidateNotShadowed(t.Context(), muster, muster, list(MCPServerInfo{Name: muster, Namespace: muster})))

	failing := func(context.Context) ([]MCPServerInfo, error) { return nil, errors.New("apiserver down") }
	require.ErrorContains(t, ValidateNotShadowed(t.Context(), "team-a", muster, failing), "apiserver down")
}
