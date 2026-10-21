package metatools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// The facts below fix agent mistakes seen in practice; each message says
// which. Reword a description freely, but keep the fact.

func metaTool(t *testing.T, name string) api.ToolMetadata {
	t.Helper()
	for _, tool := range NewProvider().GetTools() {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("no meta-tool %s", name)
	return api.ToolMetadata{}
}

// TestListToolsSaysItIsCostly: agents listed the whole catalogue, and every
// call grew the next prompt by 105k-120k tokens (#1193).
func TestListToolsSaysItIsCostly(t *testing.T) {
	d := metaTool(t, "list_tools").Description
	assert.Contains(t, d, "Costly", "list_tools lost its cost warning (#1193)")
	assert.Contains(t, d, "filter_tools", "list_tools no longer points to filter_tools for finding a tool (#1193)")
}

// TestFilterToolsNamesTheSignIn: an agent answered "I have no Slack tool"
// because filter_tools said nothing about servers awaiting a sign-in (#1351).
func TestFilterToolsNamesTheSignIn(t *testing.T) {
	d := metaTool(t, "filter_tools").Description
	assert.Contains(t, d, "servers_requiring_auth", "filter_tools lost the servers a sign-in would unlock (#1351)")
	assert.Contains(t, d, "auth_tool", "filter_tools no longer says how to start the sign-in (#1351)")
}

// TestCallToolNamesTheRoute: agents called aggregated tools by their own name
// (#1207) and lost call_tool's name after a long arguments object (#1361).
func TestCallToolNamesTheRoute(t *testing.T) {
	tool := metaTool(t, ToolCallTool)
	assert.Contains(t, tool.Description, "callable only this way", "call_tool lost that aggregated tools are reached only through it (#1207)")
	assert.Contains(t, tool.Description, "name first", "call_tool lost the argument order (#1361)")
	var name *api.ArgMetadata
	for i := range tool.Args {
		if tool.Args[i].Name == "name" {
			name = &tool.Args[i]
		}
	}
	require.NotNil(t, name)
	assert.Contains(t, name.Description, "before arguments", "call_tool's name lost the argument order (#1361)")
}
