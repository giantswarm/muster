package mcpserver

import (
	"fmt"
	"net/http"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

// MCPClientConfig contains configuration for creating an MCP client.
// This provides a unified configuration structure for all client types.
type MCPClientConfig struct {
	// Command is the executable path for stdio servers
	Command string
	// Args are the command line arguments for stdio servers
	Args []string
	// Env contains environment variables for stdio servers
	Env map[string]string
	// URL is the endpoint for remote servers (streamable-http, sse)
	URL string
	// Headers are HTTP headers for remote servers
	Headers map[string]string
	// HTTPClient is a custom HTTP client to use for remote servers.
	// When set, this client is used instead of the default.
	// Used for Teleport authentication with custom TLS certificates.
	HTTPClient *http.Client
}

// NewMCPClientFromType creates the appropriate MCP client based on the server type.
// This factory function simplifies client creation by encapsulating the logic
// for choosing the correct client implementation.
//
// Supported types:
//   - "stdio": Creates a StdioClient for local subprocess communication
//   - "streamable-http": Creates a StreamableHTTPClient for HTTP-based servers
//   - "sse": Creates an SSEClient for Server-Sent Events communication
//
// If config.HTTPClient is provided (e.g., for Teleport TLS authentication),
// it will be used instead of the default HTTP client for remote server types.
//
// Returns an error if the server type is not recognized.
func NewMCPClientFromType(serverType api.MCPServerType, config MCPClientConfig) (MCPClient, error) {
	switch serverType {
	case api.MCPServerTypeStdio:
		if config.Command == "" {
			return nil, fmt.Errorf("command is required for stdio type")
		}
		return NewStdioClientWithEnv(config.Command, config.Args, config.Env), nil

	case api.MCPServerTypeStreamableHTTP:
		if config.URL == "" {
			return nil, fmt.Errorf("url is required for streamable-http type")
		}
		// Use custom HTTP client if provided (e.g., for Teleport authentication)
		if config.HTTPClient != nil {
			logging.Debug("MCPClientFactory", "Creating StreamableHTTP client with custom HTTP client for %s", config.URL)
			return NewStreamableHTTPClientWithHTTPClient(config.URL, config.Headers, config.HTTPClient), nil
		}
		return NewStreamableHTTPClientWithHeaders(config.URL, config.Headers), nil

	case api.MCPServerTypeSSE:
		if config.URL == "" {
			return nil, fmt.Errorf("url is required for sse type")
		}
		// Note: SSE client doesn't support custom HTTP clients yet
		// Teleport authentication for SSE would require extending SSEClient similarly
		if config.HTTPClient != nil {
			logging.Warn("MCPClientFactory", "Custom HTTP client not supported for SSE type, ignoring")
		}
		return NewSSEClientWithHeaders(config.URL, config.Headers), nil

	default:
		return nil, fmt.Errorf("unsupported MCP server type: %s (supported: %s, %s, %s)",
			serverType, api.MCPServerTypeStdio, api.MCPServerTypeStreamableHTTP, api.MCPServerTypeSSE)
	}
}
