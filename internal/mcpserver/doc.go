// Package mcpserver provides Model Context Protocol (MCP) server management for muster.
//
// This package handles the lifecycle of MCP servers, which provide AI assistants
// with structured access to Kubernetes clusters and monitoring services. It supports
// both local process execution and containerized deployments.
//
// # Overview
//
// MCP servers act as bridges between AI assistants (like Claude in Cursor) and
// backend services. They expose tools and resources through a standardized protocol,
// allowing AI assistants to query and interact with systems in a controlled manner.
//
// # Server Types
//
// The package supports two deployment models:
//
// Local Command:
//   - Runs MCP servers as local processes
//   - Direct execution of binaries or scripts
//   - Environment variable configuration
//   - Process lifecycle management
//
// Container:
//   - Runs MCP servers in Docker containers
//   - Isolated execution environment
//   - Volume and port mapping support
//   - Automatic image management
//
// # Core Components
//
// Process Management:
//   - Start/stop MCP server processes
//   - Monitor process health
//   - Capture and log output
//   - Handle graceful shutdown
//
// Container Management:
//   - Pull required images
//   - Create and start containers
//   - Map ports and volumes
//   - Clean up on termination
//
// Proxy Server:
//   - HTTP proxy for MCP protocol
//   - SSE (Server-Sent Events) support
//   - Request forwarding to MCP servers
//   - Connection pooling and management
//
// # Configuration
//
// MCP servers are configured with:
//   - Name: Unique identifier
//   - Type: "localCommand" or "container"
//   - ProxyPort: Local port for proxy server
//   - Command/Image: Execution details
//   - Environment: Variables to set
//   - Dependencies: Required services
//
// # Health Monitoring
//
// Server health is determined by:
//   - Process/container running status
//   - Proxy server availability
//   - MCP protocol responsiveness
//   - Tool availability checks
//
// # Usage Example
//
//	// Local command MCP server
//	config := MCPServerConfig{
//	    Name:      "kubernetes",
//	    Type:      "localCommand",
//	    ProxyPort: 8001,
//	    Command:   []string{"mcp-server-kubernetes"},
//	    Env: map[string]string{
//	        "KUBECONFIG": "/home/user/.kube/config",
//	    },
//	}
//
//	// Start the server
//	server, err := StartMCPServer(ctx, config)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer server.Stop()
//
//	// Container-based MCP server
//	containerConfig := MCPServerConfig{
//	    Name:      "prometheus",
//	    Type:      "container",
//	    ProxyPort: 8002,
//	    Image:     "ghcr.io/org/mcp-prometheus:latest",
//	    Ports:     []string{"8002:8000"},
//	    Env: map[string]string{
//	        "PROMETHEUS_URL": "http://localhost:9090",
//	    },
//	}
//
// # Integration with AI Assistants
//
// MCP servers are accessed by AI assistants through:
//
//  1. SSE endpoint: http://localhost:{proxyPort}/sse
//  2. JSON-RPC protocol over SSE
//  3. Tool discovery and invocation
//  4. Structured responses
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
// # Error Handling
//
// The package handles various error scenarios:
//   - Binary not found
//   - Port already in use
//   - Container runtime issues
//   - Network connectivity problems
//   - Protocol errors
//
// # Logging
//
// Comprehensive logging includes:
//   - Server lifecycle events
//   - Process/container output
//   - Proxy request/response
//   - Error conditions
//   - Health check results
//
// # Thread Safety
//
// All operations are thread-safe. Multiple MCP servers can be
// managed concurrently without interference.
package mcpserver
