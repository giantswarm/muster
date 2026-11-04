package mcpserver

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

const testFormURL = "https://portal.example.com/agents/credentials/new"

// newElicitingBackend serves a tool that, like a backend following the
// protocol, answers with a URL elicitation only to a client that declared
// URL mode elicitation, and with its text answer to any other.
func newElicitingBackend(t *testing.T) string {
	t.Helper()
	srv := server.NewMCPServer("eliciting-backend", "test")
	srv.AddTool(mcp.NewTool("create"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		session, ok := server.ClientSessionFromContext(ctx).(server.SessionWithClientInfo)
		if ok {
			if declared := session.GetClientCapabilities(); declared.Elicitation != nil && declared.Elicitation.URL != nil {
				return nil, mcp.URLElicitationRequiredError{Elicitations: []mcp.ElicitationParams{{
					Mode: mcp.ElicitationModeURL, Message: "A model key is missing.", URL: testFormURL, ElicitationID: "e-1",
				}}}
			}
		}
		return mcp.NewToolResultText("A model key is missing: add one in the Dev Portal."), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(srv))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestDeclareURLElicitation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("a declared client gets the backend's URL elicitation", func(t *testing.T) {
		c := NewStreamableHTTPClientWithHeaders(newElicitingBackend(t), nil)
		c.DeclareURLElicitation()
		t.Cleanup(func() { _ = c.Close() })
		require.NoError(t, c.Initialize(ctx))

		_, err := c.CallTool(ctx, "create", nil)

		var required mcp.URLElicitationRequiredError
		require.True(t, errors.As(err, &required), "err: %v", err)
		require.Len(t, required.Elicitations, 1)
		require.Equal(t, testFormURL, required.Elicitations[0].URL)
	})

	t.Run("an undeclared client gets the backend's text answer", func(t *testing.T) {
		c := NewStreamableHTTPClientWithHeaders(newElicitingBackend(t), nil)
		t.Cleanup(func() { _ = c.Close() })
		require.NoError(t, c.Initialize(ctx))

		result, err := c.CallTool(ctx, "create", nil)

		require.NoError(t, err)
		require.Len(t, result.Content, 1)
		require.Equal(t, "A model key is missing: add one in the Dev Portal.", result.Content[0].(mcp.TextContent).Text)
	})

	t.Run("the factory declares it for a server that relays", func(t *testing.T) {
		client, err := NewMCPClientFromType("streamable-http", MCPClientConfig{URL: newElicitingBackend(t), URLElicitation: true})
		require.NoError(t, err)
		t.Cleanup(func() { _ = client.Close() })
		require.NoError(t, client.Initialize(ctx))

		_, err = client.CallTool(ctx, "create", nil)

		require.True(t, errors.As(err, new(mcp.URLElicitationRequiredError)), "err: %v", err)
	})
}
