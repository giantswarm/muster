package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/giantswarm/muster/v5/internal/metatools"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAttributeResources covers the server a listed resource is attributed
// to: one entry per server exposing a URI, the resource taken from the native
// listing where it has one.
func TestAttributeResources(t *testing.T) {
	listed := []metatools.ResourceInfo{
		{URI: "file:///readme", Name: "readme", Description: "readme", Server: "files"},
		{URI: "file:///readme", Name: "readme", Description: "readme", Server: "files_x"},
		{URI: "docs://guide", Name: "guide", Description: "The guide", MIMEType: "text/markdown", Server: "docs"},
	}
	native := []MCPResource{
		{URI: "file:///readme", Name: "readme", MIMEType: "text/plain"},
		{URI: "auth://status", Name: "Authentication status"},
	}

	got := attributeResources(listed, native)

	require.Len(t, got, 4)
	assert.Equal(t, "files", got[0].Server)
	assert.Equal(t, "files_x", got[1].Server)
	assert.Equal(t, "", got[0].Description, "the native resource is shown, not the meta-tool's name fallback")
	assert.Equal(t, "text/plain", got[1].MIMEType)
	assert.Equal(t, MCPResource{URI: "docs://guide", Name: "guide", Description: "The guide", MIMEType: "text/markdown"}, got[2].MCPResource)
	assert.Equal(t, "docs", got[2].Server)
	assert.Equal(t, MCPResourceInfo{MCPResource: native[1]}, got[3], "muster's own resource is kept without a server")
}

// TestAttributePrompts covers the server a listed prompt is attributed to and
// the arguments joined from the native listing.
func TestAttributePrompts(t *testing.T) {
	listed := []metatools.PromptInfo{
		{Name: "x_pp_triage", Description: "Triage", Server: "promptserver"},
		{Name: "x_files_summarise", Description: "Summarise", Server: "files"},
	}
	native := []MCPPrompt{{
		Name:        "x_pp_triage",
		Description: "Triage",
		Arguments:   []mcp.PromptArgument{{Name: "issue", Required: true}},
	}, {Name: "own"}}

	got := attributePrompts(listed, native)

	require.Len(t, got, 3)
	assert.Equal(t, "promptserver", got[0].Server)
	require.Len(t, got[0].Arguments, 1)
	assert.Equal(t, "issue", got[0].Arguments[0].Name)
	assert.Equal(t, "files", got[1].Server)
	assert.Equal(t, "Summarise", got[1].Description)
	assert.Empty(t, got[1].Arguments)
	assert.Equal(t, MCPPromptInfo{MCPPrompt: MCPPrompt{Name: "own"}}, got[2], "a prompt only the native listing has is kept without a server")
}

// describeCatalogue fakes describe_resource and describe_prompt over one
// aggregated server, docs; muster's own items are not in it.
func describeCatalogue(_ context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	switch {
	case name == metatools.ToolDescribeResource && args["uri"] == "proof://docs/readme":
		return mcp.NewToolResultText(`{"uri":"proof://docs/readme","name":"readme","mimeType":"text/markdown","server":"docs"}`), nil
	case name == metatools.ToolDescribeResource:
		return mcp.NewToolResultError("Resource not found: " + args["uri"].(string)), nil
	case name == metatools.ToolDescribePrompt && args["name"] == "x_docs_summarise":
		return mcp.NewToolResultText(`{"name":"x_docs_summarise","description":"Summarise","server":"docs","arguments":[{"name":"topic","required":true}]}`), nil
	case name == metatools.ToolDescribePrompt:
		return mcp.NewToolResultError("Prompt not found: " + args["name"].(string)), nil
	}
	return nil, fmt.Errorf("unexpected tool %s", name)
}

// TestFindResource covers get resource: an aggregated resource comes from
// describe_resource with its server, muster's own from the native listing,
// which carries none of the aggregated ones.
func TestFindResource(t *testing.T) {
	ctx := context.Background()
	native := func(context.Context) ([]MCPResource, error) {
		return []MCPResource{{URI: "auth://status", Name: "Authentication status"}}, nil
	}

	got, err := findResource(ctx, describeCatalogue, native, "proof://docs/readme", "")
	require.NoError(t, err)
	assert.Equal(t, &MCPResourceInfo{MCPResource: MCPResource{URI: "proof://docs/readme", Name: "readme", MIMEType: "text/markdown"}, Server: "docs"}, got)

	got, err = findResource(ctx, describeCatalogue, native, "auth://status", "")
	require.NoError(t, err)
	assert.Equal(t, &MCPResourceInfo{MCPResource: MCPResource{URI: "auth://status", Name: "Authentication status"}}, got)

	got, err = findResource(ctx, describeCatalogue, native, "auth://status", "docs")
	require.NoError(t, err)
	assert.Nil(t, got, "muster's own resource belongs to no server")

	got, err = findResource(ctx, describeCatalogue, native, "proof://none", "")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// TestFindPrompt covers get prompt: an aggregated prompt comes from
// describe_prompt with its server and arguments.
func TestFindPrompt(t *testing.T) {
	ctx := context.Background()
	native := func(context.Context) ([]MCPPrompt, error) {
		return []MCPPrompt{{Name: "own", Description: "muster's own"}}, nil
	}

	got, err := findPrompt(ctx, describeCatalogue, native, "x_docs_summarise")
	require.NoError(t, err)
	assert.Equal(t, &MCPPromptInfo{
		MCPPrompt: MCPPrompt{Name: "x_docs_summarise", Description: "Summarise", Arguments: []mcp.PromptArgument{{Name: "topic", Required: true}}},
		Server:    "docs",
	}, got)

	got, err = findPrompt(ctx, describeCatalogue, native, "own")
	require.NoError(t, err)
	assert.Equal(t, &MCPPromptInfo{MCPPrompt: MCPPrompt{Name: "own", Description: "muster's own"}}, got)

	got, err = findPrompt(ctx, describeCatalogue, native, "x_docs_other")
	require.NoError(t, err)
	assert.Nil(t, got)
}
