package metatools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// filterResult runs filter_tools and parses the typed response.
func filterResult(t *testing.T, args map[string]interface{}) FilterToolsResponse {
	t.Helper()
	provider := NewProvider()
	result, err := provider.ExecuteTool(context.Background(), "filter_tools", args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "filter_tools returned an error: %v", result.Content)

	var resp FilterToolsResponse
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(string)), &resp))
	return resp
}

// makeWorkflowTools builds n synthetic workflow tools with verbose descriptions,
// mimicking the ~280-workflow fleet described in muster#868.
func makeWorkflowTools(n int) []mcp.Tool {
	tools := make([]mcp.Tool, 0, n)
	verbose := strings.Repeat("This step has detailed stop-rules, output budgeting, and healthy-state guidance. ", 12)
	for i := 0; i < n; i++ {
		tools = append(tools, mcp.Tool{
			Name:        fmt.Sprintf("workflow_deploy_app_%03d", i),
			Description: fmt.Sprintf("Deploy application number %d to a cluster.\n%s", i, verbose),
			InputSchema: mcp.ToolInputSchema{Type: "object"},
		})
	}
	return tools
}

func TestFilterTools_LimitOffsetAndTruncation(t *testing.T) {
	mock := &mockMetaToolsHandler{tools: makeWorkflowTools(280)}
	defer registerMockHandler(mock)()

	resp := filterResult(t, map[string]interface{}{"pattern": "*workflow*"})

	assert.Equal(t, 280, resp.TotalTools)
	assert.Equal(t, 280, resp.Total, "all 280 match the pattern")
	assert.Len(t, resp.Tools, defaultFilterLimit, "page is capped to the default limit")
	assert.Equal(t, defaultFilterLimit, resp.FilteredCount)
	assert.True(t, resp.Truncated, "more matches exist beyond the page")

	// Explicit limit + offset paginates.
	resp = filterResult(t, map[string]interface{}{"limit": float64(10), "offset": float64(275)})
	assert.Len(t, resp.Tools, 5, "only 5 tools remain past offset 275")
	assert.False(t, resp.Truncated, "the final page is not truncated")
}

func TestFilterTools_DiscoveryDefaultsToSummaries(t *testing.T) {
	mock := &mockMetaToolsHandler{tools: makeWorkflowTools(3)}
	defer registerMockHandler(mock)()

	resp := filterResult(t, map[string]interface{}{"pattern": "*workflow*"})
	for _, tool := range resp.Tools {
		assert.NotEmpty(t, tool.Summary, "discovery emits a one-line summary")
		assert.Empty(t, tool.Description, "discovery omits the full description by default")
		assert.Nil(t, tool.InputSchema, "discovery omits the schema by default")
		assert.LessOrEqual(t, len([]rune(tool.Summary)), summaryMaxLen+3, "summary is length-capped")
		assert.NotContains(t, tool.Summary, "\n", "summary is a single line")
	}

	// Opting into schema restores full description + schema.
	resp = filterResult(t, map[string]interface{}{"pattern": "*workflow*", "include_schema": true})
	for _, tool := range resp.Tools {
		assert.NotEmpty(t, tool.Description)
		assert.Empty(t, tool.Summary)
		assert.NotNil(t, tool.InputSchema)
	}
}

func TestFilterTools_RankedQuery(t *testing.T) {
	mock := &mockMetaToolsHandler{tools: []mcp.Tool{
		{Name: "core_service_restart", Description: "Restart a running service."},
		{Name: "prometheus_query_range", Description: "Query Prometheus metrics over a time range."},
		{Name: "core_workflow_list", Description: "List all workflows."},
		{Name: "grafana_dashboard_get", Description: "Fetch a Grafana monitoring dashboard."},
	}}
	defer registerMockHandler(mock)()

	resp := filterResult(t, map[string]interface{}{"query": "prometheus metrics query"})

	require.NotEmpty(t, resp.Tools, "query returns the relevant tool(s)")
	assert.Less(t, len(resp.Tools), 4, "irrelevant tools are dropped, not the whole catalogue")
	assert.Equal(t, "prometheus_query_range", resp.Tools[0].Name, "best match ranks first")
	assert.Greater(t, resp.Tools[0].Score, 0.0, "ranked results carry a score")
	// Scores are monotonically non-increasing.
	for i := 1; i < len(resp.Tools); i++ {
		assert.GreaterOrEqual(t, resp.Tools[i-1].Score, resp.Tools[i].Score)
	}
}

func TestFilterTools_LabelFacets(t *testing.T) {
	withLabels := func(name, desc string, labels map[string]string) mcp.Tool {
		return mcp.Tool{
			Name:        name,
			Description: desc,
			Meta:        &mcp.Meta{AdditionalFields: map[string]any{api.MetaKeyLabels: labels}},
		}
	}
	mock := &mockMetaToolsHandler{tools: []mcp.Tool{
		withLabels("workflow_deploy", "Deploy.", map[string]string{"category": "delivery"}),
		withLabels("workflow_observe", "Observe.", map[string]string{"category": "observability"}),
		{Name: "workflow_plain", Description: "No labels."},
	}}
	defer registerMockHandler(mock)()

	resp := filterResult(t, map[string]interface{}{
		"labels": map[string]interface{}{"category": "observability"},
	})

	require.Len(t, resp.Tools, 1)
	assert.Equal(t, "workflow_observe", resp.Tools[0].Name)
	assert.Equal(t, "observability", resp.Tools[0].Labels["category"], "matched labels are surfaced")
}

// TestFilterTools_BoundedDiscoveryIsOrdersOfMagnitudeSmaller is the AC#4
// token/byte measurement: a broad workflow-discovery call against a
// ~280-workflow fleet must return a bounded result that is orders of magnitude
// smaller than the full-catalogue dump (the legacy include_schema=true shape).
func TestFilterTools_BoundedDiscoveryIsOrdersOfMagnitudeSmaller(t *testing.T) {
	mock := &mockMetaToolsHandler{tools: makeWorkflowTools(280)}
	defer registerMockHandler(mock)()

	provider := NewProvider()
	ctx := context.Background()

	// Legacy full-catalogue dump: every match, full descriptions + schemas.
	full, err := provider.ExecuteTool(ctx, "filter_tools", map[string]interface{}{
		"pattern": "*workflow*", "include_schema": true, "limit": float64(1000),
	})
	require.NoError(t, err)
	fullBytes := len(full.Content[0].(string))

	// Cheap discovery: default bounded, summarised page.
	discovery, err := provider.ExecuteTool(ctx, "filter_tools", map[string]interface{}{
		"pattern": "*workflow*",
	})
	require.NoError(t, err)
	discoveryBytes := len(discovery.Content[0].(string))

	t.Logf("full-catalogue dump: %d bytes; bounded discovery: %d bytes (%.1fx smaller)",
		fullBytes, discoveryBytes, float64(fullBytes)/float64(discoveryBytes))

	assert.Less(t, discoveryBytes*20, fullBytes,
		"bounded discovery must be at least 20x smaller than the full-catalogue dump")
}
