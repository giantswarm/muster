package aggregator

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
)

// registerApprovals registers an MCPServer manager that knows the given
// servers' approval settings (nil: a server without spec.approval).
func registerApprovals(t *testing.T, approvals map[string]*api.MCPServerApproval) *stubMCPServerManager {
	t.Helper()
	manager := &stubMCPServerManager{servers: map[string]api.MCPServerInfo{}}
	for name, approval := range approvals {
		manager.servers[name] = api.MCPServerInfo{Name: name, Type: "streamable-http", Approval: approval}
	}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })
	return manager
}

func approvalOf(t *testing.T, tools []mcp.Tool, name string) (api.ToolApproval, bool) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			approval, ok := api.ToolApprovalOf(tool)
			require.True(t, ok, "%s carries no approval", name)
			// Whether muster can ask here has its own test
			// (TestApplyApprovals_ReportsWhyAskingIsUnavailable).
			approval.Unavailable = ""
			return approval, true
		}
	}
	return api.ToolApproval{}, false
}

func approvalSoloTool(server, original string) mcp.Tool {
	tool := mcp.Tool{Name: "x_" + server + "_" + original}
	toolset.SetToolOrigin(&tool, toolset.ToolOrigin{Kind: toolset.OriginKindTool, Server: server, Tool: original})
	return tool
}

func approvalFamilyTool(family, original string, members ...string) mcp.Tool {
	tool := mcp.Tool{Name: "x_" + family + "_" + original, InputSchema: mcp.ToolInputSchema{Type: "object"}}
	toolset.SetFamilyMembers(&tool, "cluster", members)
	toolset.SetToolOrigin(&tool, toolset.ToolOrigin{Kind: toolset.OriginKindTool, Server: family, Servers: members, InstanceArg: "cluster", Tool: original})
	return tool
}

func approvalCoreTool(name string, kind toolset.OriginKind) mcp.Tool {
	tool := mcp.Tool{Name: name}
	toolset.SetToolOrigin(&tool, toolset.ToolOrigin{Kind: kind})
	return tool
}

func TestApplyApprovals(t *testing.T) {
	a := &AggregatorServer{registry: NewServerRegistry("x")}
	ctx := context.Background()

	registerApprovals(t, map[string]*api.MCPServerApproval{
		"github": {Rule: api.ApprovalAsk, Tools: map[string]string{
			"get_issue": api.ApprovalAllow, "delete_repository": api.ApprovalBlock, "odd": "Deny",
		}},
		"pagerduty": nil,
		"k8s-a":     {Rule: api.ApprovalAllow, Tools: map[string]string{"delete_pod": api.ApprovalBlock, "drain": api.ApprovalBlock}},
		"k8s-b":     {Rule: api.ApprovalAllow, Tools: map[string]string{"delete_pod": api.ApprovalAsk, "drain": api.ApprovalBlock}},
	})

	tools := a.applyApprovals(ctx, []mcp.Tool{
		approvalSoloTool("github", "get_issue"),
		approvalSoloTool("github", "merge_pull_request"),
		approvalSoloTool("github", "delete_repository"),
		approvalSoloTool("github", "odd"),
		approvalSoloTool("pagerduty", "ack"),
		approvalSoloTool("unknown", "anything"),
		approvalFamilyTool("kubernetes", "list_pods", "k8s-a", "k8s-b"),
		approvalFamilyTool("kubernetes", "delete_pod", "k8s-a", "k8s-b"),
		approvalFamilyTool("kubernetes", "drain", "k8s-a", "k8s-b"),
		approvalCoreTool("core_config_save", toolset.OriginKindCore),
		approvalCoreTool("core_mcpserver_list", toolset.OriginKindCore),
		approvalCoreTool("workflow_triage", toolset.OriginKindWorkflow),
	})

	tests := []struct {
		name string
		want api.ToolApproval
	}{
		{"x_github_get_issue", api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceTool}},
		{"x_github_merge_pull_request", api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceConnector, New: true}},
		{"x_github_odd", api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceTool}},
		{"x_pagerduty_ack", api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceUnreviewed}},
		// A registered server whose MCPServer the manager does not know is
		// not Allow: its settings cannot be known.
		{"x_unknown_anything", api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceUnavailable}},
		{"x_kubernetes_list_pods", api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceConnector, New: true}},
		{"x_kubernetes_delete_pod", api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceTool}},
		{"core_config_save", api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceBuiltin}},
		{"core_mcpserver_list", api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceUnreviewed}},
	}
	for _, tt := range tests {
		got, found := approvalOf(t, tools, tt.name)
		require.True(t, found, "%s missing from the catalogue", tt.name)
		assert.Equal(t, tt.want, got, tt.name)
	}

	_, found := approvalOf(t, tools, "x_github_delete_repository")
	assert.False(t, found, "a blocked tool leaves the catalogue")
	_, found = approvalOf(t, tools, "x_kubernetes_drain")
	assert.False(t, found, "a family tool every member blocks leaves the catalogue")

	for _, tool := range tools {
		switch tool.Name {
		case "x_kubernetes_delete_pod":
			origin, _ := toolset.ToolOriginOf(tool)
			assert.Equal(t, []string{"k8s-b"}, origin.Servers, "the member that blocks it is dropped")
			enum := tool.InputSchema.Properties["cluster"].(map[string]any)["enum"]
			assert.Equal(t, []any{"k8s-b"}, enum, "the instance argument no longer offers it")
		case "workflow_triage":
			_, ok := api.ToolApprovalOf(tool)
			assert.False(t, ok, "workflows carry no approval yet")
		}
	}
}

func TestApplyApprovals_SettingsUnreadable(t *testing.T) {
	a := &AggregatorServer{registry: NewServerRegistry("x")}
	manager := registerApprovals(t, map[string]*api.MCPServerApproval{
		"github": {Tools: map[string]string{"delete_repository": api.ApprovalBlock}},
	})
	manager.err = errors.New("apiserver unavailable")

	tools := a.applyApprovals(context.Background(), []mcp.Tool{
		approvalSoloTool("github", "delete_repository"),
		approvalCoreTool("core_mcpserver_list", toolset.OriginKindCore),
	})
	got, found := approvalOf(t, tools, "x_github_delete_repository")
	require.True(t, found, "unreadable settings hide nothing")
	assert.Equal(t, api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceUnavailable}, got,
		"never reported as allow")
	assert.Equal(t, 1, manager.lists, "the servers are listed once per catalogue")
}

func TestApplyApprovals_NoManager(t *testing.T) {
	a := &AggregatorServer{registry: NewServerRegistry("x")}
	api.RegisterMCPServerManager(nil)
	tools := a.applyApprovals(context.Background(), []mcp.Tool{approvalSoloTool("github", "get_issue")})
	got, _ := approvalOf(t, tools, "x_github_get_issue")
	assert.Equal(t, api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceUnreviewed}, got)
}

func TestCallToolInternal_RefusesBlockedTools(t *testing.T) {
	ctx := context.Background()
	newServer := func(t *testing.T) (*AggregatorServer, *recordingMCPClient) {
		t.Helper()
		server, err := NewAggregatorServer(ctx, AggregatorConfig{Host: "localhost", Port: 0}, nil)
		require.NoError(t, err)
		client := &recordingMCPClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{
			{Name: "get_issue"}, {Name: "delete_repository"},
		}}}
		require.NoError(t, server.RegisterServer(ctx, ServerRegistration{Name: "github"}, client))
		_ = server.registry.GetAllTools() // prime the name mapping
		return server, client
	}

	t.Run("a blocked tool is refused as not found and never reaches the server", func(t *testing.T) {
		server, client := newServer(t)
		registerApprovals(t, map[string]*api.MCPServerApproval{
			"github": {Tools: map[string]string{"delete_repository": api.ApprovalBlock}},
		})
		_, err := server.CallToolInternal(ctx, "x_github_delete_repository", map[string]any{})
		require.EqualError(t, err, "tool not found: x_github_delete_repository")
		assert.Empty(t, client.lastName)

		_, err = server.CallToolInternal(ctx, "x_github_get_issue", map[string]any{})
		require.NoError(t, err)
		assert.Equal(t, "get_issue", client.lastName, "allow and ask still run: only block is enforced")
	})

	t.Run("a server without settings runs everything", func(t *testing.T) {
		server, client := newServer(t)
		registerApprovals(t, map[string]*api.MCPServerApproval{"github": nil})
		_, err := server.CallToolInternal(ctx, "x_github_delete_repository", map[string]any{})
		require.NoError(t, err)
		assert.Equal(t, "delete_repository", client.lastName)
	})

	t.Run("a change of the settings reaches the next call", func(t *testing.T) {
		server, client := newServer(t)
		manager := registerApprovals(t, map[string]*api.MCPServerApproval{"github": nil})
		_, err := server.CallToolInternal(ctx, "x_github_delete_repository", map[string]any{})
		require.NoError(t, err)
		client.lastName = ""

		manager.servers["github"] = api.MCPServerInfo{Name: "github", Approval: &api.MCPServerApproval{
			Tools: map[string]string{"delete_repository": api.ApprovalBlock},
		}}
		_, err = server.CallToolInternal(ctx, "x_github_delete_repository", map[string]any{})
		require.Error(t, err)
		assert.Empty(t, client.lastName)
	})

	t.Run("unreadable settings refuse the call", func(t *testing.T) {
		server, client := newServer(t)
		manager := registerApprovals(t, map[string]*api.MCPServerApproval{"github": nil})
		manager.err = errors.New("apiserver unavailable")
		_, err := server.CallToolInternal(ctx, "x_github_get_issue", map[string]any{})
		require.ErrorContains(t, err, "approval settings of server github could not be read")
		assert.Empty(t, client.lastName)
	})

	t.Run("a family member that blocks the tool is refused, the others run", func(t *testing.T) {
		server, err := NewAggregatorServer(ctx, AggregatorConfig{Host: "localhost", Port: 0}, nil)
		require.NoError(t, err)
		family := &api.MCPServerFamily{Name: "kubernetes", InstanceArg: "cluster"}
		clientA := &recordingMCPClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{{Name: "delete_pod"}}}}
		clientB := &recordingMCPClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{{Name: "delete_pod"}}}}
		require.NoError(t, server.RegisterServer(ctx, ServerRegistration{Name: "k8s-a", Family: family}, clientA))
		require.NoError(t, server.RegisterServer(ctx, ServerRegistration{Name: "k8s-b", Family: family}, clientB))
		_ = server.registry.GetAllTools()
		registerApprovals(t, map[string]*api.MCPServerApproval{
			"k8s-a": {Tools: map[string]string{"delete_pod": api.ApprovalBlock}},
			"k8s-b": nil,
		})

		_, err = server.CallToolInternal(ctx, "x_kubernetes_delete_pod", map[string]any{"cluster": "k8s-a"})
		require.EqualError(t, err, "tool not found: x_kubernetes_delete_pod")
		assert.Empty(t, clientA.lastName)

		_, err = server.CallToolInternal(ctx, "x_kubernetes_delete_pod", map[string]any{"cluster": "k8s-b"})
		require.NoError(t, err)
		assert.Equal(t, "delete_pod", clientB.lastName)
	})
}

func TestListToolsForContext_HidesBlockedTools(t *testing.T) {
	ctx := api.WithSessionID(context.Background(), "session-approval")
	server, err := NewAggregatorServer(ctx, AggregatorConfig{Host: "localhost", Port: 0}, nil)
	require.NoError(t, err)
	client := &recordingMCPClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{
		{Name: "get_issue"}, {Name: "delete_repository"},
	}}}
	require.NoError(t, server.RegisterServer(ctx, ServerRegistration{Name: "github"}, client))
	registerApprovals(t, map[string]*api.MCPServerApproval{
		"github": {Rule: api.ApprovalAsk, Tools: map[string]string{"delete_repository": api.ApprovalBlock}},
	})

	tools := server.ListToolsForContext(ctx)
	got, found := approvalOf(t, tools, "x_github_get_issue")
	require.True(t, found)
	assert.Equal(t, api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceConnector, New: true}, got)
	_, found = approvalOf(t, tools, "x_github_delete_repository")
	assert.False(t, found)
}

// Regression: a string and the boolean, number or
// object it spells are shown differently, with their JSON type.
func TestLabelledArguments_DistinguishTypes(t *testing.T) {
	strs := labelledArguments(mcp.ToolInputSchema{}, map[string]any{"force": "false", "n": "1", "o": `{"a":1}`})
	typed := labelledArguments(mcp.ToolInputSchema{}, map[string]any{"force": false, "n": 1.0, "o": map[string]any{"a": 1.0}})
	assert.NotEqual(t, strs, typed)
	assert.Equal(t, []api.ApprovalArgument{
		{Key: "force", Label: "Force", Value: `"false"`, Type: "string"},
		{Key: "n", Label: "N", Value: `"1"`, Type: "string"},
		{Key: "o", Label: "O", Value: `"{\"a\":1}"`, Type: "string"},
	}, strs)
	assert.Equal(t, []api.ApprovalArgument{
		{Key: "force", Label: "Force", Value: `false`, Type: "boolean"},
		{Key: "n", Label: "N", Value: `1`, Type: "number"},
		{Key: "o", Label: "O", Value: `{"a":1}`, Type: "object"},
	}, typed)
}

// Labels made from keys read as words.
func TestReadable(t *testing.T) {
	for in, want := range map[string]string{
		"merge_pull_request": "Merge pull request",
		"dryRun":             "Dry run",
		"adminUsers":         "Admin users",
		"systemMessage":      "System message",
		"pr":                 "Pr",
		"toolPrefix2Name":    "Tool prefix2 name",
		"":                   "",
	} {
		assert.Equal(t, want, readable(in), in)
	}
}
