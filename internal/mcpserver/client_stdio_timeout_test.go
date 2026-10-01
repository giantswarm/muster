package mcpserver

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// slowStdioServerEnv makes the test binary, started as a stdio MCPServer's
// command, serve TestSlowStdioServer instead of running the tests.
const slowStdioServerEnv = "MUSTER_TEST_SLOW_STDIO_SERVER"

// TestSlowStdioServer is not a test: it is the stdio server the tests below
// start as a subprocess, with one tool that answers after the given delay.
func TestSlowStdioServer(t *testing.T) {
	if os.Getenv(slowStdioServerEnv) == "" {
		t.Skip("the subprocess of the stdio timeout tests")
	}
	srv := mcpgoserver.NewMCPServer("slow", "1.0.0", mcpgoserver.WithToolCapabilities(true))
	srv.AddTool(mcp.NewTool("sleep", mcp.WithString("for")), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		delay, err := time.ParseDuration(req.GetString("for", "0s"))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}
		return mcp.NewToolResultText("slept " + delay.String()), nil
	})
	_ = mcpgoserver.ServeStdio(srv)
	os.Exit(0)
}

// slowStdioClient starts the slow stdio server through the factory, as the
// MCPServer service does, with the given spec.timeout.
func slowStdioClient(t *testing.T, timeout time.Duration) MCPClient {
	t.Helper()
	client, err := NewMCPClientFromType(api.MCPServerTypeStdio, MCPClientConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestSlowStdioServer$"},
		Env:     map[string]string{slowStdioServerEnv: "1"},
		Timeout: timeout,
	})
	require.NoError(t, err)
	require.NoError(t, client.Initialize(t.Context()))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// A stdio tool call runs under the server's spec.timeout, not the default:
// one that outlasts it is cut when the budget runs out.
func TestStdioToolCall_RunsUnderSpecTimeout(t *testing.T) {
	client := slowStdioClient(t, 300*time.Millisecond)
	start := time.Now()

	_, err := client.CallTool(t.Context(), "sleep", map[string]interface{}{"for": "5s"})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.EqualError(t, err, "no answer within the server's timeout of 300ms: context deadline exceeded")
	assert.Less(t, time.Since(start), 4*time.Second, "the call was cut by spec.timeout, not by the tool")
}

// A stdio tool call within the server's spec.timeout completes.
func TestStdioToolCall_WithinSpecTimeoutCompletes(t *testing.T) {
	client := slowStdioClient(t, 5*time.Second)

	res, err := client.CallTool(t.Context(), "sleep", map[string]interface{}{"for": "200ms"})

	require.NoError(t, err)
	require.Len(t, res.Content, 1)
	assert.Equal(t, "slept 200ms", res.Content[0].(mcp.TextContent).Text)
}

// Every transport the factory builds runs its operations under the server's
// spec.timeout: with spec.timeout: 60 a 45 s tool call is within budget.
func TestNewMCPClientFromType_EveryTransportGetsSpecTimeout(t *testing.T) {
	config := MCPClientConfig{Command: "unused", URL: "http://backend/mcp", Timeout: 60 * time.Second}

	for _, serverType := range []api.MCPServerType{api.MCPServerTypeStdio, api.MCPServerTypeStreamableHTTP, api.MCPServerTypeSSE} {
		t.Run(string(serverType), func(t *testing.T) {
			client, err := NewMCPClientFromType(serverType, config)
			require.NoError(t, err)
			var base *baseMCPClient
			switch c := client.(type) {
			case *StdioClient:
				base = &c.baseMCPClient
			case *StreamableHTTPClient:
				base = &c.baseMCPClient
			case *SSEClient:
				base = &c.baseMCPClient
			}
			require.NotNil(t, base, "unexpected client type %T", client)

			_, cancel, timeout := base.operationContext(t.Context())
			defer cancel()

			assert.Equal(t, 60*time.Second, timeout)
		})
	}
}
