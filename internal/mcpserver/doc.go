// Package mcpserver provides Model Context Protocol (MCP) server management for muster.
//
// This package handles the lifecycle of MCP servers, which provide AI assistants
// with structured access to Kubernetes clusters and monitoring services through
// local process execution.
//
// # Overview
//
// MCP servers act as bridges between AI assistants (like Claude in Cursor) and
// backend services. They expose tools and resources through a standardized protocol,
// allowing AI assistants to query and interact with systems in a controlled manner.
//
// The package provides comprehensive MCP server definition management, allowing
// users to create, update, and delete server configurations through both API
// operations and direct YAML file manipulation.
//
// # Server Types
//
// The package supports local command deployment:
//
// ## Local Command
//   - Runs MCP servers as local processes
//   - Direct execution of binaries or scripts
//   - Environment variable configuration
//   - Process lifecycle management
//   - Standard input/output handling
//
// # Core Components
//
// ## Process Management
//   - Start/stop MCP server processes
//   - Monitor process health
//   - Capture and log output
//   - Handle graceful shutdown
//   - Environment variable injection
//
// ## Proxy Server
//   - HTTP proxy for MCP protocol
//   - SSE (Server-Sent Events) support
//   - Request forwarding to MCP servers
//   - Connection pooling and management
//   - Protocol translation
//
// # Configuration
//
// MCP servers are configured through YAML definitions with:
//   - **Name**: Unique identifier for the server
//   - **Type**: "localCommand"
//   - **Command**: Execution command and arguments
//   - **Environment**: Variables to set for the server
//   - **Dependencies**: Required services or conditions
//   - **Metadata**: Additional descriptive information
//
// ## Local Command Configuration
//
//	name: "kubernetes"
//	type: "localCommand"
//	command: ["npx", "mcp-server-kubernetes"]
//	env:
//	  KUBECONFIG: "/home/user/.kube/config"
//	  NAMESPACE_FILTER: "default,kube-system"
//
// # Health Monitoring
//
// Server health is determined by:
//   - Process running status
//   - Proxy server availability
//   - MCP protocol responsiveness
//   - Tool availability checks
//   - Connection health monitoring
//
// # Integration with AI Assistants
//
// MCP servers are accessed by AI assistants through:
//
//  1. **SSE endpoint**: http://localhost:{proxyPort}/sse
//  2. **JSON-RPC protocol** over SSE
//  3. **Tool discovery and invocation**
//  4. **Structured responses**
//
// Example Cursor configuration:
//
//	{
//	  "mcpServers": {
//	    "kubernetes": {
//	      "command": "curl",
//	      "args": ["-N", "http://localhost:8001/sse"]
//	    }
//	  }
//	}
//
// # Validation
//
// The package provides comprehensive validation for MCP server definitions:
//
//   - **Name validation**: Ensures unique and valid identifiers
//   - **Type validation**: Verifies server type is supported
//   - **Command validation**: For local command servers, ensures command is specified
//   - **Environment validation**: Validates environment variable format
//   - **Cross-type validation**: Prevents invalid combinations of fields
//
// # Error Handling
//
// The package handles various error scenarios:
//   - Binary not found for local command servers
//   - Port already in use
//   - Network connectivity problems
//   - Protocol errors
//   - Definition validation failures
//   - YAML parsing errors
//
// # API Integration
//
// The package integrates with muster's API layer through:
//   - **MCPServerManagerHandler**: API interface for server management
//   - **Registration pattern**: Proper API layer registration
//   - **Tool provider interface**: Exposing management tools
//   - **Event integration**: Server lifecycle event broadcasting
//
// # Thread Safety
//
// All operations are thread-safe:
//   - Multiple MCP servers can be managed concurrently
//   - Concurrent definition operations
//   - Thread-safe access to server registry
//   - Protected storage operations
//
// # Performance Considerations
//
//   - **Efficient loading**: Lazy loading of definitions when needed
//   - **Caching**: In-memory caching of loaded definitions
//   - **Validation**: Early validation to prevent runtime errors
//   - **Resource cleanup**: Proper cleanup of server resources
//
// This package provides the foundation for muster's MCP server ecosystem,
// enabling seamless integration with AI assistants and other external tools
// through the standardized MCP protocol.
package mcpserver
