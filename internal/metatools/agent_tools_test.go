package metatools

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
)

func TestParseAgentTools(t *testing.T) {
	settings, selectors, err := parseAgentTools(map[string]any{
		"tools":   []any{"server:k8s", map[string]any{"tool:x_gh_issues": "Allow"}, map[string]any{"server:gh": "Block"}},
		"default": "Ask",
		"grants":  []any{map[string]any{"entry": "tool:x_gh_issues", "server": "gh", "uid": "u1", "tools": []any{"issues"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"server:k8s", "tool:x_gh_issues", "server:gh"}, selectors)
	assert.True(t, settings.Reporting)
	assert.Equal(t, []api.AgentApprovalEntry{{Entry: "tool:x_gh_issues", Setting: "Allow"}, {Entry: "server:gh", Setting: "Block"}}, settings.Entries)
	assert.Equal(t, "Ask", settings.Default)
	assert.Equal(t, []api.AgentApprovalGrant{{Entry: "tool:x_gh_issues", Server: "gh", UID: "u1", Tools: []string{"issues"}}}, settings.Grants)

	bad := []any{
		"server:k8s",
		map[string]any{},
		map[string]any{"tools": []any{}},
		map[string]any{"tools": []any{map[string]any{"server:a": "Allow", "server:b": "Ask"}}},
		map[string]any{"tools": []any{map[string]any{"server:a": "Deny"}}},
		map[string]any{"tools": []any{map[string]any{"server:a": true}}},
		map[string]any{"tools": []any{42.0}},
		map[string]any{"tools": []any{"server:a"}, "default": "Never"},
		map[string]any{"tools": []any{"server:a"}, "extra": true},
		map[string]any{"tools": []any{"server:a"}, "grants": []any{map[string]any{"entry": "server:a", "server": "a", "uid": "u"}}},
	}
	for i, raw := range bad {
		_, _, err := parseAgentTools(raw)
		assert.Error(t, err, "case %d", i)
	}
}

// settingsRecorder is a handler that records the agent settings its
// listing was asked with.
type settingsRecorder struct {
	*mockMetaToolsHandler
	seen *api.AgentApproval
}

func (r *settingsRecorder) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	r.seen = api.AgentApprovalFromContext(ctx)
	return r.mockMetaToolsHandler.ListTools(ctx)
}

type connectorsManager struct {
	api.MCPServerManagerHandler
	servers []api.MCPServerInfo
}

func (m connectorsManager) ListMCPServers(context.Context) ([]api.MCPServerInfo, error) {
	return m.servers, nil
}

// filter_tools with agentTools narrows to the agent's selectors, lists with
// the agent's settings for reporting, and names each server tool's
// connectors; toolset and agentTools together are refused.
func TestFilterTools_AgentTools(t *testing.T) {
	recorder := &settingsRecorder{mockMetaToolsHandler: toolsetFixture()}
	api.RegisterMetaTools(recorder)
	t.Cleanup(func() { api.RegisterMetaTools(nil) })
	api.RegisterMCPServerManager(connectorsManager{servers: []api.MCPServerInfo{{Name: "k8s", Namespace: "muster", UID: "u-k8s"}}})
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })
	p := NewProvider()

	args := map[string]any{"limit": float64(10), ArgAgentTools: map[string]any{
		"tools": []any{"server:k8s", map[string]any{"tool:x_gh_issues": "Block"}}, "default": "Ask",
	}}
	result, err := p.ExecuteTool(context.Background(), "filter_tools", args)
	require.NoError(t, err)
	resp := decode(t, result)
	assert.Equal(t, []string{"x_k8s_get", "x_k8s_delete", "x_gh_issues"}, toolNames(t, resp))
	require.NotNil(t, recorder.seen)
	assert.True(t, recorder.seen.Reporting)
	assert.Equal(t, "Ask", recorder.seen.Default)

	assert.Equal(t, map[string]any{"entries": 2.0, "settings": 1.0}, resp["agentTools"], "the answer says it evaluated the list (M2-S3-C-07)")
	tools := resp["tools"].([]any)
	first := tools[0].(map[string]any)
	assert.Equal(t, []any{map[string]any{"server": "k8s", "namespace": "muster", "name": "k8s", "uid": "u-k8s"}}, first["connectors"])
	third := tools[2].(map[string]any)
	assert.Equal(t, []any{map[string]any{"server": "gh"}}, third["connectors"], "an unknown server is named by its key")

	result, err = p.ExecuteTool(context.Background(), "filter_tools", map[string]any{"toolset": []any{"server:k8s"}, ArgAgentTools: map[string]any{"tools": []any{"server:k8s"}}})
	require.NoError(t, err)
	assert.Contains(t, errorText(t, result), "not both")

	result, err = p.ExecuteTool(context.Background(), "filter_tools", map[string]any{})
	require.NoError(t, err)
	assert.Nil(t, recorder.seen, "without agentTools the listing carries no settings")
	assert.NotContains(t, decode(t, result), "agentTools")
	for _, tool := range decode(t, result)["tools"].([]any) {
		assert.NotContains(t, tool.(map[string]any), "connectors")
	}

	result, err = p.ExecuteTool(context.Background(), "describe_tool", map[string]any{"name": "x_k8s_get", ArgAgentTools: map[string]any{"tools": []any{"server:k8s"}}})
	require.NoError(t, err)
	assert.Equal(t, []any{map[string]any{"server": "k8s", "namespace": "muster", "name": "k8s", "uid": "u-k8s"}}, decode(t, result)["connectors"])
}

// A preset: entry resolves with the installation's presets: includes minus
// excludes, the read-only predicate from the tool's annotations.
func TestPresetSelects(t *testing.T) {
	p := NewProvider()
	yes := true
	readTool := mcp.Tool{Name: "x_k8s_get", Annotations: mcp.ToolAnnotation{ReadOnlyHint: &yes}}
	writeTool := mcp.Tool{Name: "x_k8s_delete"}
	target := func(tool mcp.Tool) api.AgentTarget {
		return api.AgentTarget{Kind: api.AgentTargetTool, Server: "k8s", Original: tool.Name[len("x_k8s_"):], Exposed: tool.Name, Tool: &tool}
	}
	assert.True(t, p.presetSelects(context.Background(), "read-only", target(readTool)))
	assert.False(t, p.presetSelects(context.Background(), "read-only", target(writeTool)))
	assert.False(t, p.presetSelects(context.Background(), "unknown", target(readTool)))
}

// The connectors carry the server's own name of the tool, what a grant
// names (M2-S3-S-06).
func TestConnectorLookupNamesTheServersTool(t *testing.T) {
	api.RegisterMCPServerManager(connectorsManager{servers: []api.MCPServerInfo{{Name: "k8s", Namespace: "muster", UID: "u-k8s"}}})
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })
	tool := tagged("x_k8s_get", toolset.ToolOrigin{Kind: toolset.OriginKindTool, Server: "k8s", Tool: "get"})
	assert.Equal(t, []ConnectorRef{{Server: "k8s", Namespace: "muster", Name: "k8s", UID: "u-k8s", Tool: "get"}}, connectorLookup(context.Background())(tool))
}

// An agent caller cannot list what its own binding hides with agentTools
// (M2-S3-S-07).
func TestAgentToolsRefusedToAgents(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()
	agent := api.WithSessionID(context.Background(), api.ActorSessionPrefix+"abc")
	for _, tool := range []string{"filter_tools", "describe_tool"} {
		result, err := p.ExecuteTool(agent, tool, map[string]any{"name": "x_k8s_get", ArgAgentTools: map[string]any{"tools": []any{"server:k8s"}}})
		require.NoError(t, err)
		assert.Contains(t, errorText(t, result), "agentTools is for people", tool)
	}
}
