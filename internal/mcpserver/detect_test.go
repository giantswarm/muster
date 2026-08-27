package mcpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMCPServer() *server.MCPServer {
	return server.NewMCPServer("detect-test-server", "1.2.3")
}

func TestDetectTransport_StreamableHTTP(t *testing.T) {
	ts := httptest.NewServer(server.NewStreamableHTTPServer(newTestMCPServer(), server.WithStateful(true)))
	defer ts.Close()

	result := DetectTransport(context.Background(), ts.URL+"/mcp", nil, 5*time.Second)

	assert.Equal(t, string(api.MCPServerTypeStreamableHTTP), result.Transport)
	assert.True(t, result.Reachable)
	assert.False(t, result.RequiresAuth)
	assert.Equal(t, "detect-test-server", result.ServerName)
	assert.Equal(t, "1.2.3", result.ServerVersion)
}

func TestDetectTransport_SSE(t *testing.T) {
	// The SSE server needs its base URL up front so the endpoint event it
	// sends points at a reachable message endpoint. Reserve a port first.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	baseURL := fmt.Sprintf("http://%s", listener.Addr().String())

	sseServer := server.NewSSEServer(
		newTestMCPServer(),
		server.WithBaseURL(baseURL),
		server.WithSSEEndpoint("/sse"),
		server.WithMessageEndpoint("/message"),
	)
	httpServer := &http.Server{Handler: sseServer} //nolint:gosec
	go func() { _ = httpServer.Serve(listener) }()
	defer func() { _ = httpServer.Close() }()

	result := DetectTransport(context.Background(), baseURL+"/sse", nil, 5*time.Second)

	assert.Equal(t, string(api.MCPServerTypeSSE), result.Transport)
	assert.True(t, result.Reachable)
	assert.False(t, result.RequiresAuth)
	assert.Equal(t, "detect-test-server", result.ServerName)
	assert.Equal(t, "1.2.3", result.ServerVersion)
}

func TestDetectTransport_AuthProtected(t *testing.T) {
	// A server that 401-challenges everything: detection cannot complete a
	// handshake, but the challenge identifies a live, OAuth-protected MCP
	// endpoint. The modern challenge flow implies streamable-http.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+r.Host+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	result := DetectTransport(context.Background(), ts.URL+"/mcp", nil, 5*time.Second)

	assert.Equal(t, string(api.MCPServerTypeStreamableHTTP), result.Transport)
	assert.True(t, result.Reachable)
	assert.True(t, result.RequiresAuth)
}

func TestDetectTransport_Unreachable(t *testing.T) {
	// Reserve a port and close it again so nothing is listening there.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	url := fmt.Sprintf("http://%s/mcp", listener.Addr().String())
	require.NoError(t, listener.Close())

	result := DetectTransport(context.Background(), url, nil, 5*time.Second)

	assert.Equal(t, TransportUnknown, result.Transport)
	assert.False(t, result.Reachable)
	assert.False(t, result.RequiresAuth)
	assert.NotEmpty(t, result.Detail)
}

func TestDetectTransport_NonMCPServer(t *testing.T) {
	// A plain HTTP server that answers 200 text/html to everything fits
	// neither transport and must come back unknown, not as a false positive.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not an MCP server</html>"))
	}))
	defer ts.Close()

	result := DetectTransport(context.Background(), ts.URL, nil, 5*time.Second)

	assert.Equal(t, TransportUnknown, result.Transport)
}

func TestHandleMCPServerDetect_RequiresURL(t *testing.T) {
	adapter := &Adapter{}

	result, err := adapter.handleMCPServerDetect(context.Background(), map[string]interface{}{})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestHandleMCPServerDetect_ReturnsStructuredResult(t *testing.T) {
	ts := httptest.NewServer(server.NewStreamableHTTPServer(newTestMCPServer(), server.WithStateful(true)))
	defer ts.Close()

	adapter := &Adapter{}
	result, err := adapter.handleMCPServerDetect(context.Background(), map[string]interface{}{
		"url":     ts.URL + "/mcp",
		"timeout": 5,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	detection, ok := result.StructuredContent.(*TransportDetectionResult)
	require.True(t, ok, "structuredContent should carry the detection result")
	assert.Equal(t, string(api.MCPServerTypeStreamableHTTP), detection.Transport)

	require.Len(t, result.Content, 1)
	content, ok := result.Content[0].(*TransportDetectionResult)
	require.True(t, ok, "content should carry the detection result for JSON serialization")
	assert.Equal(t, detection, content)
}
