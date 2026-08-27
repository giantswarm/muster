package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

func stdioDefinition() *api.MCPServer {
	return &api.MCPServer{
		Name:    "stdio-server",
		Type:    api.MCPServerTypeStdio,
		Command: "echo",
		Args:    []string{"hello"},
	}
}

// TestStdioRefusedInKubernetesMode covers the layer that actually spawns the
// process: whatever reached it, no subprocess is created inside the muster pod
// (issue #1067).
func TestStdioRefusedInKubernetesMode(t *testing.T) {
	svc, err := NewService(stdioDefinition(), WithKubernetesMode(true))
	require.NoError(t, err)

	err = svc.ValidateConfiguration()
	require.Error(t, err)
	assert.ErrorIs(t, err, api.ErrStdioNotAllowedInKubernetesMode)

	// Start goes through createAndInitializeClient, which refuses on its own
	// rather than relying on a prior ValidateConfiguration call.
	err = svc.Start(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, api.ErrStdioNotAllowedInKubernetesMode),
		"Start must refuse the stdio definition, got: %v", err)
	assert.Nil(t, svc.client, "no MCP client may be created for a refused definition")
}

// TestStdioAllowedInFilesystemMode pins the local-CLI half: the same definition
// validates, and the service is the default one the CLI builds.
func TestStdioAllowedInFilesystemMode(t *testing.T) {
	svc, err := NewService(stdioDefinition())
	require.NoError(t, err)
	assert.NoError(t, svc.ValidateConfiguration())

	explicit, err := NewService(stdioDefinition(), WithKubernetesMode(false))
	require.NoError(t, err)
	assert.NoError(t, explicit.ValidateConfiguration())
}

// TestRemoteTypesUnaffectedByKubernetesMode guards against the gate widening
// into "Kubernetes mode refuses MCP servers".
func TestRemoteTypesUnaffectedByKubernetesMode(t *testing.T) {
	for _, serverType := range []api.MCPServerType{api.MCPServerTypeStreamableHTTP, api.MCPServerTypeSSE} {
		t.Run(string(serverType), func(t *testing.T) {
			svc, err := NewService(&api.MCPServer{
				Name: "remote-server",
				Type: serverType,
				URL:  "http://example.com/mcp",
			}, WithKubernetesMode(true))
			require.NoError(t, err)
			assert.NoError(t, svc.ValidateConfiguration())
		})
	}
}
