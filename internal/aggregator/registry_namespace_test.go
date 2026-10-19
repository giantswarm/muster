package aggregator

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerRegistry_TenantServersCarryTheirNamespace: a server called github
// in muster's namespace and one in org-a are both registered. The first keeps
// today's tool names; the second's carry its namespace, also with a
// toolPrefix, and each exposed name resolves to its own server.
func TestServerRegistry_TenantServersCarryTheirNamespace(t *testing.T) {
	ctx := context.Background()
	registry := NewServerRegistry("x")
	tools := func() *mockMCPClient { return &mockMCPClient{tools: []mcp.Tool{{Name: "create_issue"}}} }

	require.NoError(t, registry.Register(ctx, ServerRegistration{Name: "github", Namespace: "agent-platform"}, tools()))
	require.NoError(t, registry.Register(ctx, ServerRegistration{Name: "org-a/github", Namespace: "org-a"}, tools()))
	require.NoError(t, registry.Register(ctx, ServerRegistration{Name: "org-b/github", Namespace: "org-b", ToolPrefix: "gh"}, tools()))

	var names []string
	for _, tool := range registry.GetAllTools() {
		names = append(names, tool.Name)
	}
	assert.ElementsMatch(t, []string{"x_github_create_issue", "x_org-a_github_create_issue", "x_org-b_gh_create_issue"}, names)

	for exposed, want := range map[string]string{
		"x_github_create_issue":       "github",
		"x_org-a_github_create_issue": "org-a/github",
		"x_org-b_gh_create_issue":     "org-b/github",
	} {
		server, original, err := registry.ResolveToolName(exposed)
		require.NoError(t, err)
		assert.Equal(t, want, server)
		assert.Equal(t, "create_issue", original)
	}

	assert.Equal(t, "x_org-a_github_", registry.ExposedToolPrefix("org-a/github"))
	assert.Equal(t, []string{"org-a/github"}, registry.ServersInNameSpaceOf("x_org-a_github_create_issue", nil))
}
