package mcpserver

import (
	"fmt"

	"muster/internal/api"
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
		return NewStreamableHTTPClientWithHeaders(config.URL, config.Headers), nil

	case api.MCPServerTypeSSE:
		if config.URL == "" {
			return nil, fmt.Errorf("url is required for sse type")
		}
		return NewSSEClientWithHeaders(config.URL, config.Headers), nil

	default:
		return nil, fmt.Errorf("unsupported server type: %s (supported: stdio, streamable-http, sse)", serverType)
	}
}
