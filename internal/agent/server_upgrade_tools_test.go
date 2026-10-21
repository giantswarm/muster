package agent

import (
	"testing"

	"github.com/giantswarm/mcp-toolkit/tooldesc"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/metatools"
)

func agentTools(t *testing.T) map[string]*server.ServerTool {
	t.Helper()
	m := &MCPServer{mcpServer: server.NewMCPServer("muster-agent", "test")}
	registerAgentTools(m)
	return m.mcpServer.ListTools()
}

// TestAgentToolsAreTheProvidersMetaTools: the local agent lists every
// meta-tool with the provider's description and annotations.
func TestAgentToolsAreTheProvidersMetaTools(t *testing.T) {
	tools := agentTools(t)
	provided := metatools.NewProvider().GetTools()
	require.Len(t, tools, len(provided))
	for _, meta := range provided {
		st, ok := tools[meta.Name]
		require.True(t, ok, "the agent does not list %s", meta.Name)
		assert.Equal(t, meta.Description, st.Tool.Description, meta.Name)
		assert.Equal(t, api.MCPToolAnnotation(meta.Annotations), st.Tool.Annotations, meta.Name)
	}
	assert.Contains(t, tools[metatools.ToolCallTool].Tool.InputSchema.Properties, callTimeoutArg.Name,
		"call_tool through the agent takes its timeout")
}

// TestAgentToolDescriptionBudgets holds the agent's tools, call_tool's
// timeout included, to the budget of giantswarm/giantswarm#38592.
func TestAgentToolDescriptionBudgets(t *testing.T) {
	var tools []mcp.Tool
	for _, st := range agentTools(t) {
		tools = append(tools, st.Tool)
	}
	require.NoError(t, tooldesc.Check(tools, tooldesc.DefaultBudget))
}
