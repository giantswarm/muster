package mcpserver

import (
	"fmt"

	"github.com/giantswarm/muster/internal/api"
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
	// Meta contains entries merged into the params._meta object of every
	// outbound JSON-RPC request that carries params
	Meta map[string]string
	// Auth is the authentication configuration for remote servers. Only the
	// machine-identity modes are handled here; the session-scoped OAuth modes
	// are resolved by the aggregator, not by this factory.
	Auth *api.MCPServerAuth
}

// NewMCPClientFromType creates the appropriate MCP client based on the server type.
//
// Supported types:
//   - "stdio": Creates a StdioClient for local subprocess communication
//   - "streamable-http": Creates a StreamableHTTPClient for HTTP-based servers,
//     or a SigV4-signing one when the auth type is "sigv4"
//   - "sse": Creates an SSEClient for Server-Sent Events communication
func NewMCPClientFromType(serverType api.MCPServerType, config MCPClientConfig) (MCPClient, error) {
	// The single runtime enforcement point for the auth-versus-type rules: this
	// is the only function that holds both the server type and the auth config,
	// and every path that opens a connection comes through it. Admission checks
	// the same rules earlier so a caller sees the error before the CR is written.
	if err := api.ValidateSigV4(string(serverType), config.Auth); err != nil {
		return nil, err
	}
	// Same reason: this is the only function that holds both the server type
	// and the meta map, so a map that no transport would inject is refused
	// here rather than dropped on the way to the wire.
	if err := api.ValidateMetaAllowed(string(serverType), config.Meta); err != nil {
		return nil, err
	}

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
		if config.Auth != nil && config.Auth.Type == api.MCPServerAuthTypeSigV4 {
			// ValidateSigV4 above guarantees a non-nil block with a region.
			return newSigV4Client(config.URL, config.Headers, *config.Auth.SigV4, config.Meta)
		}
		return NewStreamableHTTPClientWithHeaders(config.URL, config.Headers).WithMeta(config.Meta), nil

	case api.MCPServerTypeSSE:
		if config.URL == "" {
			return nil, fmt.Errorf("url is required for sse type")
		}
		return NewSSEClientWithHeaders(config.URL, config.Headers).WithMeta(config.Meta), nil

	default:
		return nil, fmt.Errorf("unsupported MCP server type: %s (supported: %s, %s, %s)",
			serverType, api.MCPServerTypeStdio, api.MCPServerTypeStreamableHTTP, api.MCPServerTypeSSE)
	}
}
