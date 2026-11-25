package metatools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
)

// connectorAdminManager is an MCPServer manager that answers
// IsConnectorAdmin from a fixed set, counting the reviews.
type connectorAdminManager struct {
	api.MCPServerManagerHandler
	admins  map[string]bool
	failing map[string]bool
	reviews map[string]int
}

func (m *connectorAdminManager) IsConnectorAdmin(_ context.Context, server string) (bool, error) {
	m.reviews[server]++
	if m.failing[server] {
		return true, errors.New("review failed")
	}
	return m.admins[server], nil
}

func approved(tool mcp.Tool, approval api.ToolApproval) mcp.Tool {
	api.SetToolApproval(&tool, approval)
	return tool
}

func approvalCatalogue() *mockMetaToolsHandler {
	ask := api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceConnector, New: true}
	allow := api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceTool}
	return &mockMetaToolsHandler{tools: []mcp.Tool{
		approved(tagged("x_github_get_issue", toolset.ToolOrigin{Kind: toolset.OriginKindTool, Server: "github", Tool: "get_issue"}), allow),
		approved(tagged("x_github_merge", toolset.ToolOrigin{Kind: toolset.OriginKindTool, Server: "github", Tool: "merge"}), ask),
		approved(tagged("x_kubernetes_list_pods", toolset.ToolOrigin{Kind: toolset.OriginKindTool, Server: "kubernetes", Servers: []string{"k8s-a", "k8s-b"}, InstanceArg: "cluster", Tool: "list_pods"}), allow),
		approved(tagged("core_config_save", toolset.ToolOrigin{Kind: toolset.OriginKindCore}), api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceBuiltin}),
		tagged("workflow_triage", toolset.ToolOrigin{Kind: toolset.OriginKindWorkflow}),
	}}
}

func registerConnectorAdmins(t *testing.T, admins, failing map[string]bool) *connectorAdminManager {
	t.Helper()
	manager := &connectorAdminManager{admins: admins, failing: failing, reviews: map[string]int{}}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })
	return manager
}

func TestFilterTools_ReportsApprovalAndConnectorAdmin(t *testing.T) {
	defer registerMockHandler(approvalCatalogue())()
	manager := registerConnectorAdmins(t, map[string]bool{"github": true, "k8s-a": true}, nil)
	p := NewProvider()

	resp := listTools(t, p, map[string]any{"limit": 10})
	byName := map[string]ToolInfo{}
	for _, info := range resp.Tools {
		byName[info.Name] = info
	}

	assert.Equal(t, &api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceTool}, byName["x_github_get_issue"].Approval)
	assert.Equal(t, &api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceConnector, New: true}, byName["x_github_merge"].Approval)
	assert.Equal(t, &api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceBuiltin}, byName["core_config_save"].Approval)
	assert.Nil(t, byName["workflow_triage"].Approval)

	require.NotNil(t, byName["x_github_get_issue"].ConnectorAdmin)
	assert.True(t, *byName["x_github_get_issue"].ConnectorAdmin)
	require.NotNil(t, byName["x_kubernetes_list_pods"].ConnectorAdmin)
	assert.False(t, *byName["x_kubernetes_list_pods"].ConnectorAdmin, "a family tool needs every member")
	assert.Nil(t, byName["core_config_save"].ConnectorAdmin)
	assert.Nil(t, byName["workflow_triage"].ConnectorAdmin)

	assert.Equal(t, 1, manager.reviews["github"], "each server is reviewed once per request")
}

func TestFilterTools_ApprovalWireFormat(t *testing.T) {
	defer registerMockHandler(approvalCatalogue())()
	registerConnectorAdmins(t, map[string]bool{"github": true}, nil)
	p := NewProvider()

	result, err := p.ExecuteTool(withHeader("", false), "filter_tools", map[string]any{"pattern": "x_github_merge"})
	require.NoError(t, err)
	var raw struct {
		Tools []map[string]any `json:"tools"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(string)), &raw))
	require.Len(t, raw.Tools, 1)
	assert.Equal(t, map[string]any{"setting": "ask", "source": "connector", "new": true}, raw.Tools[0]["approval"])
	assert.Equal(t, true, raw.Tools[0]["connectorAdmin"])
}

func TestFilterTools_FailedReviewIsNoConnectorAdmin(t *testing.T) {
	defer registerMockHandler(approvalCatalogue())()
	registerConnectorAdmins(t, nil, map[string]bool{"github": true})
	resp := filterTools(t, NewProvider(), map[string]any{"pattern": "x_github_get_issue"})
	require.Len(t, resp.Tools, 1)
	require.NotNil(t, resp.Tools[0].ConnectorAdmin)
	assert.False(t, *resp.Tools[0].ConnectorAdmin)
}

func TestFilterTools_NoConnectorAdminWithoutAReviewer(t *testing.T) {
	defer registerMockHandler(approvalCatalogue())()
	api.RegisterMCPServerManager(nil)
	resp := filterTools(t, NewProvider(), map[string]any{"pattern": "x_github_get_issue"})
	require.Len(t, resp.Tools, 1)
	assert.Nil(t, resp.Tools[0].ConnectorAdmin)
	assert.NotNil(t, resp.Tools[0].Approval)
}

func TestDescribeTool_ReportsApprovalAndConnectorAdmin(t *testing.T) {
	defer registerMockHandler(approvalCatalogue())()
	registerConnectorAdmins(t, map[string]bool{"github": false}, nil)
	p := NewProvider()

	result, err := p.ExecuteTool(withHeader("", false), "describe_tool", map[string]any{"name": "x_github_merge"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	var resp DescribeToolResponse
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(string)), &resp))
	assert.Equal(t, &api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceConnector, New: true}, resp.Approval)
	require.NotNil(t, resp.ConnectorAdmin)
	assert.False(t, *resp.ConnectorAdmin)

	result, err = p.ExecuteTool(withHeader("", false), "describe_tool", map[string]any{"name": "workflow_triage"})
	require.NoError(t, err)
	resp = DescribeToolResponse{}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(string)), &resp))
	assert.Nil(t, resp.Approval)
	assert.Nil(t, resp.ConnectorAdmin)
}
