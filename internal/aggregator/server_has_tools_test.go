package aggregator

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAggregatorServer_ServerHasTools guards the answer the orchestrator waits
// on after starting a server (#1621): it holds for a server registered under
// its key with tools, whatever prefix they are exposed under, and does not
// hold for a server that waits for a sign-in, so that wait cannot succeed
// before the server's tools exist.
func TestAggregatorServer_ServerHasTools(t *testing.T) {
	ctx := context.Background()
	reg := NewServerRegistry("x")
	require.NoError(t, reg.Register(ctx, ServerRegistration{
		Name:       "org-a/deepwiki",
		Namespace:  "org-a",
		ToolPrefix: "deepwiki",
	}, &mockMCPClient{tools: []mcp.Tool{{Name: "ask_question"}}}))
	require.NoError(t, reg.Register(ctx, ServerRegistration{Name: "empty"}, &mockMCPClient{}))
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "org-a/github", Namespace: "org-a"},
		URL:                "https://github.example.com/mcp",
	}))
	a := &AggregatorServer{registry: reg}

	assert.True(t, a.ServerHasTools("org-a/deepwiki"), "a tenant server with its own toolPrefix and tools")
	assert.False(t, a.ServerHasTools("org-a/github"), "a server pending a sign-in lists no tools")
	assert.False(t, a.ServerHasTools("empty"), "a server that listed no tools")
	assert.False(t, a.ServerHasTools("deepwiki"), "the toolPrefix is not the key")
}
