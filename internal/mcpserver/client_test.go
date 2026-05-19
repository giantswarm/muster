package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMCPClientInterfaceCompliance verifies that all live MCP client types
// satisfy MCPClient. Compile-time assertions in client_interface.go cover
// this too; the runtime form documents which transports actually ship.
func TestMCPClientInterfaceCompliance(t *testing.T) {
	var _ MCPClient = (*StreamableHTTPClient)(nil)
	var _ MCPClient = (*DynamicAuthClient)(nil)
}

func TestNewStreamableHTTPClientWithHeaders(t *testing.T) {
	headers := map[string]string{"Authorization": "Bearer token"}
	client := NewStreamableHTTPClientWithHeaders("http://example.com/mcp", headers)

	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com/mcp", client.url)
	assert.Equal(t, headers, client.headers)
	assert.False(t, client.connected)
}

func TestNewStreamableHTTPClientWithNilHeaders(t *testing.T) {
	client := NewStreamableHTTPClientWithHeaders("http://example.com/mcp", nil)

	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com/mcp", client.url)
	require.NotNil(t, client.headers)
	assert.Empty(t, client.headers)
}

func TestNewDynamicAuthClientWithNilStore(t *testing.T) {
	client := NewDynamicAuthClient("http://example.com/mcp", nil, "openid")

	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com/mcp", client.url)
	assert.Equal(t, "openid", client.scope)
	assert.False(t, client.connected)
}

func TestBaseMCPClientCheckConnected(t *testing.T) {
	base := &baseMCPClient{}

	err := base.checkConnected()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client not connected")

	base.connected = true
	err = base.checkConnected()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client not connected")
}

func TestBaseMCPClientCloseNotConnected(t *testing.T) {
	base := &baseMCPClient{}
	require.NoError(t, base.closeClient())
}

func TestClientOperationsWithoutConnection(t *testing.T) {
	t.Run("StreamableHTTPClient", func(t *testing.T) {
		testClientNotConnected(t, NewStreamableHTTPClientWithHeaders("http://example.com/mcp", nil))
	})
	t.Run("DynamicAuthClient", func(t *testing.T) {
		testClientNotConnected(t, NewDynamicAuthClient("http://example.com/mcp", nil, "openid"))
	})
}

func testClientNotConnected(t *testing.T, client MCPClient) {
	t.Helper()
	ctx := t.Context()

	_, err := client.ListTools(ctx)
	assert.Error(t, err)

	_, err = client.CallTool(ctx, "test", nil)
	assert.Error(t, err)

	_, err = client.ListResources(ctx)
	assert.Error(t, err)

	_, err = client.ReadResource(ctx, "test://resource")
	assert.Error(t, err)

	_, err = client.ListPrompts(ctx)
	assert.Error(t, err)

	_, err = client.GetPrompt(ctx, "test", nil)
	assert.Error(t, err)

	err = client.Ping(ctx)
	assert.Error(t, err)
}
