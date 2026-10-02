package cmd

import (
	"testing"

	"github.com/giantswarm/muster/v5/internal/cli"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompleteNames covers the completion of get resource|prompt: a URI the
// listing carries once per server is offered once.
func TestCompleteNames(t *testing.T) {
	names := []string{"proof://docs/readme", "file:///shared", "proof://notes/todo", "file:///shared", "auth://status"}

	assert.Equal(t, []string{"proof://docs/readme", "proof://notes/todo"}, completeNames(names, "PROOF"))
	assert.Equal(t, []string{"auth://status", "file:///shared", "proof://docs/readme", "proof://notes/todo"}, completeNames(names, ""))
	assert.Nil(t, completeNames(names, "x_"))
}

// TestGetDetailShowsServer covers the server an aggregated resource and
// prompt are shown with.
func TestGetDetailShowsServer(t *testing.T) {
	resource := cli.MCPResourceInfo{MCPResource: cli.MCPResource{URI: "proof://docs/readme", Name: "readme"}, Server: "docs"}
	out := captureStdout(t, func() {
		require.NoError(t, cli.FormatMCPResourceDetail(resource, cli.OutputFormatTable))
	})
	assert.Contains(t, out, "Server:       docs")

	out = captureStdout(t, func() {
		require.NoError(t, cli.FormatMCPResourceDetail(cli.MCPResourceInfo{MCPResource: cli.MCPResource{URI: "auth://status"}}, cli.OutputFormatTable))
	})
	assert.NotContains(t, out, "Server:", "muster's own resource has no server")

	prompt := cli.MCPPromptInfo{
		MCPPrompt: cli.MCPPrompt{Name: "x_docs_summarise", Arguments: []mcp.PromptArgument{{Name: "topic", Required: true}}},
		Server:    "docs",
	}
	out = captureStdout(t, func() {
		require.NoError(t, cli.FormatMCPPromptDetail(prompt, cli.OutputFormatJSON))
	})
	assert.Contains(t, out, `"server": "docs"`)
	assert.Contains(t, out, `"name": "topic"`)
}
