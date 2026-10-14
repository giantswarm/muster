package api

import (
	"context"
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
		{name: "org- name in muster's namespace", namespace: muster, server: "org-a-github", refused: ErrReservedServerName, mentions: "name"},
		{name: "org- toolPrefix in muster's namespace", namespace: muster, server: "github", toolPrefix: "org-a", refused: ErrReservedServerName, mentions: "toolPrefix"},
		{name: "org- name in filesystem mode", server: "org-a", refused: ErrReservedServerName},
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
			err := ValidateServerNamespace(tt.namespace, muster, tt.server, tt.toolPrefix, tt.family, tt.auth)
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

type audienceManager struct {
	MCPServerManagerHandler
	servers []MCPServerInfo
}

func (m *audienceManager) ListMCPServers(context.Context) ([]MCPServerInfo, error) {
	return m.servers, nil
}
