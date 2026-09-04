package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	mcpserverPkg "github.com/giantswarm/muster/internal/mcpserver"
)

// TestHandleAuthRequiredServer_CarriesNamespace: the pending-auth registration
// the 401 hook files carries the MCPServer's namespace, so the aggregator's
// entry — and every Secret read that defaults to it — names the resource's own
// namespace rather than "default".
func TestHandleAuthRequiredServer_CarriesNamespace(t *testing.T) {
	stub := &stubAggregator{}
	swapAggregatorHandler(t, stub)

	o := &Orchestrator{ctx: context.Background()}
	o.handleAuthRequiredServer(&api.MCPServer{
		Name:      "github",
		Namespace: "agent-platform",
		Type:      api.MCPServerTypeStreamableHTTP,
		URL:       "https://api.githubcopilot.com/mcp/",
		Auth:      &api.MCPServerAuth{Type: "oauth"},
	}, &mcpserverPkg.AuthRequiredError{URL: "https://api.githubcopilot.com/mcp/"})

	registrations := stub.registered()
	require.Len(t, registrations, 1)
	assert.Equal(t, "github", registrations[0].Name)
	assert.Equal(t, "agent-platform", registrations[0].Namespace)
	assert.Equal(t, "https://api.githubcopilot.com/mcp/", registrations[0].URL)
}
