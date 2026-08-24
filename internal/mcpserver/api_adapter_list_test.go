package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/client"
	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

var errListMCPServers = errors.New("connection refused")

// listingClient answers ListMCPServers and nothing else. The embedded interface
// is nil, so a call to any other method panics rather than passing silently.
type listingClient struct {
	client.MusterClient
	servers []musterv1alpha1.MCPServer
	err     error
}

func (c *listingClient) ListMCPServers(context.Context, string) ([]musterv1alpha1.MCPServer, error) {
	return c.servers, c.err
}

// TestListMCPServersReportsReadFailures keeps a failed read from reaching a
// caller as an empty list. A caller that mints a token from the result cannot
// tell the two apart, and an ID token minted without an audience cannot be
// repaired without a new login.
func TestListMCPServersReportsReadFailures(t *testing.T) {
	adapter := NewAdapterWithClient(&listingClient{err: errListMCPServers}, "muster")

	servers, err := adapter.ListMCPServers(t.Context())

	require.ErrorIs(t, err, errListMCPServers)
	assert.Nil(t, servers)
}

// TestHandleMCPServerListReportsReadFailures pins the same distinction on the
// tool path: the caller gets an error, not an empty server list.
func TestHandleMCPServerListReportsReadFailures(t *testing.T) {
	adapter := NewAdapterWithClient(&listingClient{err: errListMCPServers}, "muster")

	result, err := adapter.ExecuteTool(t.Context(), "mcpserver_list", nil)

	require.ErrorIs(t, err, errListMCPServers)
	assert.Nil(t, result)
}

// TestListMCPServersReportsAnEmptySet covers the other state: the read
// succeeded and no MCPServer exists.
func TestListMCPServersReportsAnEmptySet(t *testing.T) {
	adapter := NewAdapterWithClient(&listingClient{}, "muster")

	servers, err := adapter.ListMCPServers(t.Context())

	require.NoError(t, err)
	assert.Empty(t, servers)
}
