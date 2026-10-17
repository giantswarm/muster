package testing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/metatools"
	"github.com/giantswarm/muster/v5/internal/toolset"
)

// lateFamilyClient answers describe_tool for one family tool with the members
// registered so far; the members after the first register when the test says.
type lateFamilyClient struct {
	MCPTestClient
	tool       string
	registered []string
}

func (c *lateFamilyClient) CallToolDirect(_ context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if name != metatools.ToolDescribeTool || args["name"] != c.tool || len(c.registered) == 0 {
		return mcp.NewToolResultError("Tool not found"), nil
	}
	tool := mcp.Tool{Name: c.tool, Description: "List pods"}
	toolset.SetFamilyMembers(&tool, "management_cluster", c.registered)
	detail, err := json.Marshal(map[string]any{"name": tool.Name, "description": tool.Description})
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(detail)), nil
}

func familyServer(name string) MCPServerConfig {
	return MCPServerConfig{Name: name, Config: map[string]interface{}{
		"family": map[string]interface{}{"name": "kubernetes", "instanceArg": "management_cluster"},
		"tools": []interface{}{map[string]interface{}{
			"name":         "list_pods",
			"description":  "List pods",
			"input_schema": map[string]interface{}{"type": "object"},
		}},
	}}
}

// TestReadinessWaitsForLateFamilyMembers covers a three-member family whose
// members after the first register late: the family tool is listed from the
// first registration on, so readiness must keep waiting until its trailer
// names every member the pre-configuration contributes it from.
func TestReadinessWaitsForLateFamilyMembers(t *testing.T) {
	m := &musterInstanceManager{logger: NewSilentLogger(false, false)}
	tools, families := m.extractExpectedToolsWithHTTPMocks(&MusterPreConfiguration{
		MCPServers: []MCPServerConfig{familyServer("k8s-a"), familyServer("k8s-b"), familyServer("k8s-c")},
	}, nil)
	const tool = "x_kubernetes_list_pods"
	require.Equal(t, map[string][]string{tool: {"k8s-a", "k8s-b", "k8s-c"}}, families)

	client := &lateFamilyClient{tool: tool}
	ctx := context.Background()

	missing, err := findMissingFamilyMembers(ctx, client, families)
	require.NoError(t, err)
	require.Equal(t, []string{tool + " on [k8s-a k8s-b k8s-c]"}, missing, "an unlisted tool misses every member")

	client.registered = []string{"k8s-a"}
	require.Empty(t, m.findMissingTools(tools, []string{tool}), "the tool name is listed from the first member on")
	missing, err = findMissingFamilyMembers(ctx, client, families)
	require.NoError(t, err)
	require.Equal(t, []string{tool + " on [k8s-b k8s-c]"}, missing, "the late members still gate readiness")

	client.registered = []string{"k8s-a", "k8s-c"}
	missing, err = findMissingFamilyMembers(ctx, client, families)
	require.NoError(t, err)
	require.Equal(t, []string{tool + " on [k8s-b]"}, missing)

	client.registered = []string{"k8s-a", "k8s-b", "k8s-c"}
	missing, err = findMissingFamilyMembers(ctx, client, families)
	require.NoError(t, err)
	require.Empty(t, missing, "ready once every member is registered")
}

// TestReadinessExpectsNoFamilyMembersForPerServerTools keeps a family that
// falls back to per-server prefixing out of the member wait: its tools are
// exposed per server and carry no trailer.
func TestReadinessExpectsNoFamilyMembersForPerServerTools(t *testing.T) {
	m := &musterInstanceManager{logger: NewSilentLogger(false, false)}
	divergent := familyServer("k8s-b")
	divergent.Config["family"] = map[string]interface{}{"name": "kubernetes", "instanceArg": "cluster"}
	tools, families := m.extractExpectedToolsWithHTTPMocks(&MusterPreConfiguration{
		MCPServers: []MCPServerConfig{familyServer("k8s-a"), divergent},
	}, nil)
	require.Equal(t, []string{"x_k8s-a_list_pods", "x_k8s-b_list_pods"}, tools)
	require.Empty(t, families)
}
