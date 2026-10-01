package cli

import (
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
	native := []MCPResource{{URI: "file:///readme", Name: "readme", MIMEType: "text/plain"}}

	got := attributeResources(listed, native)

	require.Len(t, got, 3)
	assert.Equal(t, "files", got[0].Server)
	assert.Equal(t, "files_x", got[1].Server)
	assert.Equal(t, "", got[0].Description, "the native resource is shown, not the meta-tool's name fallback")
	assert.Equal(t, "text/plain", got[1].MIMEType)
	assert.Equal(t, MCPResource{URI: "docs://guide", Name: "guide", Description: "The guide", MIMEType: "text/markdown"}, got[2].MCPResource)
	assert.Equal(t, "docs", got[2].Server)
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
	}}

	got := attributePrompts(listed, native)

	require.Len(t, got, 2)
	assert.Equal(t, "promptserver", got[0].Server)
	require.Len(t, got[0].Arguments, 1)
	assert.Equal(t, "issue", got[0].Arguments[0].Name)
	assert.Equal(t, "files", got[1].Server)
	assert.Equal(t, "Summarise", got[1].Description)
	assert.Empty(t, got[1].Arguments)
}
