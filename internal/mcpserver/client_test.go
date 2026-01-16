package mcpserver

import (
	"testing"
	"time"

	"muster/internal/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMCPClientInterfaceCompliance verifies that all client types implement the MCPClient interface.
// This is a compile-time check that's also verified at runtime for documentation purposes.
func TestMCPClientInterfaceCompliance(t *testing.T) {
	// These are compile-time checks (the var declarations in client_interface.go)
	// but we also test at runtime for documentation
	var _ MCPClient = (*StdioClient)(nil)
	var _ MCPClient = (*SSEClient)(nil)
	var _ MCPClient = (*StreamableHTTPClient)(nil)
	var _ MCPClient = (*DynamicAuthClient)(nil)
}

// TestNewMCPClientFromType tests the factory function for creating MCP clients
func TestNewMCPClientFromType(t *testing.T) {
	tests := []struct {
		name        string
		serverType  api.MCPServerType
		config      MCPClientConfig
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid stdio client",
			serverType: api.MCPServerTypeStdio,
			config: MCPClientConfig{
				Command: "echo",
				Args:    []string{"hello"},
			},
			wantErr: false,
		},
		{
			name:       "stdio client with env",
			serverType: api.MCPServerTypeStdio,
			config: MCPClientConfig{
				Command: "echo",
				Args:    []string{"hello"},
				Env:     map[string]string{"TEST": "value"},
			},
			wantErr: false,
		},
		{
			name:        "stdio client missing command",
			serverType:  api.MCPServerTypeStdio,
			config:      MCPClientConfig{},
			wantErr:     true,
			errContains: "command is required for stdio type",
		},
		{
			name:       "valid streamable-http client",
			serverType: api.MCPServerTypeStreamableHTTP,
			config: MCPClientConfig{
				URL: "http://example.com/mcp",
			},
			wantErr: false,
		},
		{
			name:       "streamable-http client with headers",
			serverType: api.MCPServerTypeStreamableHTTP,
			config: MCPClientConfig{
				URL:     "http://example.com/mcp",
				Headers: map[string]string{"Authorization": "Bearer token"},
			},
			wantErr: false,
		},
		{
			name:        "streamable-http client missing URL",
			serverType:  api.MCPServerTypeStreamableHTTP,
			config:      MCPClientConfig{},
			wantErr:     true,
			errContains: "url is required for streamable-http type",
		},
		{
			name:       "valid sse client",
			serverType: api.MCPServerTypeSSE,
			config: MCPClientConfig{
				URL: "http://example.com/sse",
			},
			wantErr: false,
		},
		{
			name:       "sse client with headers",
			serverType: api.MCPServerTypeSSE,
			config: MCPClientConfig{
				URL:     "http://example.com/sse",
				Headers: map[string]string{"X-API-Key": "secret"},
			},
			wantErr: false,
		},
		{
			name:        "sse client missing URL",
			serverType:  api.MCPServerTypeSSE,
			config:      MCPClientConfig{},
			wantErr:     true,
			errContains: "url is required for sse type",
		},
		{
			name:        "unsupported server type",
			serverType:  api.MCPServerType("invalid"),
			config:      MCPClientConfig{},
			wantErr:     true,
			errContains: "unsupported MCP server type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewMCPClientFromType(tt.serverType, tt.config)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, client)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, client)
			}
		})
	}
}

// TestNewStdioClient tests the StdioClient constructor
func TestNewStdioClient(t *testing.T) {
	client := NewStdioClient("echo", []string{"hello"})

	assert.NotNil(t, client)
	assert.Equal(t, "echo", client.command)
	assert.Equal(t, []string{"hello"}, client.args)
	assert.NotNil(t, client.env)
	assert.Empty(t, client.env)
	assert.False(t, client.connected)
}

// TestNewStdioClientWithEnv tests the StdioClient constructor with environment variables
func TestNewStdioClientWithEnv(t *testing.T) {
	env := map[string]string{"KEY": "value", "ANOTHER": "test"}
	client := NewStdioClientWithEnv("echo", []string{"hello"}, env)

	assert.NotNil(t, client)
	assert.Equal(t, "echo", client.command)
	assert.Equal(t, []string{"hello"}, client.args)
	assert.Equal(t, env, client.env)
	assert.False(t, client.connected)
}

// TestNewStreamableHTTPClient tests the StreamableHTTPClient constructor
func TestNewStreamableHTTPClient(t *testing.T) {
	client := NewStreamableHTTPClient("http://example.com/mcp")

	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com/mcp", client.url)
	assert.NotNil(t, client.headers)
	assert.Empty(t, client.headers)
	assert.False(t, client.connected)
}

// TestNewStreamableHTTPClientWithHeaders tests the StreamableHTTPClient constructor with headers
func TestNewStreamableHTTPClientWithHeaders(t *testing.T) {
	headers := map[string]string{"Authorization": "Bearer token"}
	client := NewStreamableHTTPClientWithHeaders("http://example.com/mcp", headers)

	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com/mcp", client.url)
	assert.Equal(t, headers, client.headers)
	assert.False(t, client.connected)
}

// TestNewStreamableHTTPClientWithNilHeaders tests that nil headers are handled gracefully
func TestNewStreamableHTTPClientWithNilHeaders(t *testing.T) {
	client := NewStreamableHTTPClientWithHeaders("http://example.com/mcp", nil)

	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com/mcp", client.url)
	assert.NotNil(t, client.headers) // Should be initialized to empty map
	assert.Empty(t, client.headers)
}

// TestNewSSEClient tests the SSEClient constructor
func TestNewSSEClient(t *testing.T) {
	client := NewSSEClient("http://example.com/sse")

	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com/sse", client.url)
	assert.NotNil(t, client.headers)
	assert.Empty(t, client.headers)
	assert.False(t, client.connected)
}

// TestNewSSEClientWithHeaders tests the SSEClient constructor with headers
func TestNewSSEClientWithHeaders(t *testing.T) {
	headers := map[string]string{"X-API-Key": "secret"}
	client := NewSSEClientWithHeaders("http://example.com/sse", headers)

	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com/sse", client.url)
	assert.Equal(t, headers, client.headers)
	assert.False(t, client.connected)
}

// TestNewSSEClientWithNilHeaders tests that nil headers are handled gracefully
func TestNewSSEClientWithNilHeaders(t *testing.T) {
	client := NewSSEClientWithHeaders("http://example.com/sse", nil)

	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com/sse", client.url)
	assert.NotNil(t, client.headers) // Should be initialized to empty map
	assert.Empty(t, client.headers)
}

// TestNewDynamicAuthClient tests the DynamicAuthClient constructor
func TestNewDynamicAuthClient(t *testing.T) {
	provider := StaticTokenProvider("test-token")
	client := NewDynamicAuthClient("http://example.com/mcp", provider)

	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com/mcp", client.url)
	assert.NotNil(t, client.tokenProvider)
	assert.False(t, client.connected)
}

// TestNewDynamicAuthClientWithNilProvider tests that nil provider is handled gracefully
func TestNewDynamicAuthClientWithNilProvider(t *testing.T) {
	client := NewDynamicAuthClient("http://example.com/mcp", nil)

	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com/mcp", client.url)
	assert.NotNil(t, client.tokenProvider) // Should be initialized to no-op provider
	assert.False(t, client.connected)

	// Verify the no-op provider returns empty string
	token := client.tokenProvider.GetAccessToken(t.Context())
	assert.Empty(t, token)
}

// TestBaseMCPClientCheckConnected tests the connection check helper
func TestBaseMCPClientCheckConnected(t *testing.T) {
	base := &baseMCPClient{}

	// Not connected, no client
	err := base.checkConnected()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client not connected")

	// Connected but no client
	base.connected = true
	err = base.checkConnected()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client not connected")
}

// TestBaseMCPClientCloseNotConnected tests closing a client that's not connected
func TestBaseMCPClientCloseNotConnected(t *testing.T) {
	base := &baseMCPClient{}

	// Should not error when not connected
	err := base.closeClient()
	assert.NoError(t, err)
}

// TestClientOperationsWithoutConnection tests that all operations fail gracefully when not connected
func TestClientOperationsWithoutConnection(t *testing.T) {
	t.Run("StdioClient", func(t *testing.T) {
		client := NewStdioClient("echo", nil)
		testClientNotConnected(t, client)
	})

	t.Run("StreamableHTTPClient", func(t *testing.T) {
		client := NewStreamableHTTPClient("http://example.com/mcp")
		testClientNotConnected(t, client)
	})

	t.Run("SSEClient", func(t *testing.T) {
		client := NewSSEClient("http://example.com/sse")
		testClientNotConnected(t, client)
	})

	t.Run("DynamicAuthClient", func(t *testing.T) {
		client := NewDynamicAuthClient("http://example.com/mcp", StaticTokenProvider("test-token"))
		testClientNotConnected(t, client)
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

// TestDefaultStdioInitTimeout verifies the default timeout constant is set correctly
func TestDefaultStdioInitTimeout(t *testing.T) {
	// DefaultStdioInitTimeout should be a reasonable value for subprocess initialization
	assert.Equal(t, 10*time.Second, DefaultStdioInitTimeout,
		"DefaultStdioInitTimeout should be 10 seconds")
}
